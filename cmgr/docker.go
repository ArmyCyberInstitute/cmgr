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
	"regexp"
	"strconv"
	_ "embed"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
)

//go:embed dockerfiles/seccomp.json
var seccompPolicy string

func (m *Manager) initDocker() error {
	cli, err := client.NewClientWithOpts(client.FromEnv)
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

	chalInterface, isSet := os.LookupEnv(IFACE_ENV)
	if !isSet {
		chalInterface = "0.0.0.0"
	}
	m.challengeInterface = chalInterface

	return nil
}

func (b *BuildMetadata) makeFlag() *string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%d", b.Challenge, b.Format, b.Seed)))
	sumStr := fmt.Sprintf("%x", sum)

	flag := new(string)
	if len(sumStr) > 8 {
		sumStr = sumStr[:8]
	}
	*flag = fmt.Sprintf(b.Format, sumStr)
	return flag
}

func (b *BuildMetadata) getArtifactsFilename() string {
	return fmt.Sprintf("%d.tar.gz", b.Id)
}

func (i *InstanceMetadata) getNetworkName() string {
	return fmt.Sprintf("cmgr-%d", i.Id)
}

func (m *Manager) generateBuilds(builds []*BuildMetadata) error {
	if len(builds) == 0 {
		return nil
	}

	buildsComplete := true
	for _, build := range builds {
		buildsComplete = buildsComplete && (build.Flag != "")
	}
	if buildsComplete {
		return nil
	}

	cMeta, err := m.lookupChallengeMetadata(builds[0].Challenge)
	if err != nil {
		return err
	}

	updates := m.DetectChanges(filepath.Dir(cMeta.Path))
	if len(updates.Errors) > 0 {
		err = fmt.Errorf("errors detected in directory for '%s' run 'update'", cMeta.Id)
		m.log.error(err)
		return err
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
		return err
	}

	buildCtxFile, err := m.createBuildContext(cMeta, m.getDockerfile(cMeta.ChallengeType))
	if err != nil {
		m.log.errorf("failed to create build context: %s", err)
		return err
	}
	defer os.Remove(buildCtxFile)

	for _, build := range builds {
		if build.Flag != "" {
			continue
		}

		err = m.openBuild(build)
		if err != nil {
			return err
		}

		err = m.executeBuild(cMeta, build, buildCtxFile)
		if err != nil {
			m.removeBuildMetadata(build.Id)
			return err
		}

		err = m.finalizeBuild(build)
		if err != nil {
			return err
		}
	}

	return nil
}

type dockerError struct {
	Error string `json:"error"`
}

func (bMeta *BuildMetadata) dockerId(image Image) string {
	return fmt.Sprintf("%d-%s", bMeta.Id, image.Host)
}

func (m *Manager) executeBuild(cMeta *ChallengeMetadata, bMeta *BuildMetadata, buildCtxFile string) error {

	seedStr := fmt.Sprintf("%d", bMeta.Seed)

	images := []Image{}
	var buildImage string
	for _, host := range cMeta.Hosts {
		image := Image{Host: host.Name, Ports: []string{}}
		imageName := fmt.Sprintf("%s:%s", cMeta.Id, bMeta.dockerId(image))

		if host.Name == "builder" || (host.Name == "challenge" && buildImage == "") {
			buildImage = imageName
		}

		for _, portInfo := range cMeta.PortMap {
			if portInfo.Host == image.Host {
				image.Ports = append(image.Ports, fmt.Sprintf("%d/tcp", portInfo.Port))
			}
		}

		// Setup build options
		opts := types.ImageBuildOptions{
			BuildArgs: map[string]*string{
				"FLAG_FORMAT": &bMeta.Format,
				"SEED":        &seedStr,
				"FLAG":        bMeta.makeFlag(),
			},
			Remove: true,
			Tags:   []string{imageName},
			Target: host.Target,
		}

		// Call build
		buildCtx, err := os.Open(buildCtxFile)
		if err != nil {
			m.log.errorf("failed to seek to beginning of file for %s/%d: %s", cMeta.Id, bMeta.Id, err)
			return err
		}
		defer buildCtx.Close()

		m.log.debugf("creating image %s", imageName)
		resp, err := m.cli.ImageBuild(m.ctx, buildCtx, opts)
		if err != nil {
			m.log.errorf("failed to build base image: %s", err)
			return err
		}

		messages, err := ioutil.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			m.log.errorf("failed to read build response from docker: %s", err)
			return err
		}

		re := regexp.MustCompile(`{"errorDetail":[^\n]+`)
		errMsg := re.Find(messages)
		if errMsg != nil {
			var dMsg dockerError
			err = json.Unmarshal(errMsg, &dMsg)
			if err == nil {
				errMsg = []byte(dMsg.Error)
			}
			err = fmt.Errorf("failed to build image: %s", errMsg)
			m.log.error(err)
			return err
		}
		images = append(images, image)
	}

	if buildImage == "" {
		err := fmt.Errorf("aborting because no build image identified %s/%d", cMeta.Id, bMeta.Id)
		m.log.error(err)
		return err
	}

	cConfig := container.Config{Image: buildImage}
	hConfig := container.HostConfig{}
	nConfig := network.NetworkingConfig{}

	hostInfo, err := m.cli.Info(m.ctx)
	if err != nil {
		return err
	}

	if hostInfo.OSType == "linux" {
		m.log.debug("inserting custom seccomp profile")
		hConfig.SecurityOpt = []string{"seccomp:"+seccompPolicy}
	}

	respCC, err := m.cli.ContainerCreate(m.ctx, &cConfig, &hConfig, &nConfig, "")
	if err != nil {
		m.log.errorf("failed to create artifacts container: %s", err)
		return err
	}

	cid := respCC.ID
	crOpts := types.ContainerRemoveOptions{RemoveVolumes: true, RemoveLinks: false, Force: true}
	defer m.cli.ContainerRemove(m.ctx, cid, crOpts)

	m.log.infof("created container %s", cid)

	metaFile, _, err := m.cli.CopyFromContainer(m.ctx, cid, "/challenge")
	if err != nil {
		m.log.errorf("could not find '/challenge' in container: %s", err)
		return err
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
				return err
			}

			lookups = make(map[string]string)
			err = json.Unmarshal(data, &lookups)
			if err != nil {
				m.log.errorf("could not decode build metadata JSON file: %s", err)
				return err
			}

			var ok bool
			flag, ok = lookups["flag"]
			if !ok {
				err = errors.New("build metadata missing the flag")
				m.log.error(err)
				return err
			}

			delete(lookups, "flag")
		} else if hdr.Name == "challenge/artifacts.tar.gz" {
			artifactsFileName := bMeta.getArtifactsFilename()
			// Iterate through reading filenames and copying over the tarball
			artifactsFile, err := os.Create(filepath.Join(m.artifactsDir, artifactsFileName))
			if err != nil {
				m.log.errorf("could not create cached artifacts archive: %s", err)
				return err
			}
			defer artifactsFile.Close()

			srcGz, err := gzip.NewReader(cTar)
			if err != nil {
				m.log.errorf("could not gzip read artifacts file: %s", err)
				return err
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
					return err
				}

				if h.Typeflag != tar.TypeDir {
					_, err = io.Copy(dstTar, srcTar)
					if err != nil {
						m.log.errorf("could not write body to artifacts file: %s", err)
						return err
					}
				}
			}

			if err != io.EOF {
				m.log.errorf("error occurred during copy of artifacts: %s", err)
				return err
			}

			err = dstTar.Close()
			if err != nil {
				m.log.errorf("error closing artifacts tar file: %s", err)
				return err
			}

			err = srcGz.Close()
			if err != nil {
				m.log.errorf("error closing GZIP decoder: %s", err)
				return err
			}

			err = dstGz.Close()
			if err != nil {
				m.log.errorf("error closing GZIP encoder: %s", err)
				return err
			}

			err = artifactsFile.Close()
			if err != nil {
				m.log.errorf("error occurred when closing artifacts: %s", err)
				return err
			}
		}
	}

	if err != io.EOF {
		m.log.errorf("could not read metadata file: %s", err)
		return err
	}

	if flag == "" {
		err = errors.New("'flag' missing in metadata.json")
		m.log.error(err)
		return err
	}

	bMeta.Flag = flag
	bMeta.LookupData = lookups
	bMeta.Images = images
	bMeta.HasArtifacts = len(files) > 0

	err = m.validateBuild(cMeta, bMeta, files)
	if err != nil {
		os.Remove(bMeta.getArtifactsFilename())

		iro := types.ImageRemoveOptions{Force: false, PruneChildren: true}
		for _, image := range bMeta.Images {
			imageName := fmt.Sprintf("%s:%s", bMeta.Challenge, bMeta.dockerId(image))
			m.cli.ImageRemove(m.ctx, imageName, iro)
		}
	}

	m.log.debugf("%v", bMeta)

	return err
}

func (m *Manager) startNetwork(instance *InstanceMetadata) error {
	netSpec := types.NetworkCreate{
		Driver: "bridge",
	}
	netname := instance.getNetworkName()
	_, err := m.cli.NetworkCreate(m.ctx, netname, netSpec)
	if err != nil {
		m.log.errorf("could not create challenge network (%s): %s", netname, err)
	}
	return err
}

func (m *Manager) stopNetwork(instance *InstanceMetadata) error {
	err := m.cli.NetworkRemove(m.ctx, instance.getNetworkName())
	if err != nil {
		m.log.errorf("failed to remove network: %s", err)
	}
	return err
}

func (m *Manager) startContainers(build *BuildMetadata, instance *InstanceMetadata) error {

	revPortMap, err := m.getReversePortMap(build.Challenge)
	if err != nil {
		return err
	}

	// Call create in docker
	netname := instance.getNetworkName()
	for _, image := range build.Images {
		if image.Host == "builder" {
			continue
		}
		exposedPorts := nat.PortSet{}
		publishedPorts := nat.PortMap{}
		for _, portStr := range image.Ports {
			port := nat.Port(portStr)
			exposedPorts[port] = struct{}{}
			publishedPorts[port] = []nat.PortBinding{{HostIP: m.challengeInterface}}
		}

		cConfig := container.Config{
			Image:        fmt.Sprintf("%s:%s", build.Challenge, build.dockerId(image)),
			Hostname:     image.Host,
			ExposedPorts: exposedPorts,
		}

		hConfig := container.HostConfig{
			PortBindings:  publishedPorts,
			RestartPolicy: container.RestartPolicy{Name: "always"},
		}

		hostInfo, err := m.cli.Info(m.ctx)
		if err != nil {
			return err
		}

		if hostInfo.OSType == "linux" {
			m.log.debug("inserting custom seccomp profile")
			hConfig.SecurityOpt = []string{"seccomp:"+seccompPolicy}
		}

		nConfig := network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				netname: {
					NetworkID: netname,
					Aliases:   []string{image.Host},
				},
			},
		}

		respCC, err := m.cli.ContainerCreate(m.ctx, &cConfig, &hConfig, &nConfig, "")
		if err != nil {
			m.log.errorf("failed to create instance container: %s", err)
			return err
		}

		cid := respCC.ID
		instance.Containers = append(instance.Containers, cid)
		m.log.infof("created new image: %s", cid)

		err = m.cli.ContainerStart(m.ctx, cid, types.ContainerStartOptions{})
		if err != nil {
			m.log.errorf("failed to start container: %s", err)
			return err
		}

		cInfo, err := m.cli.ContainerInspect(m.ctx, cid)
		if err != nil {
			m.log.errorf("failed to get container info: %s", err)
			return err
		}

		for cPort, hPortInfo := range cInfo.NetworkSettings.Ports {
			if len(hPortInfo) == 0 {
				continue
			}

			hPort, err := strconv.Atoi(string(hPortInfo[0].HostPort))
			if err != nil {
				return err
			}
			instance.Ports[revPortMap[string(cPort)]] = hPort
			m.log.debugf("container port %s mapped to %s", cPort, hPortInfo[0].HostPort)
		}
	}

	return m.finalizeInstance(instance)
}

func (m *Manager) stopContainers(instance *InstanceMetadata) error {
	var err error
	for _, cid := range instance.Containers {
		opts := types.ContainerRemoveOptions{RemoveVolumes: true, Force: true}
		err = m.cli.ContainerRemove(m.ctx, cid, opts)
		if err != nil {
			m.log.errorf("failed to remove container: %s", err)
		}
	}

	mdErr := m.removeContainersMetadata(instance)
	if mdErr != nil {
		err = mdErr
	}

	return err
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

	if bMeta.HasArtifacts {
		err := os.Remove(filepath.Join(m.artifactsDir, bMeta.getArtifactsFilename()))
		if err != nil {
			m.log.errorf("failed to remove artifacts file: %s", err)
			return err
		}
	}

	iro := types.ImageRemoveOptions{Force: false, PruneChildren: true}
	for _, image := range bMeta.Images {

		imageName := fmt.Sprintf("%s:%s", bMeta.Challenge, bMeta.dockerId(image))
		_, err := m.cli.ImageRemove(m.ctx, imageName, iro)
		if err != nil {
			m.log.errorf("failed to remove image: %s", err)
			return err
		}
	}

	return nil
}
