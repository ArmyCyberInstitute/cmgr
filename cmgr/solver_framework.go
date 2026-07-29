package cmgr

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

func (m *Manager) runSolver(instance InstanceId) error {
	solverTimeout := m.policy.SolverTimeout
	if solverTimeout == 0 {
		solverTimeout = 5 * time.Minute
	}
	operationContext, cancel := context.WithTimeout(m.ctx, solverTimeout)
	defer cancel()
	maxLogBytes := m.policy.MaxSolverLogBytes
	if maxLogBytes == 0 {
		maxLogBytes = 1024 * 1024
	}
	maxFlagBytes := m.policy.MaxSolverFlagBytes
	if maxFlagBytes == 0 {
		maxFlagBytes = 4 * 1024
	}

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

	solveContext := m.createSolveContext(bMeta)

	imageName := fmt.Sprintf("%s/%s:%d", bMeta.Challenge, "solver", bMeta.Id)
	opts := client.ImageBuildOptions{Remove: true, Tags: []string{imageName}}

	// Build the base image (will run the solver)
	resp, err := m.cli.ImageBuild(operationContext, solveContext, opts)
	if err != nil {
		m.log.errorf("failed to build solver image: %s", err)
		return err
	}

	if err := consumeDockerProgress(resp.Body, "solver image build"); err != nil {
		m.log.error(err)
		return err
	}

	iro := client.ImageRemoveOptions{Force: false, PruneChildren: true}
	// Defer the image deletion
	defer m.cli.ImageRemove(m.ctx, imageName, iro)

	// Create a container & run the solver
	cConfig := container.Config{
		Image:    imageName,
		Hostname: "solve",
		Tty:      true,
	}

	hConfig := container.HostConfig{}

	netname := iMeta.getNetworkName()
	nConfig := network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			netname: {
				NetworkID: netname,
				Aliases:   []string{"solver"},
			},
		},
	}

	respCC, err := m.cli.ContainerCreate(
		operationContext,
		client.ContainerCreateOptions{
			Config:           &cConfig,
			HostConfig:       &hConfig,
			NetworkingConfig: &nConfig,
		},
	)
	if err != nil {
		m.log.errorf("failed to create solve container: %s", err)
		return err
	}
	cid := respCC.ID

	if err := m.retireContainer(cid); err != nil {
		removeOptions := client.ContainerRemoveOptions{
			RemoveVolumes: true,
			Force:         true,
		}
		_, removeErr := m.cli.ContainerRemove(m.ctx, cid, removeOptions)
		return errors.Join(
			fmt.Errorf("could not track temporary solver container %s: %w", cid, err),
			removeErr,
		)
	}
	defer func() {
		if cleanupErr := m.removeRetiredContainerIDs(
			[]string{cid},
		); cleanupErr != nil {
			m.log.warnf(
				"could not remove temporary solver container %s: %v",
				cid,
				cleanupErr,
			)
		}
	}()

	_, err = m.cli.ContainerStart(operationContext, cid, client.ContainerStartOptions{})
	if err != nil {
		m.log.errorf("failed to start solve container: %s", err)
		return err
	}

	waitResult := m.cli.ContainerWait(
		operationContext,
		cid,
		client.ContainerWaitOptions{Condition: container.WaitConditionNotRunning},
	)
	select {
	case err := <-waitResult.Error:
		if err == nil {
			err = errors.New("Docker solver wait ended without a result")
		}
		m.log.errorf("failed to wait on solve container: %s", err)
		return err
	case _ = <-waitResult.Result:
	case <-operationContext.Done():
		return fmt.Errorf("solver exceeded %s: %w", solverTimeout, operationContext.Err())
	}

	// Copy out the flag & compare
	copyResult, err := m.cli.CopyFromContainer(
		operationContext,
		cid,
		client.CopyFromContainerOptions{SourcePath: "/solve/flag"},
	)
	if err != nil {
		m.log.errorf("could not find flag file: %s", err)
		clo := client.ContainerLogsOptions{
			ShowStdout: true,
			ShowStderr: true,
		}
		logs, lerr := m.cli.ContainerLogs(operationContext, cid, clo)
		if lerr != nil {
			m.log.errorf("could not access error logs: %s", lerr)
			err = lerr
		} else {
			defer logs.Close()
			s, lerr := ioutil.ReadAll(io.LimitReader(logs, maxLogBytes+1))
			if lerr != nil {
				m.log.errorf("could not read logs: %s", lerr)
				err = lerr
			} else {
				if int64(len(s)) > maxLogBytes {
					s = append(s[:maxLogBytes], []byte("\n[solver log truncated]")...)
				}
				m.log.errorf("logs from failed container: %s", s)
			}
		}

		return err
	}
	flagFileTar := copyResult.Content
	defer flagFileTar.Close()

	fTar := tar.NewReader(flagFileTar)
	for {
		header, nextErr := fTar.Next()
		if nextErr == io.EOF {
			err = io.EOF
			break
		}
		if nextErr != nil {
			return fmt.Errorf("could not read solver flag archive: %w", nextErr)
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return fmt.Errorf("solver flag output is not a regular file")
		}
		if header.Size < 0 || header.Size > maxFlagBytes {
			return fmt.Errorf("solver flag output exceeds %d bytes", maxFlagBytes)
		}
		flag, err := ioutil.ReadAll(io.LimitReader(fTar, maxFlagBytes+1))
		if err != nil {
			m.log.errorf("could not read flag file: %s", err)
			return err
		}
		if int64(len(flag)) > maxFlagBytes {
			return fmt.Errorf("solver flag output exceeds %d bytes", maxFlagBytes)
		}

		flagStr := strings.TrimSpace(string(flag))
		if flagStr == bMeta.Flag {
			iMeta.LastSolved = time.Now().Unix()
			return m.recordSolve(iMeta)
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
	maxContextBytes := m.policy.MaxBuildContextBytes + m.policy.MaxArtifactBytes
	if maxContextBytes == 0 {
		maxContextBytes = 7 * 1024 * 1024 * 1024
	}
	ctx := tar.NewWriter(&boundedWriter{
		writer:    w,
		remaining: maxContextBytes,
		limit:     maxContextBytes,
	})

	customDocker := false

	go func() {
		cMeta, err := m.lookupChallengeMetadata(meta.Challenge)
		if err != nil {
			_ = w.CloseWithError(err)
			return
		}

		// Copy in contents of the "solver" directory
		solveDir := filepath.Join(filepath.Dir(cMeta.Path), "solver")
		root, err := os.OpenRoot(solveDir)
		if err != nil {
			_ = w.CloseWithError(err)
			return
		}
		defer root.Close()
		entryCount := 0
		maxEntries := m.policy.MaxBuildContextFiles
		if maxEntries == 0 {
			maxEntries = 10_000
		}
		err = filepath.WalkDir(solveDir, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			if path == solveDir {
				return nil
			}
			entryCount++
			if entryCount > maxEntries {
				return fmt.Errorf("solver context contains more than %d entries", maxEntries)
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			archivePath, err := filepath.Rel(solveDir, path)
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("solver context cannot contain symlink %q", archivePath)
			}
			if !info.IsDir() && !info.Mode().IsRegular() {
				return fmt.Errorf(
					"solver context contains unsupported file type %q (%s)",
					archivePath,
					info.Mode(),
				)
			}

			if path == filepath.Join(solveDir, "Dockerfile") {
				customDocker = true
			}

			hdr, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return err
			}

			hdr.Name = strings.ReplaceAll(archivePath, `\`, `/`)
			hdr.Linkname = ""

			err = ctx.WriteHeader(hdr)
			if err != nil {
				return err
			}

			if info.IsDir() {
				return nil
			}

			fd, err := root.Open(archivePath)
			if err != nil {
				return err
			}
			currentInfo, err := fd.Stat()
			if err != nil {
				_ = fd.Close()
				return err
			}
			if !currentInfo.Mode().IsRegular() ||
				currentInfo.Size() != info.Size() ||
				currentInfo.Mode() != info.Mode() {
				_ = fd.Close()
				return fmt.Errorf("solver file changed while archiving: %q", archivePath)
			}
			_, err = io.CopyN(ctx, fd, currentInfo.Size())
			closeErr := fd.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}

			return nil
		})

		if err != nil {
			w.CloseWithError(err)
			return
		}

		if !customDocker {
			// Insert the default
			hdr := tar.Header{Name: "Dockerfile", Mode: 0644, Size: int64(len(m.GetDockerfile("solver")))}
			err = ctx.WriteHeader(&hdr)
			if err != nil {
				w.CloseWithError(err)
				return
			}

			_, err = ctx.Write(m.GetDockerfile("solver"))
			if err != nil {
				w.CloseWithError(err)
				return
			}
		}

		if meta.HasArtifacts {
			artifactsPath := filepath.Join(m.artifactsDir, meta.getArtifactsFilename())
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
