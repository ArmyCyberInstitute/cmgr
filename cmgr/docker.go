package cmgr

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ArmyCyberInstitute/cmgr/internal/ociinterceptor"
	"github.com/containerd/errdefs"
	"github.com/docker/go-units"
	"github.com/moby/moby/api/pkg/authconfig"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/registry"
	"github.com/moby/moby/api/types/strslice"
	"github.com/moby/moby/api/types/system"
	"github.com/moby/moby/client"
)

func (m *Manager) initDocker() error {
	cli, err := client.New(client.FromEnv)
	if err != nil {
		m.log.errorf("could not create docker client: %s", err)
		return err
	}

	m.cli = cli
	m.ctx = context.Background()

	ping, err := cli.Ping(
		m.ctx,
		client.PingOptions{NegotiateAPIVersion: true},
	)
	if err != nil {
		m.log.errorf("could not connect to docker engine: %s", err)
		return err
	}

	m.log.infof("connected to docker (API v%s)", ping.APIVersion)

	hostInfoResult, infoErr := cli.Info(m.ctx, client.InfoOptions{})
	if infoErr != nil {
		m.log.warnf(
			"could not determine whether seccomp tweaks are available: %s",
			infoErr,
		)
	} else if warning := seccompRuntimeWarning(hostInfoResult.Info); warning != "" {
		m.log.warn(warning)
	}
	if _, requested := os.LookupEnv(DISK_QUOTA_ENV); requested {
		if infoErr != nil {
			m.log.warnf("disk quotas disabled because Docker storage could not be inspected")
		} else {
			info := hostInfoResult.Info
			switch info.Driver {
			case "zfs":
				m.diskQuotasEnabled.Store(true)
			case "overlay2":
				for _, status := range info.DriverStatus {
					if len(status) >= 2 &&
						strings.EqualFold(status[0], "Backing Filesystem") &&
						strings.EqualFold(status[1], "xfs") {
						// overlay2 size limits additionally require the XFS
						// filesystem to be mounted with project quotas. Docker
						// reports an actionable error at create time if it is not.
						m.diskQuotasEnabled.Store(true)
						break
					}
				}
			}
			if !m.diskQuotasEnabled.Load() {
				m.log.warnf(
					"disk quotas disabled: Docker driver %q does not report a supported backing store",
					info.Driver,
				)
			}
		}
	}

	chalInterface, isSet := os.LookupEnv(IFACE_ENV)
	if !isSet {
		chalInterface = "0.0.0.0"
	}
	m.challengeInterface = chalInterface

	m.challengeRegistry, isSet = os.LookupEnv(REGISTRY_ENV)
	if isSet {
		m.authString, err = authconfig.Encode(registry.AuthConfig{
			Username:      os.Getenv(REGISTRY_USER_ENV),
			Password:      os.Getenv(REGISTRY_TOKEN_ENV),
			ServerAddress: strings.SplitN(m.challengeRegistry, "/", 2)[0],
		})
		if err != nil {
			return fmt.Errorf("could not encode registry authentication: %w", err)
		}
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

	if low < 1024 || high >= (1<<16) || high < low {
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
		return "", fmt.Errorf("could not load assigned ports: %w", err)
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
	*flag = strings.Replace(b.Format, "%s", sumStr, 1)
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

func challengeSourceVersion(challenge *ChallengeMetadata) string {
	if challenge.SourceDigest != "" {
		return challenge.SourceDigest
	}
	return fmt.Sprintf("%08x", challenge.SourceChecksum)
}

func (i *InstanceMetadata) getNetworkName() string {
	return fmt.Sprintf("cmgr-%d", i.Id)
}

func (m *Manager) acquireBuildLock(build *BuildMetadata) func() {
	key := fmt.Sprintf(
		"%s\x00%s\x00%s\x00%d",
		build.Schema,
		build.Format,
		build.Challenge,
		build.Seed,
	)
	m.buildLocksMu.Lock()
	lock := m.buildLocks[key]
	if lock == nil {
		lock = new(buildLock)
		m.buildLocks[key] = lock
	}
	lock.refs++
	m.buildLocksMu.Unlock()

	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()
		m.buildLocksMu.Lock()
		lock.refs--
		if lock.refs == 0 {
			delete(m.buildLocks, key)
		}
		m.buildLocksMu.Unlock()
	}
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

		releaseBuildLock := m.acquireBuildLock(build)
		if build.Id == 0 {
			err = m.openBuild(build)
			if err != nil {
				releaseBuildLock()
				return err
			}
		} else {
			requestedCount := build.InstanceCount
			persisted, lookupErr := m.lookupBuildMetadata(build.Id)
			if lookupErr != nil {
				releaseBuildLock()
				return lookupErr
			}
			if persisted.Flag != "" {
				*build = *persisted
				build.InstanceCount = requestedCount
			}
		}
		// A concurrent request may have completed this build before we acquired
		// its keyed lock. openBuild reloads all persisted metadata on conflict.
		if build.Flag != "" {
			releaseBuildLock()
			continue
		}

		if m.buildSlots != nil {
			m.buildSlots <- struct{}{}
		}
		err = m.executeBuild(cMeta, build, buildCtxFile, "")
		if m.buildSlots != nil {
			<-m.buildSlots
		}
		if err != nil {
			if cleanupErr := m.discardStagedBuild(cMeta, build, ""); cleanupErr != nil {
				err = errors.Join(err, cleanupErr)
			}
			if cleanupErr := m.removeBuildMetadata(build.Id); cleanupErr != nil {
				err = errors.Join(
					err,
					fmt.Errorf(
						"could not remove failed build %d metadata: %w",
						build.Id,
						cleanupErr,
					),
				)
			}
			releaseBuildLock()
			return err
		}

		err = m.finalizeBuild(build)
		if err != nil {
			if cleanupErr := m.discardStagedBuild(cMeta, build, ""); cleanupErr != nil {
				err = errors.Join(err, cleanupErr)
			}
			if cleanupErr := m.removeBuildMetadata(build.Id); cleanupErr != nil {
				err = errors.Join(
					err,
					fmt.Errorf(
						"could not remove unfinalized build %d metadata: %w",
						build.Id,
						cleanupErr,
					),
				)
			}
		}
		releaseBuildLock()
		if err != nil {
			return err
		}
	}

	return nil
}

type dockerProgressMessage struct {
	Error       string `json:"error"`
	ErrorDetail *struct {
		Message string `json:"message"`
	} `json:"errorDetail"`
	Stream string `json:"stream"`
	Status string `json:"status"`
}

func consumeDockerProgress(response io.ReadCloser, operation string) error {
	defer response.Close()

	const outputTailLimit = 8 * 1024
	var outputTail string
	appendOutput := func(output string) {
		if output == "" {
			return
		}
		outputTail += output
		if len(outputTail) > outputTailLimit {
			outputTail = outputTail[len(outputTail)-outputTailLimit:]
		}
	}
	responseError := func(message string) error {
		message = strings.TrimSpace(message)
		outputTail = strings.TrimSpace(outputTail)
		if outputTail == "" {
			return fmt.Errorf("Docker %s failed: %s", operation, message)
		}
		return fmt.Errorf(
			"Docker %s failed: %s\nDocker output (tail):\n%s",
			operation,
			message,
			outputTail,
		)
	}

	decoder := json.NewDecoder(response)
	for {
		var message dockerProgressMessage
		err := decoder.Decode(&message)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("failed to decode Docker %s response: %w", operation, err)
		}

		appendOutput(message.Stream)
		if message.Status != "" {
			appendOutput(message.Status + "\n")
		}
		if message.ErrorDetail != nil && message.ErrorDetail.Message != "" {
			return responseError(message.ErrorDetail.Message)
		}
		if message.Error != "" {
			return responseError(message.Error)
		}
	}
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

	imageName := fmt.Sprintf(
		"%s/%s:%s",
		m.challengeRegistry,
		challengeToFreezeName(challenge),
		challengeSourceVersion(cMeta),
	)

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
	opts := client.ImageBuildOptions{
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

	if err := consumeDockerProgress(resp.Body, "base image build"); err != nil {
		m.log.error(err)
		return err
	}

	// Push the image
	pushOpts := client.ImagePushOptions{RegistryAuth: m.authString}
	pushResp, err := m.cli.ImagePush(m.ctx, imageName, pushOpts)
	if err != nil {
		m.log.errorf("failed to push base image: %s", err)
		return err
	}

	if err := consumeDockerProgress(pushResp, "base image push"); err != nil {
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
	cloned.RequiredSeccompTweaks = append(
		SeccompTweakList(nil),
		build.RequiredSeccompTweaks...,
	)
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
	installedArtifact bool
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
	if err := m.discardStagedBuild(
		update.metadata,
		update.candidate,
		update.qualifier,
	); err != nil {
		errs = append(errs, err)
	}
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
	removeOptions := client.ImageRemoveOptions{PruneChildren: true}
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
		); err != nil && !errdefs.IsNotFound(err) {
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
		inspection, err := m.cli.ImageInspect(m.ctx, canonical)
		if err == nil {
			promotion.canonicalImageIDs[canonical] = inspection.ID
		} else if errdefs.IsNotFound(err) {
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
		if _, err := m.cli.ImageTag(
			m.ctx,
			client.ImageTagOptions{Source: staged, Target: canonical},
		); err != nil {
			operationErr := fmt.Errorf(
				"could not promote image %s to %s: %v",
				staged,
				canonical,
				err,
			)
			if rollbackErr := m.rollbackStagedBuild(promotion); rollbackErr != nil {
				operationErr = errors.Join(
					operationErr,
					fmt.Errorf("could not roll back staged build: %w", rollbackErr),
				)
			}
			return nil, operationErr
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
			operationErr := fmt.Errorf("could not preserve current artifacts: %v", err)
			if rollbackErr := m.rollbackStagedBuild(promotion); rollbackErr != nil {
				operationErr = errors.Join(
					operationErr,
					fmt.Errorf("could not roll back staged build: %w", rollbackErr),
				)
			}
			return nil, operationErr
		}
		promotion.hadArtifact = true
	} else if !os.IsNotExist(err) {
		operationErr := fmt.Errorf("could not inspect current artifacts: %v", err)
		if rollbackErr := m.rollbackStagedBuild(promotion); rollbackErr != nil {
			operationErr = errors.Join(
				operationErr,
				fmt.Errorf("could not roll back staged build: %w", rollbackErr),
			)
		}
		return nil, operationErr
	}

	if build.HasArtifacts {
		if err := os.Rename(stagedArtifact, promotion.canonicalArtifact); err != nil {
			operationErr := fmt.Errorf("could not promote staged artifacts: %v", err)
			if rollbackErr := m.rollbackStagedBuild(promotion); rollbackErr != nil {
				operationErr = errors.Join(
					operationErr,
					fmt.Errorf("could not roll back staged build: %w", rollbackErr),
				)
			}
			return nil, operationErr
		}
		promotion.installedArtifact = true
	}
	return promotion, nil
}

func (m *Manager) rollbackStagedBuild(promotion *stagedBuildPromotion) error {
	var firstErr error
	for canonical, imageID := range promotion.canonicalImageIDs {
		if imageID == "" {
			removeOptions := client.ImageRemoveOptions{PruneChildren: true}
			if _, err := m.cli.ImageRemove(
				m.ctx,
				canonical,
				removeOptions,
			); err != nil && !errdefs.IsNotFound(err) && firstErr == nil {
				firstErr = err
			}
		} else if _, err := m.cli.ImageTag(
			m.ctx,
			client.ImageTagOptions{Source: imageID, Target: canonical},
		); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if promotion.installedArtifact {
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
	removeOptions := client.ImageRemoveOptions{PruneChildren: false}
	for _, imageName := range promotion.stagedImageNames {
		if _, err := m.cli.ImageRemove(m.ctx, imageName, removeOptions); err != nil &&
			!errdefs.IsNotFound(err) && firstErr == nil {
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
) error {
	var errs []error
	removeOptions := client.ImageRemoveOptions{PruneChildren: true}
	for _, host := range metadata.Hosts {
		image := Image{Host: host.Name}
		imageName := buildImageName(metadata.Id, build, image, qualifier)
		if _, err := m.cli.ImageRemove(
			m.ctx,
			imageName,
			removeOptions,
		); err != nil && !errdefs.IsNotFound(err) {
			errs = append(
				errs,
				fmt.Errorf("could not discard image %s: %w", imageName, err),
			)
		}
	}
	artifactPath := filepath.Join(
		m.artifactsDir,
		build.getArtifactsFilenameForQualifier(qualifier),
	)
	if err := os.Remove(artifactPath); err != nil &&
		!errors.Is(err, os.ErrNotExist) {
		errs = append(
			errs,
			fmt.Errorf("could not discard artifacts %s: %w", artifactPath, err),
		)
	}
	return errors.Join(errs...)
}

// The Docker client passes build contexts to net/http as request bodies.
// net/http closes a request body after RoundTrip returns, so a subsequent
// defensive close commonly reports os.ErrClosed. Treat that as successful
// while preserving any other close failure.
func closeBuildContextFile(file *os.File) error {
	err := file.Close()
	if errors.Is(err, os.ErrClosed) {
		return nil
	}
	return err
}

func unsupportedStorageQuotaError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "storage-opt") &&
		(strings.Contains(message, "pquota") ||
			strings.Contains(message, "quota") ||
			strings.Contains(message, "not supported") ||
			strings.Contains(message, "supported only"))
}

func (m *Manager) executeBuild(
	cMeta *ChallengeMetadata,
	bMeta *BuildMetadata,
	buildCtxFile string,
	qualifier string,
) error {

	seedStr := fmt.Sprintf("%d", bMeta.Seed)

	baseName := fmt.Sprintf(
		"%s/%s:%s",
		m.challengeRegistry,
		challengeToFreezeName(cMeta.Id),
		challengeSourceVersion(cMeta),
	)
	pullOpts := client.ImagePullOptions{RegistryAuth: m.authString}
	var buildCache []string
	pullResp, err := m.cli.ImagePull(m.ctx, baseName, pullOpts)
	if err == nil {
		if err := consumeDockerProgress(pullResp, "base image pull"); err == nil {
			m.log.infof("Successfully pulled base image '%s'", baseName)
			buildCache = append(buildCache, baseName)
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
		opts := client.ImageBuildOptions{
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

		m.log.debugf("creating image %s", imageName)
		resp, err := m.cli.ImageBuild(m.ctx, buildCtx, opts)
		closeErr := closeBuildContextFile(buildCtx)
		if err != nil {
			m.log.errorf("failed to build base image: %s", err)
			return err
		}
		if closeErr != nil {
			_ = resp.Body.Close()
			return fmt.Errorf("could not close build context: %w", closeErr)
		}

		if err := consumeDockerProgress(resp.Body, "challenge image build"); err != nil {
			m.log.error(err)
			return err
		}
		images = append(images, image)
		// Multi-container and builder/challenge targets share the same context.
		// Explicitly offer each completed target to the next build so daemon
		// cache behavior does not cause common stages to be rebuilt.
		buildCache = append(buildCache, imageName)
	}

	if buildImage == "" {
		err := fmt.Errorf("aborting because no build image identified %s/%d", cMeta.Id, bMeta.Id)
		m.log.error(err)
		return err
	}

	cConfig := container.Config{Image: buildImage}
	hConfig := container.HostConfig{}
	nConfig := network.NetworkingConfig{}

	respCC, err := m.cli.ContainerCreate(
		m.ctx,
		client.ContainerCreateOptions{
			Config:           &cConfig,
			HostConfig:       &hConfig,
			NetworkingConfig: &nConfig,
		},
	)
	if err != nil {
		m.log.errorf("failed to create artifacts container: %s", err)
		return err
	}

	cid := respCC.ID
	if err := m.retireContainer(cid); err != nil {
		removeOptions := client.ContainerRemoveOptions{
			RemoveVolumes: true,
			RemoveLinks:   false,
			Force:         true,
		}
		_, removeErr := m.cli.ContainerRemove(m.ctx, cid, removeOptions)
		return errors.Join(
			fmt.Errorf("could not track temporary build container %s: %w", cid, err),
			removeErr,
		)
	}
	defer func() {
		if cleanupErr := m.removeRetiredContainerIDs(
			[]string{cid},
		); cleanupErr != nil {
			m.log.warnf(
				"could not remove temporary build container %s: %v",
				cid,
				cleanupErr,
			)
		}
	}()

	m.log.infof("created container %s", cid)

	copyResult, err := m.cli.CopyFromContainer(
		m.ctx,
		cid,
		client.CopyFromContainerOptions{SourcePath: "/challenge"},
	)
	if err != nil {
		m.log.errorf("could not find '/challenge' in container: %s", err)
		return err
	}
	metaFile := copyResult.Content
	defer metaFile.Close()

	cTar := tar.NewReader(metaFile)
	var hdr *tar.Header
	var lookups map[string]string
	var files []string
	var flag string
	metadataFound := false
	artifactsFound := false
	for hdr, err = cTar.Next(); err == nil; hdr, err = cTar.Next() {
		m.log.debugf("found in tar: %s", hdr.Name)
		if hdr.Name == "challenge/metadata.json" {
			if metadataFound {
				return errors.New("build output contains metadata.json more than once")
			}
			metadataFound = true
			const maxBuildMetadataBytes = 1024 * 1024
			data, err := ioutil.ReadAll(io.LimitReader(cTar, maxBuildMetadataBytes+1))
			if err != nil {
				m.log.errorf("could not read metadata.json: %s", err)
				return err
			}
			if len(data) > maxBuildMetadataBytes {
				return fmt.Errorf(
					"build metadata exceeds %d bytes",
					maxBuildMetadataBytes,
				)
			}

			lookups = make(map[string]string)
			decoder := json.NewDecoder(strings.NewReader(string(data)))
			if err = decoder.Decode(&lookups); err != nil {
				m.log.errorf("could not decode build metadata JSON file: %s", err)
				return err
			}
			if err = decoder.Decode(&struct{}{}); err != io.EOF {
				return errors.New("build metadata contains trailing JSON data")
			}

			var ok bool
			flag, ok = lookups["flag"]
			if !ok {
				err = errors.New("build metadata missing the flag")
				m.log.error(err)
				return err
			}

			delete(lookups, "flag")

			bMeta.RequiredSeccompTweaks, err = consumeBuildSeccompTweaks(lookups)
			if err != nil {
				m.log.error(err)
				return err
			}
		} else if hdr.Name == "challenge/artifacts.tar.gz" {
			if artifactsFound {
				return errors.New("build output contains artifacts.tar.gz more than once")
			}
			artifactsFound = true
			artifactsFileName := bMeta.getArtifactsFilenameForQualifier(qualifier)
			artifactsPath := filepath.Join(m.artifactsDir, artifactsFileName)
			files, err = m.cacheArtifacts(
				cTar,
				artifactsPath,
			)
			if err != nil {
				m.log.errorf("could not cache build artifacts: %s", err)
				return err
			}
			if len(files) == 0 {
				if err := os.Remove(artifactsPath); err != nil &&
					!errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf(
						"could not remove empty artifact archive: %w",
						err,
					)
				}
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
	maxFlagBytes := m.policy.MaxSolverFlagBytes
	if maxFlagBytes == 0 {
		maxFlagBytes = 4 * 1024
	}
	if int64(len(flag)) > maxFlagBytes {
		return fmt.Errorf(
			"build flag is %d bytes; maximum is %d",
			len(flag),
			maxFlagBytes,
		)
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

		iro := client.ImageRemoveOptions{Force: false, PruneChildren: true}
		for _, image := range bMeta.Images {
			imageName := buildImageName(bMeta.Challenge, bMeta, image, qualifier)
			m.cli.ImageRemove(m.ctx, imageName, iro)
		}
	}

	m.log.debugf("%v", bMeta)

	return err
}

func challengeNetworkCreateOptions(opts NetworkOptions) client.NetworkCreateOptions {
	options := client.NetworkCreateOptions{Driver: "bridge"}
	if !opts.AllowEgress {
		// Docker's Internal setting also suppresses host port publishing on
		// current engines. Disabling masquerading instead preserves published
		// ingress while preventing Internet egress in the standard bridge/NAT
		// topology used by cmgr's Linux deployments.
		options.Options = map[string]string{
			"com.docker.network.bridge.enable_ip_masquerade": "false",
		}
	}
	return options
}

func (m *Manager) startNetwork(instance *InstanceMetadata, opts NetworkOptions) error {
	netSpec := challengeNetworkCreateOptions(opts)
	netname := instance.getNetworkName()
	_, err := m.cli.NetworkCreate(m.ctx, netname, netSpec)
	if err != nil {
		m.log.errorf("could not create challenge network (%s): %s", netname, err)
	}
	return err
}

func (m *Manager) stopNetwork(instance *InstanceMetadata) error {
	networkName := instance.getNetworkName()
	_, err := m.cli.NetworkRemove(
		m.ctx,
		networkName,
		client.NetworkRemoveOptions{},
	)
	if err != nil {
		if errdefs.IsNotFound(err) {
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
	hostIP, err := netip.ParseAddr(m.challengeInterface)
	if err != nil {
		return fmt.Errorf("invalid challenge interface %q: %v", m.challengeInterface, err)
	}
	// Call create in docker
	netname := instance.getNetworkName()
	for _, image := range build.Images {
		if image.Host == "builder" {
			continue
		}
		exposedPorts := network.PortSet{}
		publishedPorts := network.PortMap{}
		expectedPorts := make(map[network.Port]string, len(image.Ports))
		for _, portStr := range image.Ports {
			port, err := network.ParsePort(portStr)
			if err != nil {
				return fmt.Errorf("invalid image port %q: %v", portStr, err)
			}
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
			expectedPorts[port] = portName
			hostPort, err := m.selectHostPort(portName, preferredPorts)
			if err != nil {
				return err
			}
			exposedPorts[port] = struct{}{}
			publishedPorts[port] = []network.PortBinding{
				{HostIP: hostIP, HostPort: hostPort},
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
		cOpts = mergeRuntimeDefaults(m.runtimeDefaults, cOpts)
		hasContainerOpts = hasContainerOpts ||
			cOpts.Cpus != "" ||
			cOpts.Memory != "" ||
			cOpts.PidsLimit != 0 ||
			len(cOpts.Ulimits) != 0
		if hasContainerOpts {
			hConfig.Init = &cOpts.Init
			if cOpts.Cpus != "" {
				nanoCpus, err := parseNanoCPUs(cOpts.Cpus)
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
				hConfig.MemorySwap = memoryBytes
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
				if m.diskQuotasEnabled.Load() {
					var storageOpt = map[string]string{
						"size": cOpts.DiskQuota,
					}
					hConfig.StorageOpt = storageOpt
				} else {
					m.log.warnf("disk quota for %s container '%s' ignored (disk quotas are unavailable or not enabled)", build.Challenge, image.Host)
				}
			}
			if cOpts.CgroupParent != "" {
				hConfig.CgroupParent = cOpts.CgroupParent
			}
		}

		effectiveSeccomp, err := withRequiredSeccompTweaks(
			cOpts.Seccomp,
			build.RequiredSeccompTweaks,
		)
		if err != nil {
			return fmt.Errorf(
				"invalid seccomp requirements for challenge %q container %q: %v",
				build.Challenge,
				image.Host,
				err,
			)
		}
		if effectiveSeccomp != nil &&
			(len(effectiveSeccomp.Tweaks) != 0 ||
				effectiveSeccomp.effectiveProfile != "") {
			hostInfoResult, err := m.cli.Info(m.ctx, client.InfoOptions{})
			if err != nil {
				return err
			}
			if effectiveSeccomp.effectiveProfile != "" {
				m.log.debugf(
					"inserting custom seccomp profile (%s)",
					effectiveSeccomp.ProfileHash,
				)
			}
			if len(effectiveSeccomp.Tweaks) != 0 {
				m.log.debugf(
					"requesting OCI seccomp tweaks: %s",
					strings.Join(effectiveSeccomp.Tweaks, ","),
				)
			}
			if err = configureContainerSeccomp(
				&cConfig,
				&hConfig,
				effectiveSeccomp,
				hostInfoResult.Info,
			); err != nil {
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

		createOptions := client.ContainerCreateOptions{
			Config:           &cConfig,
			HostConfig:       &hConfig,
			NetworkingConfig: &nConfig,
		}
		respCC, err := m.cli.ContainerCreate(
			m.ctx,
			createOptions,
		)
		if err != nil &&
			len(hConfig.StorageOpt) != 0 &&
			unsupportedStorageQuotaError(err) {
			m.diskQuotasEnabled.Store(false)
			hConfig.StorageOpt = nil
			m.log.warnf(
				"disk quota for %s container %q ignored after Docker rejected quota support: %v",
				build.Challenge,
				image.Host,
				err,
			)
			respCC, err = m.cli.ContainerCreate(m.ctx, createOptions)
		}
		if err != nil {
			m.log.errorf("failed to create instance container: %s", err)
			return err
		}

		cid := respCC.ID
		instance.Containers = append(instance.Containers, cid)
		// Treat every new container as retired until its active instance
		// metadata commits. If cmgr exits between Docker creation and the
		// database cutover, startup recovery can still remove it.
		if err := m.retireContainer(cid); err != nil {
			return fmt.Errorf(
				"could not record pending container %s: %w",
				cid,
				err,
			)
		}
		m.log.infof("created new image: %s", cid)

		_, err = m.cli.ContainerStart(m.ctx, cid, client.ContainerStartOptions{})
		if err != nil {
			m.log.errorf("failed to start container: %s", err)
			return err
		}

		done := false
		for backoff := time.Millisecond; backoff < time.Second; backoff *= 2 {
			m.log.debug("Querying docker for port info...")

			cInfo, err := m.cli.ContainerInspect(
				m.ctx,
				cid,
				client.ContainerInspectOptions{},
			)
			if err != nil {
				m.log.errorf("failed to get container info: %s", err)
				return err
			}
			assignments, ready, err := inspectedPortAssignments(
				cid,
				expectedPorts,
				cInfo.Container,
			)
			if err != nil {
				return err
			}
			if ready {
				for portName, hostPort := range assignments {
					instance.Ports[portName] = hostPort
					m.log.debugf(
						"container port %s mapped to %d",
						portName,
						hostPort,
					)
				}
				done = true
				break
			}
			time.Sleep(backoff)
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

func inspectedPortAssignments(
	containerID string,
	expectedPorts map[network.Port]string,
	inspection container.InspectResponse,
) (map[string]int, bool, error) {
	state := inspection.State
	if state == nil {
		return nil, false, fmt.Errorf(
			"container %s inspection omitted runtime state",
			containerID,
		)
	}
	if !state.Running || state.Paused || state.Restarting || state.Dead {
		return nil, false, fmt.Errorf(
			"container %s failed to remain running (status %q, exit code %d, error %q)",
			containerID,
			state.Status,
			state.ExitCode,
			state.Error,
		)
	}
	if len(expectedPorts) == 0 {
		return map[string]int{}, true, nil
	}
	if inspection.NetworkSettings == nil {
		return nil, false, nil
	}

	assignments := make(map[string]int, len(expectedPorts))
	for containerPort, portName := range expectedPorts {
		hostPortInfo := inspection.NetworkSettings.Ports[containerPort]
		if len(hostPortInfo) == 0 {
			return nil, false, nil
		}
		hostPort, err := strconv.Atoi(string(hostPortInfo[0].HostPort))
		if err != nil || hostPort <= 0 || hostPort >= 1<<16 {
			return nil, false, fmt.Errorf(
				"container %s reported invalid host port %q for %s: %v",
				containerID,
				hostPortInfo[0].HostPort,
				containerPort,
				err,
			)
		}
		assignments[portName] = hostPort
	}
	return assignments, true, nil
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
		if cleanupErr := m.removeRetiredContainerIDs(
			candidate.Containers,
		); cleanupErr != nil {
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
		if cleanupErr := m.removeRetiredContainerIDs(
			candidate.Containers,
		); cleanupErr != nil {
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
	// Restore the durable ownership record before removing the replacement.
	// If cmgr exits after this commit, startup recovery sees the replacement as
	// retired and the old containers as active, and can finish both operations.
	if err := m.replaceInstanceRuntimeMetadata(cutover.old); err != nil {
		return fmt.Errorf("restore instance metadata: %w", err)
	}
	if err := m.removeRetiredContainerIDs(
		cutover.candidate.Containers,
	); err != nil {
		problems = append(problems, fmt.Sprintf("remove replacement containers: %v", err))
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
	return m.removeRetiredContainerIDs(cutover.old.Containers)
}

// pauseContainerProcesses stops containers without removing them or their
// database rows. Staged challenge updates retain them for rollback.
func (m *Manager) pauseContainerProcesses(
	containerIDs []string,
) ([]string, error) {
	timeout := 10
	paused := make([]string, 0, len(containerIDs))
	for _, containerID := range containerIDs {
		if _, err := m.cli.ContainerStop(
			m.ctx,
			containerID,
			client.ContainerStopOptions{Timeout: &timeout},
		); err != nil {
			return paused, fmt.Errorf("could not stop container %s: %v", containerID, err)
		}
		paused = append(paused, containerID)
	}
	return paused, nil
}

func (m *Manager) resumeContainerProcesses(containerIDs []string) error {
	var problems []string
	for _, containerID := range containerIDs {
		if _, err := m.cli.ContainerStart(
			m.ctx,
			containerID,
			client.ContainerStartOptions{},
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

func (m *Manager) removeRetiredContainerIDs(containerIDs []string) error {
	var errs []error
	for _, containerID := range containerIDs {
		_, err := m.cli.ContainerRemove(
			m.ctx,
			containerID,
			client.ContainerRemoveOptions{RemoveVolumes: true, Force: true},
		)
		if err != nil && !errdefs.IsNotFound(err) {
			errs = append(errs, fmt.Errorf("remove retired container %s: %w", containerID, err))
			continue
		}
		if err := m.forgetRetiredContainer(containerID); err != nil {
			errs = append(errs, fmt.Errorf("forget retired container %s: %w", containerID, err))
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) retryRetiredResources() error {
	var errs []error
	containerIDs, err := m.retiredContainerIDs()
	if err != nil {
		errs = append(errs, err)
	} else if err := m.removeRetiredContainerIDs(containerIDs); err != nil {
		errs = append(errs, err)
	}
	networkNames, err := m.retiredNetworkNames()
	if err != nil {
		errs = append(errs, err)
	} else {
		for _, networkName := range networkNames {
			_, removeErr := m.cli.NetworkRemove(
				m.ctx,
				networkName,
				client.NetworkRemoveOptions{},
			)
			if removeErr != nil && !errdefs.IsNotFound(removeErr) {
				errs = append(
					errs,
					fmt.Errorf("remove retired network %s: %w", networkName, removeErr),
				)
				continue
			}
			if forgetErr := m.forgetRetiredNetwork(networkName); forgetErr != nil {
				errs = append(errs, forgetErr)
			}
		}
	}
	incompleteInstances, err := m.incompleteInstanceIDs()
	if err != nil {
		errs = append(errs, err)
	} else {
		for _, instanceID := range incompleteInstances {
			instance, lookupErr := m.lookupInstanceMetadata(instanceID)
			if lookupErr != nil {
				errs = append(errs, lookupErr)
				continue
			}
			if removeErr := m.stopNetwork(instance); removeErr != nil {
				if retireErr := m.retireNetwork(
					instance.getNetworkName(),
				); retireErr != nil {
					errs = append(errs, retireErr)
				}
			}
			if removeErr := m.removeInstanceMetadata(instanceID); removeErr != nil {
				errs = append(
					errs,
					fmt.Errorf(
						"remove incomplete instance %d: %w",
						instanceID,
						removeErr,
					),
				)
			}
		}
	}
	trackedContainers, err := m.trackedContainerIDs()
	if err != nil {
		errs = append(errs, err)
	} else {
		for _, containerID := range trackedContainers {
			inspection, inspectErr := m.cli.ContainerInspect(
				m.ctx,
				containerID,
				client.ContainerInspectOptions{},
			)
			if inspectErr != nil {
				errs = append(
					errs,
					fmt.Errorf(
						"inspect tracked container %s: %w",
						containerID,
						inspectErr,
					),
				)
				continue
			}
			if inspection.Container.State != nil &&
				!inspection.Container.State.Running {
				if _, startErr := m.cli.ContainerStart(
					m.ctx,
					containerID,
					client.ContainerStartOptions{},
				); startErr != nil {
					errs = append(
						errs,
						fmt.Errorf(
							"restart tracked container %s: %w",
							containerID,
							startErr,
						),
					)
				}
			}
		}
	}
	return errors.Join(errs...)
}

// stopContainers permanently removes an instance's containers and their
// runtime metadata. Staged updates use pauseContainerProcesses instead.
func (m *Manager) stopContainers(instance *InstanceMetadata) error {
	var errs []error
	remaining := make([]string, 0, len(instance.Containers))
	for _, cid := range instance.Containers {
		opts := client.ContainerRemoveOptions{RemoveVolumes: true, Force: true}
		_, err := m.cli.ContainerRemove(m.ctx, cid, opts)
		if err != nil {
			if errdefs.IsNotFound(err) {
				m.log.warnf("skipped removing container (not found): %s", cid)
			} else {
				m.log.errorf("failed to remove container: %s", err)
				errs = append(errs, fmt.Errorf("remove container %s: %w", cid, err))
				remaining = append(remaining, cid)
				continue
			}
		}
		if err := m.removeContainerMetadata(instance.Id, cid); err != nil {
			errs = append(errs, fmt.Errorf("forget container %s: %w", cid, err))
			remaining = append(remaining, cid)
			continue
		}
		if err := m.forgetRetiredContainer(cid); err != nil {
			errs = append(
				errs,
				fmt.Errorf("forget pending container %s: %w", cid, err),
			)
			remaining = append(remaining, cid)
		}
	}

	instance.Containers = remaining
	if len(remaining) == 0 {
		if err := m.removeInstancePorts(instance.Id); err != nil {
			errs = append(errs, fmt.Errorf("release instance ports: %w", err))
		} else {
			instance.Ports = make(map[string]int)
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) destroyImages(build BuildId) error {
	m.log.debugf("destroying build %d", build)
	bMeta, err := m.lookupBuildMetadata(build)
	if err != nil {
		return err
	}
	instances, err := m.getBuildInstances(build)
	if err != nil {
		return err
	}
	if len(instances) != 0 {
		return &ConflictError{Err: fmt.Errorf(
			"cannot destroy build %d while %d instances remain",
			build,
			len(instances),
		)}
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

	iro := client.ImageRemoveOptions{Force: true, PruneChildren: true}
	for _, image := range bMeta.Images {

		imageName := fmt.Sprintf("%s:%s", bMeta.Challenge, bMeta.dockerId(image))
		_, err := m.cli.ImageRemove(m.ctx, imageName, iro)
		if err != nil {
			if errdefs.IsNotFound(err) {
				m.log.warnf("skipped removing image (not found): %s", imageName)
			} else {
				m.log.errorf("failed to remove image: %s", err)
				return err
			}
		}
	}

	return m.removeBuildMetadata(build)
}

func configureContainerSeccomp(
	cConfig *container.Config,
	hConfig *container.HostConfig,
	options *SeccompOptions,
	hostInfo system.Info,
) error {
	if options == nil {
		return nil
	}
	if hostInfo.OSType != "linux" {
		return fmt.Errorf("seccomp configuration is only supported by Linux Docker hosts")
	}
	if options.effectiveProfile != "" {
		hConfig.SecurityOpt = append(
			hConfig.SecurityOpt,
			"seccomp="+options.effectiveProfile,
		)
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

	hostInfoResult, err := m.cli.Info(m.ctx, client.InfoOptions{})
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
			hostInfoResult.Info,
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

func seccompRuntimeReady(hostInfo system.Info) bool {
	runtimeConfig, ok := hostInfo.Runtimes[ociinterceptor.RuntimeName]
	return ok && ociinterceptor.RuntimeRegistrationCompatible(
		runtimeConfig.Path,
		runtimeConfig.Args,
	)
}

func seccompRuntimeWarning(hostInfo system.Info) string {
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
