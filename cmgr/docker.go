package cmgr

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	dockeropts "github.com/docker/cli/opts"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/strslice"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"github.com/docker/go-units"
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

	m.challengeRegistry, isSet = os.LookupEnv(REGISTRY_ENV)
	if isSet {
		authPayload := fmt.Sprintf(
			`{"username":"%s","password":"%s","serveraddress":"%s"}`,
			os.Getenv(REGISTRY_USER_ENV),
			os.Getenv(REGISTRY_TOKEN_ENV),
			strings.SplitN(m.challengeRegistry, "/", 2)[0],
		)
		m.authString = base64.StdEncoding.EncodeToString([]byte(authPayload))
	}

	m.portLow, m.portHigh, err = getPortRange()
	if err != nil {
		m.log.errorf("%s", err)
	}

	return err
}

func getPortRange() (int, int, error) {
	portRange := os.Getenv(PORTS_ENV)
	if portRange == "" {
		return 0, 0, nil
	}

	portStrs := strings.Split(portRange, "-")
	if len(portStrs) != 2 {
		return 0, 0, fmt.Errorf("malformed port range: '%s' does not contain '-' character", portRange)
	}

	var low int
	var high int
	var err error
	low, err = strconv.Atoi(portStrs[0])
	if err == nil {
		high, err = strconv.Atoi(portStrs[1])
	}

	if err != nil {
		return 0, 0, err
	}

	if low < 1024 || high > (1<<16) || high < low {
		err = fmt.Errorf("bad port range: %d-%d either contains invalid/privileged ports or includes 0 ports", low, high)
	}

	return low, high, err
}

// Returns a string to simplify integration with Docker's Go API.  Specifically,
// an empty string will tell it to use an ephemeral port while a non-empty string
// (even if it is "0") will tell it to attempt binding to that specific port.
func (m *Manager) getFreePort() (string, error) {
	if m.portLow == 0 {
		return "", nil
	}

	numPorts := m.portHigh - m.portLow + 1

	// Get currently used ports...
	ports, err := m.usedPortSet()
	if err != nil {
		return "", nil
	}

	// Pick a random starting point in the port range...
	port := rand.Intn(numPorts) + m.portLow

	// Sweep through ports looking for a free one...
	for i := 0; i < numPorts; i++ {
		if _, used := ports[port]; !used {
			return fmt.Sprint(port), nil
		}

		port = port + 1
		if port > m.portHigh {
			port = m.portLow
		}
	}

	return "", fmt.Errorf("All ports between %d and %d are in use", m.portLow, m.portHigh)
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

func challengeToFreezeName(challenge ChallengeId) string {
	return strings.ReplaceAll(string(challenge), "/", "_")
}

func (m *Manager) freezeBaseImage(challenge ChallengeId, force bool) error {
	cMeta, err := m.lookupChallengeMetadata(challenge)
	if err != nil {
		return err
	}

	imageName := fmt.Sprintf("%s/%s:%x", m.challengeRegistry, challengeToFreezeName(challenge), cMeta.SourceChecksum)

	if !force {
		// Do some check here to see if it already exists
	}

	buildCtxFile, err := m.createBuildContext(cMeta, m.getDockerfile(cMeta.ChallengeType))
	if err != nil {
		m.log.errorf("failed to create build context: %s", err)
		return err
	}
	defer os.Remove(buildCtxFile)
	buildCtx, err := os.Open(buildCtxFile)
	if err != nil {
		m.log.errorf("failed to seek to beginning of file for %s: %s", cMeta.Id, err)
		return err
	}
	defer buildCtx.Close()

	// Setup build options
	opts := types.ImageBuildOptions{
		Remove:     true,
		Tags:       []string{imageName},
		Target:     "base",
		NoCache:    force, // Require to use latest info on force
		PullParent: force, // Update parent image as well on force
	}

	// Build the image
	m.log.debugf("creating base image %s", imageName)
	resp, err := m.cli.ImageBuild(m.ctx, buildCtx, opts)
	if err != nil {
		m.log.errorf("failed to build base image: %s", err)
		return err
	}

	// Read the response because errors aren't propagated.
	messages, err := ioutil.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		m.log.errorf("failed to read build response from docker: %s", err)
		return err
	}

	// Search the response for an error message
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

	// Push the image
	pushOpts := types.ImagePushOptions{RegistryAuth: m.authString}
	pushResp, err := m.cli.ImagePush(m.ctx, imageName, pushOpts)
	if err != nil {
		m.log.errorf("failed to push base image: %s", err)
		return err
	}

	// Read the response because errors aren't propagated.
	messages, err = ioutil.ReadAll(pushResp)
	resp.Body.Close()
	if err != nil {
		m.log.errorf("failed to read push response from docker: %s", err)
		return err
	}

	// Search the response for an error message
	errMsg = re.Find(messages)
	if errMsg != nil {
		var dMsg dockerError
		err = json.Unmarshal(errMsg, &dMsg)
		if err == nil {
			errMsg = []byte(dMsg.Error)
		}
		err = fmt.Errorf("failed to push image: %s", errMsg)
		m.log.error(err)
		return err
	}

	return nil
}

func (m *Manager) executeBuild(cMeta *ChallengeMetadata, bMeta *BuildMetadata, buildCtxFile string) error {

	seedStr := fmt.Sprintf("%d", bMeta.Seed)

	baseName := fmt.Sprintf("%s/%s:%x", m.challengeRegistry, challengeToFreezeName(cMeta.Id), cMeta.SourceChecksum)
	pullOpts := types.ImagePullOptions{RegistryAuth: m.authString}
	var buildCache []string
	pullResp, err := m.cli.ImagePull(m.ctx, baseName, pullOpts)
	if err == nil {
		// Read the response because errors aren't propagated.
		messages, err := ioutil.ReadAll(pullResp)
		pullResp.Close()
		if err == nil {
			// Search the response for an error message
			re := regexp.MustCompile(`{"errorDetail":[^\n]+`)
			errMsg := re.Find(messages)
			if errMsg == nil {
				m.log.infof("Successfully pulled base image '%s'", baseName)
				buildCache = append(buildCache, baseName)
			}
		}
	}

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
			Remove:    true,
			CacheFrom: buildCache,
			Tags:      []string{imageName},
			Target:    host.Target,
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
		hConfig.SecurityOpt = []string{"seccomp:" + seccompPolicy}
	}

	respCC, err := m.cli.ContainerCreate(m.ctx, &cConfig, &hConfig, &nConfig, nil, "")
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

func (m *Manager) startNetwork(instance *InstanceMetadata, opts *NetworkOptions) error {
	netSpec := types.NetworkCreate{
		Driver:   "bridge",
		Internal: opts.Internal,
	}
	netname := instance.getNetworkName()
	_, err := m.cli.NetworkCreate(m.ctx, netname, netSpec)
	if err != nil {
		m.log.errorf("could not create challenge network (%s): %s", netname, err)
	}
	return err
}

func (m *Manager) stopNetwork(instance *InstanceMetadata) error {
	networkName := instance.getNetworkName()
	err := m.cli.NetworkRemove(m.ctx, networkName)
	if err != nil {
		if client.IsErrNotFound(err) {
			m.log.warnf("skipped removing network (not found): %s", networkName)
			err = nil
		} else {
			m.log.errorf("failed to remove network: %s", err)
		}
	}
	return err
}

// This approach is pretty heavy-handed and effectively serializes the creation
// of all challenges that expose ports.  If this becomes a performance issue,
// may need to look at fully managing ports in memory rather than a hybrid
// with the SQLite database.
var portLock sync.Mutex

func (m *Manager) startContainers(build *BuildMetadata, instance *InstanceMetadata, opts map[string]ContainerOptions) error {
	revPortMap, err := m.getReversePortMap(build.Challenge)
	if err != nil {
		return err
	}

	if len(revPortMap) != 0 {
		// No need to lock the port mapping if we are not mapping any ports...
		portLock.Lock()
		defer portLock.Unlock()
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
			hostPort, err := m.getFreePort()
			if err != nil {
				return err
			}
			exposedPorts[port] = struct{}{}
			publishedPorts[port] = []nat.PortBinding{
				{HostIP: m.challengeInterface, HostPort: hostPort},
			}
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
		if cOpts, ok := opts[image.Host]; ok {
			hConfig.Init = &cOpts.Init
			if cOpts.Cpus != nil {
				nanoCpus, err := dockeropts.ParseCPUs(*cOpts.Cpus)
				if err != nil {
					return err
				}
				hConfig.NanoCPUs = nanoCpus
			}
			if cOpts.Memory != nil {
				memoryBytes, err := units.RAMInBytes(*cOpts.Memory)
				if err != nil {
					return err
				}
				hConfig.Memory = memoryBytes
			}
			if len(cOpts.Ulimits) > 0 {
				limits := make([]*units.Ulimit, len(cOpts.Ulimits))
				for i, limitStr := range cOpts.Ulimits {
					limit, err := units.ParseUlimit(limitStr)
					if err != nil {
						return err
					}
					limits[i] = limit
				}
				hConfig.Ulimits = limits
			}
			if cOpts.PidsLimit != nil {
				hConfig.PidsLimit = cOpts.PidsLimit
			}
			hConfig.ReadonlyRootfs = cOpts.ReadonlyRootfs
			hConfig.CapDrop = (strslice.StrSlice)(cOpts.CapDrop)
		}

		hostInfo, err := m.cli.Info(m.ctx)
		if err != nil {
			return err
		}

		if hostInfo.OSType == "linux" {
			m.log.debug("inserting custom seccomp profile")
			hConfig.SecurityOpt = []string{"seccomp:" + seccompPolicy}
		}

		nConfig := network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				netname: {
					NetworkID: netname,
					Aliases:   []string{image.Host},
				},
			},
		}

		respCC, err := m.cli.ContainerCreate(m.ctx, &cConfig, &hConfig, &nConfig, nil, "")
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

		backoff := time.Millisecond
		done := false
		for !done && backoff < time.Second {
			m.log.debug("Querying docker for port info...")

			cInfo, err := m.cli.ContainerInspect(m.ctx, cid)
			if err != nil {
				m.log.errorf("failed to get container info: %s", err)
				return err
			}
			done = true

			for cPort, hPortInfo := range cInfo.NetworkSettings.Ports {
				if len(hPortInfo) == 0 {
					done = false
					time.Sleep(backoff)
					backoff = 2 * backoff
					break
				}

				hPort, err := strconv.Atoi(string(hPortInfo[0].HostPort))
				if err != nil {
					return err
				}
				instance.Ports[revPortMap[string(cPort)]] = hPort
				m.log.debugf("container port %s mapped to %s", cPort, hPortInfo[0].HostPort)
			}
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
			if client.IsErrNotFound(err) {
				m.log.warnf("skipped removing container (not found): %s", cid)
				err = nil
			} else {
				m.log.errorf("failed to remove container: %s", err)
			}
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
		artifactsFilename := bMeta.getArtifactsFilename()
		err := os.Remove(filepath.Join(m.artifactsDir, artifactsFilename))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				m.log.warnf("skipped removing artifacts file (not found): %s", artifactsFilename)
				err = nil
			} else {
				m.log.errorf("failed to remove artifacts file: %s", err)
				return err
			}
		}
	}

	iro := types.ImageRemoveOptions{Force: true, PruneChildren: true}
	for _, image := range bMeta.Images {

		imageName := fmt.Sprintf("%s:%s", bMeta.Challenge, bMeta.dockerId(image))
		_, err := m.cli.ImageRemove(m.ctx, imageName, iro)
		if err != nil {
			if client.IsErrNotFound(err) {
				m.log.warnf("skipped removing image (not found): %s", imageName)
			} else {
				m.log.errorf("failed to remove image: %s", err)
				return err
			}
		}
	}

	return nil
}
