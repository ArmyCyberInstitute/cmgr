package cmgr

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
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

	"github.com/ArmyCyberInstitute/cmgr/internal/ociinterceptor"
	dockeropts "github.com/docker/cli/opts"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/strslice"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"github.com/docker/go-units"
)

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

	hostInfo, infoErr := cli.Info(m.ctx)
	if infoErr != nil {
		m.log.warnf(
			"could not determine whether seccomp tweaks are available: %s",
			infoErr,
		)
	} else if warning := seccompRuntimeWarning(hostInfo); warning != "" {
		m.log.warn(warning)
	}

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

func (b *BuildMetadata) getArtifactsFilenameForQualifier(qualifier string) string {
	if qualifier == "" {
		return b.getArtifactsFilename()
	}
	return fmt.Sprintf(".%s-%s", qualifier, b.getArtifactsFilename())
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

	buildCtxFile, err := m.createBuildContext(cMeta, m.GetDockerfile(cMeta.ChallengeType))
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

		err = m.executeBuild(cMeta, build, buildCtxFile, "")
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

	buildCtxFile, err := m.createBuildContext(cMeta, m.GetDockerfile(cMeta.ChallengeType))
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

func buildImageName(
	challenge ChallengeId,
	build *BuildMetadata,
	image Image,
	qualifier string,
) string {
	tag := build.dockerId(image)
	if qualifier != "" {
		tag = qualifier + "-" + tag
	}
	return fmt.Sprintf("%s:%s", challenge, tag)
}

func cloneBuildMetadata(build *BuildMetadata) *BuildMetadata {
	cloned := *build
	cloned.Images = append([]Image(nil), build.Images...)
	for i := range cloned.Images {
		cloned.Images[i].Ports = append([]string(nil), build.Images[i].Ports...)
	}
	if build.LookupData != nil {
		cloned.LookupData = make(map[string]string, len(build.LookupData))
		for key, value := range build.LookupData {
			cloned.LookupData[key] = value
		}
	}
	return &cloned
}

type stagedBuildPromotion struct {
	build             *BuildMetadata
	qualifier         string
	canonicalImageIDs map[string]string
	stagedImageNames  []string
	canonicalArtifact string
	backupArtifact    string
	hadArtifact       bool
}

type stagedBuildUpdate struct {
	metadata  *ChallengeMetadata
	previous  *BuildMetadata
	candidate *BuildMetadata
	qualifier string
	promotion *stagedBuildPromotion
	cutovers  []*instanceCutover
}

func (m *Manager) rollbackBuildUpdate(update *stagedBuildUpdate) []error {
	var errs []error
	for i := len(update.cutovers) - 1; i >= 0; i-- {
		if err := m.rollbackInstanceCutover(update.cutovers[i]); err != nil {
			errs = append(errs, err)
		}
	}
	if update.promotion != nil {
		if err := m.rollbackStagedBuild(update.promotion); err != nil {
			errs = append(errs, err)
		}
	}
	if update.previous != nil {
		if err := m.finalizeBuild(update.previous); err != nil {
			errs = append(errs, err)
		} else if _, err := m.db.Exec(
			"UPDATE builds SET lastsolved=? WHERE id=?;",
			update.previous.LastSolved,
			update.previous.Id,
		); err != nil {
			errs = append(errs, err)
		}
	}
	m.discardStagedBuild(update.metadata, update.candidate, update.qualifier)
	return errs
}

func (m *Manager) finishBuildUpdate(update *stagedBuildUpdate) []error {
	var errs []error
	for _, cutover := range update.cutovers {
		if err := m.finishInstanceCutover(cutover); err != nil {
			errs = append(errs, err)
		}
	}
	if err := m.finishStagedBuild(update.promotion); err != nil {
		errs = append(errs, err)
	}
	candidateHosts := make(map[string]struct{}, len(update.candidate.Images))
	for _, image := range update.candidate.Images {
		candidateHosts[image.Host] = struct{}{}
	}
	removeOptions := types.ImageRemoveOptions{PruneChildren: true}
	for _, image := range update.previous.Images {
		if _, retained := candidateHosts[image.Host]; retained {
			continue
		}
		imageName := buildImageName(
			update.previous.Challenge,
			update.previous,
			image,
			"",
		)
		if _, err := m.cli.ImageRemove(
			m.ctx,
			imageName,
			removeOptions,
		); err != nil && !client.IsErrNotFound(err) {
			errs = append(
				errs,
				fmt.Errorf("could not remove obsolete image %s: %v", imageName, err),
			)
		}
	}
	return errs
}

func (m *Manager) promoteStagedBuild(
	build *BuildMetadata,
	qualifier string,
) (*stagedBuildPromotion, error) {
	promotion := &stagedBuildPromotion{
		build:             build,
		qualifier:         qualifier,
		canonicalImageIDs: make(map[string]string),
	}
	for _, image := range build.Images {
		canonical := buildImageName(build.Challenge, build, image, "")
		staged := buildImageName(build.Challenge, build, image, qualifier)
		inspection, _, err := m.cli.ImageInspectWithRaw(m.ctx, canonical)
		if err == nil {
			promotion.canonicalImageIDs[canonical] = inspection.ID
		} else if client.IsErrNotFound(err) {
			// A new challenge host has no canonical tag to preserve.
			promotion.canonicalImageIDs[canonical] = ""
		} else {
			return nil, fmt.Errorf("could not snapshot image %s before promotion: %v", canonical, err)
		}
		promotion.stagedImageNames = append(promotion.stagedImageNames, staged)
	}

	for _, image := range build.Images {
		canonical := buildImageName(build.Challenge, build, image, "")
		staged := buildImageName(build.Challenge, build, image, qualifier)
		if err := m.cli.ImageTag(m.ctx, staged, canonical); err != nil {
			_ = m.rollbackStagedBuild(promotion)
			return nil, fmt.Errorf("could not promote image %s to %s: %v", staged, canonical, err)
		}
	}

	promotion.canonicalArtifact = filepath.Join(
		m.artifactsDir,
		build.getArtifactsFilename(),
	)
	promotion.backupArtifact = filepath.Join(
		m.artifactsDir,
		fmt.Sprintf(".cmgr-old-%s-%s", qualifier, build.getArtifactsFilename()),
	)
	stagedArtifact := filepath.Join(
		m.artifactsDir,
		build.getArtifactsFilenameForQualifier(qualifier),
	)
	if _, err := os.Stat(promotion.canonicalArtifact); err == nil {
		if err = os.Rename(promotion.canonicalArtifact, promotion.backupArtifact); err != nil {
			_ = m.rollbackStagedBuild(promotion)
			return nil, fmt.Errorf("could not preserve current artifacts: %v", err)
		}
		promotion.hadArtifact = true
	} else if !os.IsNotExist(err) {
		_ = m.rollbackStagedBuild(promotion)
		return nil, fmt.Errorf("could not inspect current artifacts: %v", err)
	}

	if build.HasArtifacts {
		if err := os.Rename(stagedArtifact, promotion.canonicalArtifact); err != nil {
			_ = m.rollbackStagedBuild(promotion)
			return nil, fmt.Errorf("could not promote staged artifacts: %v", err)
		}
	}
	return promotion, nil
}

func (m *Manager) rollbackStagedBuild(promotion *stagedBuildPromotion) error {
	var firstErr error
	for canonical, imageID := range promotion.canonicalImageIDs {
		if imageID == "" {
			removeOptions := types.ImageRemoveOptions{PruneChildren: true}
			if _, err := m.cli.ImageRemove(
				m.ctx,
				canonical,
				removeOptions,
			); err != nil && !client.IsErrNotFound(err) && firstErr == nil {
				firstErr = err
			}
		} else if err := m.cli.ImageTag(
			m.ctx,
			imageID,
			canonical,
		); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if promotion.canonicalArtifact != "" {
		if err := os.Remove(promotion.canonicalArtifact); err != nil &&
			!os.IsNotExist(err) && firstErr == nil {
			firstErr = err
		}
	}
	if promotion.hadArtifact {
		if err := os.Rename(
			promotion.backupArtifact,
			promotion.canonicalArtifact,
		); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (m *Manager) finishStagedBuild(promotion *stagedBuildPromotion) error {
	var firstErr error
	removeOptions := types.ImageRemoveOptions{PruneChildren: false}
	for _, imageName := range promotion.stagedImageNames {
		if _, err := m.cli.ImageRemove(m.ctx, imageName, removeOptions); err != nil &&
			!client.IsErrNotFound(err) && firstErr == nil {
			firstErr = err
		}
	}
	if promotion.hadArtifact {
		if err := os.Remove(promotion.backupArtifact); err != nil &&
			!os.IsNotExist(err) && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (m *Manager) discardStagedBuild(
	metadata *ChallengeMetadata,
	build *BuildMetadata,
	qualifier string,
) {
	removeOptions := types.ImageRemoveOptions{PruneChildren: true}
	for _, host := range metadata.Hosts {
		image := Image{Host: host.Name}
		imageName := buildImageName(metadata.Id, build, image, qualifier)
		_, _ = m.cli.ImageRemove(m.ctx, imageName, removeOptions)
	}
	_ = os.Remove(filepath.Join(
		m.artifactsDir,
		build.getArtifactsFilenameForQualifier(qualifier),
	))
}

func (m *Manager) executeBuild(
	cMeta *ChallengeMetadata,
	bMeta *BuildMetadata,
	buildCtxFile string,
	qualifier string,
) error {

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
		imageName := buildImageName(cMeta.Id, bMeta, image, qualifier)

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
			artifactsFileName := bMeta.getArtifactsFilenameForQualifier(qualifier)
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
		os.Remove(filepath.Join(
			m.artifactsDir,
			bMeta.getArtifactsFilenameForQualifier(qualifier),
		))

		iro := types.ImageRemoveOptions{Force: false, PruneChildren: true}
		for _, image := range bMeta.Images {
			imageName := buildImageName(bMeta.Challenge, bMeta, image, qualifier)
			m.cli.ImageRemove(m.ctx, imageName, iro)
		}
	}

	m.log.debugf("%v", bMeta)

	return err
}

func (m *Manager) startNetwork(instance *InstanceMetadata, opts NetworkOptions) error {
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

func (m *Manager) startContainers(
	build *BuildMetadata,
	instance *InstanceMetadata,
	opts map[string]ContainerOptions,
) error {
	return m.startContainersWithPersistence(build, instance, opts, true, nil)
}

func (m *Manager) startContainersWithPersistence(
	build *BuildMetadata,
	instance *InstanceMetadata,
	opts map[string]ContainerOptions,
	persist bool,
	preferredPorts map[string]int,
) error {
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
			endpoint := challengePortEndpoint{
				Host: image.Host,
				Port: portStr,
			}
			portName, ok := revPortMap[endpoint]
			if !ok {
				return fmt.Errorf(
					"could not find the challenge port name for %s on host %s",
					portStr,
					image.Host,
				)
			}
			hostPort, err := m.selectHostPort(portName, preferredPorts)
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

		cOpts, hasContainerOpts := effectiveContainerOptions(
			opts,
			strings.ToLower(image.Host),
		)
		if image.Host == "builder" {
			hasContainerOpts = false
		}
		if hasContainerOpts {
			hConfig.Init = &cOpts.Init
			if cOpts.Cpus != "" {
				nanoCpus, err := dockeropts.ParseCPUs(cOpts.Cpus)
				if err != nil {
					return err
				}
				hConfig.NanoCPUs = nanoCpus
			}
			if cOpts.Memory != "" {
				memoryBytes, err := units.RAMInBytes(cOpts.Memory)
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
			if cOpts.PidsLimit != 0 {
				hConfig.PidsLimit = &cOpts.PidsLimit
			}
			hConfig.ReadonlyRootfs = cOpts.ReadonlyRootfs
			hConfig.CapDrop = (strslice.StrSlice)(cOpts.DroppedCaps)
			if cOpts.NoNewPrivileges {
				hConfig.SecurityOpt = append(hConfig.SecurityOpt, "no-new-privileges:true")
			}
			if cOpts.DiskQuota != "" {
				_, quotas_enabled := os.LookupEnv(DISK_QUOTA_ENV)
				if quotas_enabled {
					var storageOpt = map[string]string{
						"size": cOpts.DiskQuota,
					}
					hConfig.StorageOpt = storageOpt
				} else {
					m.log.warnf("disk quota for %s container '%s' ignored (disk quotas are not enabled)", build.Challenge, image.Host)
				}
			}
			if cOpts.CgroupParent != "" {
				hConfig.CgroupParent = cOpts.CgroupParent
			}
		}

		if cOpts.Seccomp != nil &&
			(len(cOpts.Seccomp.Tweaks) != 0 || cOpts.Seccomp.effectiveProfile != "") {
			hostInfo, err := m.cli.Info(m.ctx)
			if err != nil {
				return err
			}
			if len(cOpts.Seccomp.Tweaks) != 0 {
				m.log.debugf("requesting OCI seccomp tweaks: %s", strings.Join(cOpts.Seccomp.Tweaks, ","))
			} else {
				m.log.debugf("inserting custom seccomp profile (%s)", cOpts.Seccomp.ProfileHash)
			}
			if err = configureContainerSeccomp(&cConfig, &hConfig, cOpts.Seccomp, hostInfo); err != nil {
				return err
			}
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
				endpoint := challengePortEndpoint{
					Host: image.Host,
					Port: string(cPort),
				}
				portName, ok := revPortMap[endpoint]
				if !ok {
					return fmt.Errorf(
						"could not find the challenge port name for %s on host %s",
						cPort,
						image.Host,
					)
				}
				instance.Ports[portName] = hPort
				m.log.debugf("container port %s mapped to %s", cPort, hPortInfo[0].HostPort)
			}
		}
		if !done {
			return fmt.Errorf(
				"timed out waiting for Docker port assignments for container %s",
				cid,
			)
		}
	}

	if persist {
		return m.finalizeInstance(instance)
	}
	return nil
}

func (m *Manager) selectHostPort(
	portName string,
	preferredPorts map[string]int,
) (string, error) {
	if preferredPort, reuse := preferredPorts[portName]; reuse {
		if preferredPort <= 0 || preferredPort >= 65536 {
			return "", fmt.Errorf(
				"cannot preserve invalid host port %d for %q",
				preferredPort,
				portName,
			)
		}
		return strconv.Itoa(preferredPort), nil
	}
	return m.getFreePort()
}

func effectiveContainerOptions(
	options map[string]ContainerOptions,
	host string,
) (ContainerOptions, bool) {
	defaultOptions, hasDefault := options[""]
	hostOptions, hasHost := options[host]
	if !hasHost {
		return defaultOptions, hasDefault
	}

	// Host options retain the existing replacement semantics for general
	// container settings. Seccomp is inherited separately so a challenge-level
	// policy applies to every runtime container unless that host explicitly
	// selects its own policy.
	if hostOptions.Seccomp == nil {
		hostOptions.Seccomp = defaultOptions.Seccomp
	}
	return hostOptions, true
}

type instanceCutover struct {
	old       *InstanceMetadata
	candidate *InstanceMetadata
}

func (m *Manager) prepareInstanceCutover(
	build *BuildMetadata,
	current *InstanceMetadata,
	options map[string]ContainerOptions,
) (*instanceCutover, error) {
	oldContainerIDs := append([]string(nil), current.Containers...)
	pausedContainerIDs, err := m.pauseContainerProcesses(oldContainerIDs)
	if err != nil {
		if resumeErr := m.resumeContainerProcesses(pausedContainerIDs); resumeErr != nil {
			return nil, fmt.Errorf(
				"could not pause old containers: %v; partially paused containers could not be resumed: %v",
				err,
				resumeErr,
			)
		}
		return nil, err
	}

	old := *current
	old.Containers = append([]string(nil), current.Containers...)
	old.Ports = make(map[string]int, len(current.Ports))
	for name, port := range current.Ports {
		old.Ports[name] = port
	}
	candidate := &InstanceMetadata{
		Id:         current.Id,
		Build:      current.Build,
		LastSolved: current.LastSolved,
		Ports:      make(map[string]int),
		Containers: []string{},
	}
	if err := m.startContainersWithPersistence(
		build,
		candidate,
		options,
		false,
		current.Ports,
	); err != nil {
		var recoveryProblems []string
		if cleanupErr := m.removeContainerIDs(candidate.Containers); cleanupErr != nil {
			recoveryProblems = append(
				recoveryProblems,
				fmt.Sprintf("remove replacement containers: %v", cleanupErr),
			)
		}
		restartErr := m.resumeContainerProcesses(oldContainerIDs)
		if restartErr != nil {
			recoveryProblems = append(
				recoveryProblems,
				fmt.Sprintf("restart old containers: %v", restartErr),
			)
		}
		if len(recoveryProblems) != 0 {
			return nil, fmt.Errorf(
				"replacement containers failed: %v; recovery also failed: %s",
				err,
				strings.Join(recoveryProblems, "; "),
			)
		}
		return nil, err
	}

	if err := m.replaceInstanceRuntimeMetadata(candidate); err != nil {
		var recoveryProblems []string
		if cleanupErr := m.removeContainerIDs(candidate.Containers); cleanupErr != nil {
			recoveryProblems = append(
				recoveryProblems,
				fmt.Sprintf("remove replacement containers: %v", cleanupErr),
			)
		}
		restartErr := m.resumeContainerProcesses(oldContainerIDs)
		if restartErr != nil {
			recoveryProblems = append(
				recoveryProblems,
				fmt.Sprintf("restart old containers: %v", restartErr),
			)
		}
		if len(recoveryProblems) != 0 {
			return nil, fmt.Errorf(
				"could not record replacement containers: %v; recovery also failed: %s",
				err,
				strings.Join(recoveryProblems, "; "),
			)
		}
		return nil, err
	}

	return &instanceCutover{old: &old, candidate: candidate}, nil
}

func (m *Manager) rollbackInstanceCutover(cutover *instanceCutover) error {
	var problems []string
	if err := m.removeContainerIDs(cutover.candidate.Containers); err != nil {
		problems = append(problems, fmt.Sprintf("remove replacement containers: %v", err))
	}
	if err := m.replaceInstanceRuntimeMetadata(cutover.old); err != nil {
		problems = append(problems, fmt.Sprintf("restore instance metadata: %v", err))
	}
	if err := m.resumeContainerProcesses(cutover.old.Containers); err != nil {
		problems = append(problems, fmt.Sprintf("restart old containers: %v", err))
	}
	if len(problems) != 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func (m *Manager) finishInstanceCutover(cutover *instanceCutover) error {
	return m.removeContainerIDs(cutover.old.Containers)
}

// pauseContainerProcesses stops containers without removing them or their
// database rows. Staged challenge updates retain them for rollback.
func (m *Manager) pauseContainerProcesses(
	containerIDs []string,
) ([]string, error) {
	timeout := 10 * time.Second
	paused := make([]string, 0, len(containerIDs))
	for _, containerID := range containerIDs {
		if err := m.cli.ContainerStop(m.ctx, containerID, &timeout); err != nil {
			return paused, fmt.Errorf("could not stop container %s: %v", containerID, err)
		}
		paused = append(paused, containerID)
	}
	return paused, nil
}

func (m *Manager) resumeContainerProcesses(containerIDs []string) error {
	var problems []string
	for _, containerID := range containerIDs {
		if err := m.cli.ContainerStart(
			m.ctx,
			containerID,
			types.ContainerStartOptions{},
		); err != nil {
			problems = append(
				problems,
				fmt.Sprintf("could not restart container %s: %v", containerID, err),
			)
		}
	}
	if len(problems) != 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func (m *Manager) removeContainerIDs(containerIDs []string) error {
	var firstErr error
	for _, containerID := range containerIDs {
		err := m.cli.ContainerRemove(
			m.ctx,
			containerID,
			types.ContainerRemoveOptions{RemoveVolumes: true, Force: true},
		)
		if err != nil && !client.IsErrNotFound(err) && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// stopContainers permanently removes an instance's containers and their
// runtime metadata. Staged updates use pauseContainerProcesses instead.
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

func configureContainerSeccomp(
	cConfig *container.Config,
	hConfig *container.HostConfig,
	options *SeccompOptions,
	hostInfo types.Info,
) error {
	if options == nil {
		return nil
	}
	if hostInfo.OSType != "linux" {
		return fmt.Errorf("seccomp configuration is only supported by Linux Docker hosts")
	}
	if len(options.Tweaks) != 0 {
		if !seccompRuntimeReady(hostInfo) {
			return fmt.Errorf(
				"seccomp tweaks require compatible Docker runtime %q; on the Docker host run: %s",
				ociinterceptor.RuntimeName,
				ociinterceptor.RegistrationCommand,
			)
		}
		hConfig.Runtime = ociinterceptor.RuntimeName
		cConfig.Env = append(
			cConfig.Env,
			ociinterceptor.TweakEnvironmentVariable+"="+strings.Join(options.Tweaks, ","),
		)
		return nil
	}
	if options.effectiveProfile != "" {
		hConfig.SecurityOpt = append(hConfig.SecurityOpt, "seccomp="+options.effectiveProfile)
	}
	return nil
}

// preflightChallengeSeccomp verifies each runtime host's effective policy
// against the connected Docker daemon before an update mutates persisted
// challenge state.
func (m *Manager) preflightChallengeSeccomp(metadata *ChallengeMetadata) error {
	requiresSeccomp := false
	for _, options := range metadata.ChallengeOptions.Overrides {
		if options.Seccomp != nil &&
			(len(options.Seccomp.Tweaks) != 0 ||
				options.Seccomp.effectiveProfile != "") {
			requiresSeccomp = true
			break
		}
	}
	if !requiresSeccomp {
		return nil
	}

	hostInfo, err := m.cli.Info(m.ctx)
	if err != nil {
		return fmt.Errorf("could not preflight seccomp configuration: %v", err)
	}
	for _, host := range metadata.Hosts {
		if host.Name == "builder" {
			continue
		}
		options, ok := effectiveContainerOptions(
			metadata.ChallengeOptions.Overrides,
			strings.ToLower(host.Name),
		)
		if !ok || options.Seccomp == nil {
			continue
		}
		var config container.Config
		var hostConfig container.HostConfig
		if err = configureContainerSeccomp(
			&config,
			&hostConfig,
			options.Seccomp,
			hostInfo,
		); err != nil {
			return fmt.Errorf(
				"seccomp preflight failed for challenge %q container %q: %v",
				metadata.Id,
				host.Name,
				err,
			)
		}
	}
	return nil
}

func seccompRuntimeReady(hostInfo types.Info) bool {
	runtimeConfig, ok := hostInfo.Runtimes[ociinterceptor.RuntimeName]
	return ok && ociinterceptor.RuntimeRegistrationCompatible(
		runtimeConfig.Path,
		runtimeConfig.Args,
	)
}

func seccompRuntimeWarning(hostInfo types.Info) string {
	if hostInfo.OSType != "linux" {
		return ""
	}
	if seccompRuntimeReady(hostInfo) {
		return ""
	}
	return fmt.Sprintf(
		"seccomp tweaks are unavailable until this command is run on the Docker host: %s (registers or updates Docker runtime %q)",
		ociinterceptor.RegistrationCommand,
		ociinterceptor.RuntimeName,
	)
}
