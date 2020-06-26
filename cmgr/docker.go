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
	"strconv"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
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

	buildCtxFile, err := m.createBuildContext(cMeta, m.getDockerfile(cMeta.ChallengeType))
	if err != nil {
		m.log.errorf("failed to create build context: %s", err)
		return nil, err
	}
	defer os.Remove(buildCtxFile)

	builds := make([]*BuildMetadata, 0, len(seeds))
	for _, seed := range seeds {
		buildCtx, err := os.Open(buildCtxFile)
		if err != nil {
			m.log.errorf("failed to seek to beginning of file for %d: %s", seed, err)
			return nil, err
		}

		build, err := m.generateBuild(cMeta, buildCtx, seed, format)
		if err != nil {
			return nil, err
		}
		builds = append(builds, build)
	}

	return m.saveBuildMetadata(builds)
}

func (m *Manager) generateBuild(cMeta *ChallengeMetadata, buildCtx io.Reader, seed int, format string) (*BuildMetadata, error) {
	seedStr := fmt.Sprintf("%x", seed)

	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%s", cMeta.Id, format, seedStr)))
	sumStr := fmt.Sprintf("%x", sum)

	image := Image{DockerId: sumStr, Ports: []string{}}
	for _, port := range cMeta.PortMap {
		image.Ports = append(image.Ports, fmt.Sprintf("%d/tcp", port))
	}
	images := []Image{image} // TODO(jrolli): Figure out how this expands to multi-image challenges

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
	m.log.debugf("creating image %s", imageName)
	resp, err := m.cli.ImageBuild(m.ctx, buildCtx, opts)
	if err != nil {
		m.log.errorf("failed to build base image: %s", err)
		return nil, err
	}

	_, err = ioutil.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		m.log.errorf("failed to read build response from docker: %s", err)
		return nil, err
	}

	cConfig := container.Config{Image: imageName}
	hConfig := container.HostConfig{}
	nConfig := network.NetworkingConfig{}

	respCC, err := m.cli.ContainerCreate(m.ctx, &cConfig, &hConfig, &nConfig, "")
	if err != nil {
		m.log.errorf("failed to create artifacts container: %s", err)
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
			if err != nil {
				m.log.errorf("could not decode build metadata JSON file: %s", err)
				return nil, err
			}

			var ok bool
			flag, ok = lookups["flag"]
			if !ok {
				err = errors.New("build metadata missing the flag")
				m.log.error(err)
				return nil, err
			}

			delete(lookups, "flag")
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

	bMeta := BuildMetadata{
		Flag:         flag,
		Seed:         seed,
		Format:       format,
		LookupData:   lookups,
		Images:       images,
		HasArtifacts: len(files) > 0,
		Challenge:    cMeta.Id,
	}

	m.log.debugf("%v", bMeta)

	return &bMeta, nil
}

func (m *Manager) startContainers(build BuildId) (InstanceId, error) {
	// Get build metadata
	bMeta, err := m.lookupBuildMetadata(build)
	if err != nil {
		return 0, err
	}

	iMeta := InstanceMetadata{
		Build:      bMeta.Id,
		Ports:      make(map[string]int),
		Containers: []string{},
	}

	revPortMap, err := m.getReversePortMap(bMeta.Challenge)
	if err != nil {
		return 0, err
	}

	// Create a bridge just for this container
	netSpec := types.NetworkCreate{
		Driver: "bridge",
	}
	respNC, err := m.cli.NetworkCreate(m.ctx, "cmgr-internal", netSpec)
	if err != nil {
		m.log.errorf("could not create challenge network: %s", err)
		return 0, err
	}
	iMeta.Network = respNC.ID

	// Call create in docker
	for _, image := range bMeta.Images {
		exposedPorts := nat.PortSet{}
		publishedPorts := nat.PortMap{}
		for _, portStr := range image.Ports {
			port := nat.Port(portStr)
			exposedPorts[port] = struct{}{}
			publishedPorts[port] = []nat.PortBinding{{HostIP: "0.0.0.0"}}
		}

		cConfig := container.Config{
			Image:        fmt.Sprintf("%s:%s", bMeta.Challenge, image.DockerId),
			Hostname:     "challenge",
			ExposedPorts: exposedPorts,
		}

		hConfig := container.HostConfig{
			PortBindings: publishedPorts,
		}

		nConfig := network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				"cmgr-internal": {
					NetworkID: iMeta.Network,
					Aliases:   []string{"challenge"},
				},
			},
		}

		respCC, err := m.cli.ContainerCreate(m.ctx, &cConfig, &hConfig, &nConfig, "")
		if err != nil {
			m.log.errorf("failed to create instance container: %s", err)
			return 0, err
		}

		cid := respCC.ID
		iMeta.Containers = append(iMeta.Containers, cid)
		m.log.infof("created new image: %s", cid)

		err = m.cli.ContainerStart(m.ctx, cid, types.ContainerStartOptions{})
		if err != nil {
			m.log.errorf("failed to start container: %s", err)
			return 0, err
		}

		cInfo, err := m.cli.ContainerInspect(m.ctx, cid)
		if err != nil {
			m.log.errorf("failed to get container info: %s", err)
			return 0, err
		}

		for cPort, hPortInfo := range cInfo.NetworkSettings.Ports {
			if len(hPortInfo) == 0 {
				continue
			}

			hPort, err := strconv.Atoi(string(hPortInfo[0].HostPort))
			if err != nil {
				return 0, err
			}
			iMeta.Ports[revPortMap[string(cPort)]] = hPort
			m.log.debugf("container port %s mapped to %s", cPort, hPortInfo[0].HostPort)
		}
	}

	// Store instance metadata
	return m.saveInstanceMetadata(&iMeta)
}

func (m *Manager) stopContainers(instance InstanceId) error {
	m.log.debugf("stopping instance %d", instance)
	iMeta, err := m.lookupInstanceMetadata(instance)
	if err != nil {
		return err
	}

	for _, cid := range iMeta.Containers {
		err = m.cli.ContainerKill(m.ctx, cid, "SIGKILL")
		if err != nil {
			m.log.errorf("failed to kill container: %s", err)
			return err
		}

		opts := types.ContainerRemoveOptions{RemoveVolumes: true, Force: true}
		err = m.cli.ContainerRemove(m.ctx, cid, opts)
		if err != nil {
			m.log.errorf("failed to remove container: %s", err)
			return err
		}
	}

	err = m.cli.NetworkRemove(m.ctx, iMeta.Network)
	if err != nil {
		m.log.errorf("failed to remove network: %s", err)
		return err
	}

	return m.removeInstanceMetadata(instance)
}

func (m *Manager) destroyImages(build BuildId) error {
	m.log.debugf("destroying build %d", build)
	bMeta, err := m.lookupBuildMetadata(build)
	if err != nil {
		return err
	}

	err = m.removeBuildMetadata(build)
	if err != nil {
		return err
	}

	iro := types.ImageRemoveOptions{Force: false, PruneChildren: true}
	for _, image := range bMeta.Images {
		if bMeta.HasArtifacts {
			artifactsFileName := fmt.Sprintf("%s.tar.gz", image.DockerId)
			err := os.Remove(filepath.Join(m.artifactsDir, artifactsFileName))
			if err != nil {
				m.log.errorf("failed to remove artifacts file: %s", err)
				return err
			}
		}

		imageName := fmt.Sprintf("%s:%s", bMeta.Challenge, image.DockerId)
		_, err := m.cli.ImageRemove(m.ctx, imageName, iro)
		if err != nil {
			m.log.errorf("failed to remove image: %s", err)
			return err
		}
	}

	return nil
}
