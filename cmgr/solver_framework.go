package cmgr

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
)

func (m *Manager) runSolver(instance InstanceId) error {
	iMeta, err := m.lookupInstanceMetadata(instance)
	if err != nil {
		return err
	}

	bMeta, err := m.lookupBuildMetadata(iMeta.Build)
	if err != nil {
		return err
	}

	cMeta, err := m.lookupChallengeMetadata(bMeta.Challenge)
	if err != nil {
		return err
	}

	if !cMeta.SolveScript {
		return fmt.Errorf("no solve script for '%s'", cMeta.Id)
	}

	solveCtx := m.createSolveContext(bMeta)

	imageName := fmt.Sprintf("%s/%s:%d", bMeta.Challenge, "solver", bMeta.Id)
	opts := types.ImageBuildOptions{Tags: []string{imageName}}

	// Build the base image (will run the solver)
	resp, err := m.cli.ImageBuild(m.ctx, solveCtx, opts)
	if err != nil {
		m.log.errorf("failed to build solver image: %s", err)
		return err
	}

	_, err = ioutil.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		m.log.errorf("failed to read build response from docker: %s", err)
		return err
	}

	iro := types.ImageRemoveOptions{Force: false, PruneChildren: true}
	// Defer the image deletion
	defer m.cli.ImageRemove(m.ctx, imageName, iro)

	// Create a container & run the solver
	cConfig := container.Config{
		Image:    imageName,
		Hostname: "solve",
	}

	hConfig := container.HostConfig{}

	nConfig := network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			"cmgr-internal": {
				NetworkID: iMeta.Network,
				Aliases:   []string{"solver"},
			},
		},
	}

	respCC, err := m.cli.ContainerCreate(m.ctx, &cConfig, &hConfig, &nConfig, "")
	if err != nil {
		m.log.errorf("failed to create solve container: %s", err)
		return err
	}
	cid := respCC.ID

	cro := types.ContainerRemoveOptions{RemoveVolumes: true, Force: true}
	defer m.cli.ContainerRemove(m.ctx, cid, cro)

	err = m.cli.ContainerStart(m.ctx, cid, types.ContainerStartOptions{})
	if err != nil {
		m.log.errorf("failed to start solve container: %s", err)
		return err
	}

	_, err = m.cli.ContainerWait(m.ctx, cid)
	if err != nil {
		m.log.errorf("failed to wait on solve container: %s", err)
		return err
	}

	// Copy out the flag & compare
	flagFileTar, _, err := m.cli.CopyFromContainer(m.ctx, cid, "/solve/flag")
	if err != nil {
		m.log.errorf("could not find flag file: %s", err)
		return err
	}
	defer flagFileTar.Close()

	fTar := tar.NewReader(flagFileTar)
	for _, err = fTar.Next(); err == nil; _, err = fTar.Next() {
		flag, err := ioutil.ReadAll(fTar)
		if err != nil {
			m.log.errorf("could not read flag file: %s", err)
			return err
		}

		flagStr := strings.TrimSpace(string(flag))
		if flagStr == bMeta.Flag {
			return nil
		}

		return fmt.Errorf("solve script returned incorrect flag: received '%s', expected '%s'", flagStr, bMeta.Flag)
	}

	if err != io.EOF {
		m.log.errorf("error during file copy: %s", err)
		return err
	}

	return errors.New("failed to process flag results properly")
}

func (m *Manager) createSolveContext(meta *BuildMetadata) io.Reader {
	r, w := io.Pipe()
	ctx := tar.NewWriter(w)

	customDocker := false

	go func() {
		cMeta, err := m.lookupChallengeMetadata(meta.Challenge)
		if err != nil {
			w.CloseWithError(err)
		}

		// Copy in contents of the "solver" directory
		solveDir := filepath.Join(filepath.Dir(cMeta.Path), "solver")
		err = filepath.Walk(solveDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			if path == solveDir {
				return nil
			}

			if path == filepath.Join(solveDir, "Dockerfile") {
				customDocker = true
			}

			hdr, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return err
			}

			archivePath := path[len(solveDir)+1:]
			hdr.Name = archivePath

			err = ctx.WriteHeader(hdr)
			if err != nil {
				return err
			}

			if info.IsDir() {
				return nil
			}

			fd, err := os.Open(path)
			if err != nil {
				return err
			}

			_, err = io.Copy(ctx, fd)
			if err != nil {
				return err
			}
			fd.Close()

			return nil
		})

		if err != nil {
			w.CloseWithError(err)
			return
		}

		if !customDocker {
			// Insert the default
			hdr := tar.Header{Name: "Dockerfile", Mode: 0644, Size: int64(len(m.challengeDockerfiles["solver"]))}
			err = ctx.WriteHeader(&hdr)
			if err != nil {
				w.CloseWithError(err)
				return
			}

			_, err = ctx.Write(m.challengeDockerfiles["solver"])
			if err != nil {
				w.CloseWithError(err)
				return
			}
		}

		if meta.HasArtifacts {
			sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%x", meta.Challenge, meta.Format, meta.Seed)))
			sumStr := fmt.Sprintf("%x", sum)
			artifactsPath := filepath.Join(m.artifactsDir, fmt.Sprintf("%s.tar.gz", sumStr))
			artifactsFile, err := os.Open(artifactsPath)
			if err != nil {
				w.CloseWithError(err)
				return
			}

			defer artifactsFile.Close()

			artGz, err := gzip.NewReader(artifactsFile)
			if err != nil {
				w.CloseWithError(err)
				return
			}

			artTar := tar.NewReader(artGz)

			// Copy them in
			var h *tar.Header
			for h, err = artTar.Next(); err == nil; h, err = artTar.Next() {
				err = ctx.WriteHeader(h)
				if err != nil {
					w.CloseWithError(err)
					return
				}

				if h.Typeflag != tar.TypeDir {
					_, err = io.Copy(ctx, artTar)
					if err != nil {
						w.CloseWithError(err)
						return
					}
				}
			}

			if err != io.EOF {
				w.CloseWithError(err)
				return
			}

			err = artGz.Close()
			if err != nil {
				w.CloseWithError(err)
				return
			}
		}

		if len(meta.LookupData) > 0 {
			// Create the metadata.json file
			data, err := json.Marshal(meta.LookupData)
			if err != nil {
				w.CloseWithError(err)
				return
			}

			hdr := tar.Header{Name: "metadata.json", Mode: 0644, Size: int64(len(data))}
			err = ctx.WriteHeader(&hdr)
			if err != nil {
				w.CloseWithError(err)
				return
			}

			_, err = ctx.Write(data)
			if err != nil {
				w.CloseWithError(err)
				return
			}
		}

		err = ctx.Close()
		w.CloseWithError(err)
		return
	}()

	return r
}
