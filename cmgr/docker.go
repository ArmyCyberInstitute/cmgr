package cmgr

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
)

func (m *Manager) initDocker() error {
	cli, err := client.NewEnvClient()
	if err != nil {
		m.log.errorf("could not create docker client: %s", err)
		return err
	}

	m.cli = cli
	m.ctx = context.Background()

	ping, err := cli.Ping(m.ctx)
	if err != nil {
		m.log.errorf("could not connect to docker engine: %s", err)
		return err
	}

	m.log.infof("connected to docker (API v%s)", ping.APIVersion)
	m.initDockerfiles()

	return nil
}

func makeFlag(format, rand string) *string {
	flag := new(string)
	if len(rand) > 8 {
		rand = rand[:8]
	}
	*flag = fmt.Sprintf(format, rand)
	return flag
}

func (m *Manager) buildImages(challenge ChallengeId, seeds []int, format string) ([]BuildId, error) {
	cMeta, err := m.lookupChallengeMetadata(challenge)
	if err != nil {
		return nil, err
	}

	updates := m.DetectChanges(filepath.Dir(cMeta.Path))
	if len(updates.Errors) > 0 {
		err = fmt.Errorf("errors detected in directory for '%s' run 'update'", cMeta.Id)
		m.log.error(err)
		return nil, err
	}

	modified := true
	for _, md := range updates.Unmodified {
		if md.Id == cMeta.Id {
			modified = false
			break
		}
	}
	if modified {
		err = fmt.Errorf("'%s' has changed since last update", cMeta.Id)
		m.log.error(err)
		return nil, err
	}

	buildCtx, err := m.createBuildContext(cMeta, m.getDockerfile(cMeta.ChallengeType))
	if err != nil {
		m.log.errorf("failed to create build context: %s", err)
		return nil, err
	}
	defer buildCtx.Close()

	builds := make([]BuildMetadata, 0, len(seeds))
	for _, seed := range seeds {
		seedStr := fmt.Sprintf("%x", seed)

		sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%s", cMeta.Id, format, seedStr)))
		sumStr := fmt.Sprintf("%x", sum)

		imageIds := []string{sumStr} // TODO(jrolli): Figure out how this expands to multi-image challenges

		// Setup build options
		imageName := fmt.Sprintf("%s:%x", cMeta.Id, sum)
		opts := types.ImageBuildOptions{BuildArgs: map[string]*string{
			"FLAG_FORMAT": &format,
			"SEED":        &seedStr,
			"FLAG":        makeFlag(format, sumStr),
		},
			Tags: []string{imageName},
		}

		// Call build
		resp, err := m.cli.ImageBuild(m.ctx, buildCtx, opts)
		if err != nil {
			m.log.errorf("failed to build base image: %s", err)
			return nil, err
		}

		_, err = ioutil.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			m.log.errorf("failed to read build response from docker: %d", err)
			return nil, err
		}

		cConfig := container.Config{Image: imageName}
		hConfig := container.HostConfig{}
		nConfig := network.NetworkingConfig{}

		respCC, err := m.cli.ContainerCreate(m.ctx, &cConfig, &hConfig, &nConfig, "")
		if err != nil {
			m.log.errorf("failed to create artifacts container: %d", err)
			return nil, err
		}

		cid := respCC.ID
		crOpts := types.ContainerRemoveOptions{RemoveVolumes: true, RemoveLinks: false, Force: true}
		defer m.cli.ContainerRemove(m.ctx, cid, crOpts)

		m.log.infof("created container %s", cid)

		metaFile, _, err := m.cli.CopyFromContainer(m.ctx, cid, "/challenge")
		if err != nil {
			m.log.errorf("could not find '/challenge' in container: %s", err)
			return nil, err
		}
		defer metaFile.Close()

		cTar := tar.NewReader(metaFile)
		var hdr *tar.Header
		var lookups map[string]string
		var files []string
		var flag string
		for hdr, err = cTar.Next(); err == nil; hdr, err = cTar.Next() {
			m.log.debugf("found in tar: %s", hdr.Name)
			if hdr.Name == "challenge/metadata.json" {
				data, err := ioutil.ReadAll(cTar)
				if err != nil {
					m.log.errorf("could not read metadata.json: %s", err)
					return nil, err
				}

				lookups = make(map[string]string)
				err = json.Unmarshal(data, &lookups)
				flag = lookups["flag"]
			} else if hdr.Name == "challenge/artifacts.tar.gz" {
				artifactsFileName := fmt.Sprintf("%s.tar.gz", sumStr)
				// Iterate through reading filenames and copying over the tarball
				artifactsFile, err := os.Create(filepath.Join(m.artifactsDir, artifactsFileName))
				if err != nil {
					m.log.errorf("could not create cached artifacts archive: %s", err)
					return nil, err
				}
				defer artifactsFile.Close()

				srcGz, err := gzip.NewReader(cTar)
				if err != nil {
					m.log.errorf("could not gzip read artifacts file: %s", err)
					return nil, err
				}

				dstGz := gzip.NewWriter(artifactsFile)
				srcTar := tar.NewReader(srcGz)
				dstTar := tar.NewWriter(dstGz)

				var h *tar.Header
				for h, err = srcTar.Next(); err == nil; h, err = srcTar.Next() {
					files = append(files, h.Name)
					m.log.debugf("artifact found: %s", h.Name)
					err = dstTar.WriteHeader(h)
					if err != nil {
						m.log.errorf("could not write header to artifacts file: %s", err)
						return nil, err
					}

					if h.Typeflag != tar.TypeDir {
						_, err = io.Copy(dstTar, srcTar)
						if err != nil {
							m.log.errorf("could not write body to artifacts file: %s", err)
							return nil, err
						}
					}
				}

				if err != io.EOF {
					m.log.errorf("error occurred during copy of artifacts: %s", err)
					return nil, err
				}

				err = dstTar.Close()
				if err != nil {
					m.log.errorf("error closing artifacts tar file: %s", err)
					return nil, err
				}

				err = srcGz.Close()
				if err != nil {
					m.log.errorf("error closing GZIP decoder: %s", err)
					return nil, err
				}

				err = dstGz.Close()
				if err != nil {
					m.log.errorf("error closing GZIP encoder: %s", err)
					return nil, err
				}

				err = artifactsFile.Close()
				if err != nil {
					m.log.errorf("error occurred when closing artifacts: %s", err)
					return nil, err
				}
			}
		}

		if err != io.EOF {
			m.log.errorf("could not read metadata file: %s", err)
			return nil, err
		}

		if flag == "" {
			err = errors.New("'flag' missing in metadata.json")
			m.log.error(err)
			return nil, err
		}

		builds = append(builds,
			BuildMetadata{
				Flag:         flag,
				Seed:         seed,
				LookupData:   lookups,
				ImageIds:     imageIds,
				HasArtifacts: len(files) > 0,
				ChallengeId:  cMeta.Id,
			})
	}

	return m.saveBuildMetadata(builds)
}
