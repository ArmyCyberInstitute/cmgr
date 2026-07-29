package cmgr

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/ArmyCyberInstitute/cmgr/cmgr/dockerfiles"
)

const manualSchemaPrefix = "manual-"

var version string

// Returns the version string associated with the build (results of
// `git describe --tags`) or "unknown" if it was not set at build time.
func Version() string {
	if version != "" {
		return version
	}
	return "unknown"
}

// Creates a new instance of the challenge manager validating the appropriate
// environment variables in the process.  A return value of `nil` indicates
// a fatal error occurred during intitialization.
func NewManager(logLevel LogLevel) *Manager {
	mgr := new(Manager)
	mgr.log = newLogger(logLevel)
	mgr.buildLocks = make(map[string]*buildLock)

	mgr.log.infof("version: %s", Version())

	if err := mgr.setDirectories(); err != nil {
		return nil
	}

	if err := mgr.initPolicy(); err != nil {
		mgr.log.error(err)
		return nil
	}

	if err := mgr.initDocker(); err != nil {
		return nil
	}

	if err := mgr.initDatabase(); err != nil {
		return nil
	}

	if err := mgr.retryRetiredResources(); err != nil {
		mgr.log.warnf("could not finish deferred Docker cleanup: %v", err)
	}

	return mgr
}

func randomIdentifier() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("could not generate random identifier: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}

const maxFlagFormatBytes = 128

func validateFlagFormat(format string) error {
	if len(format) > maxFlagFormatBytes {
		return fmt.Errorf(
			"flag format is %d bytes; maximum is %d",
			len(format),
			maxFlagFormatBytes,
		)
	}
	if strings.Count(format, "%s") != 1 {
		return errors.New("flag format must contain exactly one literal %s placeholder")
	}
	if strings.Count(format, "%") != 1 {
		return errors.New("flag format cannot contain directives other than one literal %s")
	}
	return nil
}

func validateSeeds(seeds []int) error {
	seen := make(map[int]struct{}, len(seeds))
	for _, seed := range seeds {
		if _, exists := seen[seed]; exists {
			return fmt.Errorf("seed %d is specified more than once", seed)
		}
		seen[seed] = struct{}{}
	}
	return nil
}

func validateSchemaDefinition(schema *Schema) error {
	if schema == nil {
		return errors.New("schema definition cannot be null")
	}
	if schema.Name == "" {
		return errors.New("schema name cannot be empty")
	}
	if strings.HasPrefix(schema.Name, manualSchemaPrefix) {
		return fmt.Errorf("schema names beginning with %q are reserved", manualSchemaPrefix)
	}
	if err := validateFlagFormat(schema.FlagFormat); err != nil {
		return err
	}
	for challenge, spec := range schema.Challenges {
		if spec.InstanceCount < DYNAMIC_INSTANCES {
			return fmt.Errorf(
				"challenge %q has invalid instance_count %d; use -1 for dynamic instances or a non-negative value",
				challenge,
				spec.InstanceCount,
			)
		}
		if err := validateSeeds(spec.Seeds); err != nil {
			return fmt.Errorf("challenge %q: %w", challenge, err)
		}
	}
	return nil
}

// Traverses the entire directory and captures all valid challenge
// descriptions it comes across.  In general, it will continue even when it
// encounters errors (permission, poorly formatted JSON, etc.) in order to
// give the as much feedback as possible to the caller.  However, it will fail
// fast on two challenges with the same name and namespace.
//
// This function does not have any side-effects on the database or
// built/running challenge state, but changes that it detects will effect new
// builds.  It is important to resolve any issues/errors it raises before
// making any other API calls for affected challenges.  Failure to follow this
// guidance could result in inconsistencies in deployed challenges.
func (m *Manager) DetectChanges(fp string) *ChallengeUpdates {
	if fp == "" {
		fp = m.chalDir
	}

	cu := new(ChallengeUpdates)

	fp, err := m.normalizeDirPath(fp)
	if err != nil {
		cu.Errors = []error{err}
		return cu
	}

	challenges, errs := m.inventoryChallenges(fp)
	db_metadata, err := m.listChallenges()

	if err != nil {
		cu.Errors = append(errs, err)
		return cu
	}

	for _, curr := range db_metadata {
		newMeta, ok := challenges[curr.Id]
		if !ok {
			if pathInDirectory(curr.Path, fp) || !pathInDirectory(curr.Path, m.chalDir) {
				cu.Removed = append(cu.Removed, curr)
			}
			continue
		}

		sourceChanged := curr.SourceDigest == "" ||
			curr.SourceDigest != newMeta.SourceDigest
		metadataChanged := curr.MetadataDigest == "" ||
			curr.MetadataDigest != newMeta.MetadataDigest
		solvescriptChanged := curr.SolveScript != newMeta.SolveScript
		currentMetadata, err := m.lookupChallengeMetadata(curr.Id)
		if err != nil {
			cu.Errors = append(cu.Errors, err)
			delete(challenges, curr.Id)
			continue
		}
		seccompChanged := !seccompPoliciesEqual(
			currentMetadata.ChallengeOptions,
			newMeta.ChallengeOptions,
		)
		if !sourceChanged && !metadataChanged && !solvescriptChanged && !seccompChanged {
			cu.Unmodified = append(cu.Unmodified, curr)
		} else if !sourceChanged && !seccompChanged && m.safeToRefresh(newMeta) {
			m.log.debugf("Marking %s as refresh", newMeta.Id)
			cu.Refreshed = append(cu.Refreshed, newMeta)
		} else {
			cu.Updated = append(cu.Updated, newMeta)
		}
		delete(challenges, curr.Id)
	}

	for _, metadata := range challenges {
		cu.Added = append(cu.Added, metadata)
	}

	cu.Errors = append(cu.Errors, errs...)
	return cu
}

// This will update the global system state based off the changes that are
// detected by a call to `DetectChanges`.  Specifically, in addition to
// updating challenge metadata (new and existing) it will rebuild and, if
// successful restart, existing challenges and then remove the metadata for
// challenges that can no longer be found.  Challenges that have not been
// modified should not be affected.
//
// In the presence of errors, this function will do addition and updates as
// best it can in order to preserve a consistent system state.  However, if a
// build fails, it will keep the existing instance running and rollback the
// challenge metadata.  Additionally, in the presence of errors it will not
// perform any removals of challenge metadata (removing a built challenge is
// considered an error).
func (m *Manager) Update(fp string) *ChallengeUpdates {
	cu := m.DetectChanges(fp)
	candidates := cu.Added
	var errs []error
	cu.Added, errs = m.addChallenges(candidates)
	if len(errs) != 0 {
		cu.Errors = append(cu.Errors, errs...)
	}

	errs = m.updateChallenges(cu.Refreshed, false)
	if len(errs) != 0 {
		cu.Errors = append(cu.Errors, errs...)
	}

	errs = m.updateChallenges(cu.Updated, true)
	if len(errs) != 0 {
		cu.Errors = append(cu.Errors, errs...)
	}

	if len(cu.Errors) == 0 {
		err := m.removeChallenges(cu.Removed)
		if err != nil {
			cu.Errors = append(cu.Errors, err)
		}
	}
	return cu
}

// Builds the "base" stage of the challenge and push it to the Docker
// repository identified by the `CMGR_REGISTRY`.  Any `cmgr` instances
// that use the same repository will then use this base image as the initial
// cache for building the challenge (must match both challenge hash and
// challenge ID). If `CMGR_REGISTRY` is unspecified, the repository has
// not been configured for the active Docker daemon, or an error occurs
// during the build step, then  this function will return a descriptive
// error.  If "force" is `false`, then `cmgr` checks the repository prior to
// attempting to building the "base" image and returns an error if an image
// already exists.  If "force" is `true`, `cmgr` skips this check and
// unconditionally attempts to build and push a base image.
//
// NOTE: There is no validation of whether the "base" stage is self-contained
// (i.e., has copies of all required libraries) so this does not guarantee
// necessarily future builds will work.  Built-in challenge types are
// carefully designed to reduce the risk, but any network traffic after
// the "base" stage(e.g., downloading extra packages or libraries)
// significantly increases the likelihood that the image becomes
// non-functional.  It is ultimately the challenge author's responsibility to
// take proper precautions.
func (m *Manager) Freeze(challenge ChallengeId, force bool) error {
	return m.freezeBaseImage(challenge, force)
}

// Templates out a "challenge" and generates concrete images, flags, and
// lookup values for the seeds provided which is called a "build" and returns
// a list of identifiers that can be used to reference the build in other API
// functions.  This function may take a significant amount of time because it
// will implicitly download base docker images and build the artifacts.
//
// NOTE: if `CMGR_REGISTRY` is specified and the specified challenge and
// challenge hash are found in the repository, then that image will be used
// for the build cache.  This can reduce the risk of dependency changes
// breaking functioning challenges, but can may also make debugging
// challenges harder.  This feature is opt-in by setting the
// `CMGR_REGISTRY` environment variable.
func (m *Manager) Build(challenge ChallengeId, seeds []int, flagFormat string) ([]*BuildMetadata, error) {
	if len(seeds) == 0 {
		return nil, invalidInput(errors.New("at least one seed is required"))
	}
	if err := validateFlagFormat(flagFormat); err != nil {
		return nil, invalidInput(err)
	}
	if err := validateSeeds(seeds); err != nil {
		return nil, invalidInput(err)
	}
	if err := m.validateSeedLimit(len(seeds)); err != nil {
		return nil, invalidInput(err)
	}
	randomSuffix, err := randomIdentifier()
	if err != nil {
		return nil, err
	}
	schema := manualSchemaPrefix + randomSuffix
	instanceCount := -1

	builds := make([]*BuildMetadata, len(seeds))
	for i := range builds {
		builds[i] = &BuildMetadata{
			Seed:          seeds[i],
			Format:        flagFormat,
			Challenge:     challenge,
			Schema:        schema,
			InstanceCount: instanceCount,
		}
	}
	err = m.createSchemaRecord(schema, true)
	if err != nil {
		return nil, err
	}
	err = m.generateBuilds(builds)
	if err != nil {
		var cleanupErrors []error
		buildIDs, lookupErr := m.getSchemaBuilds(schema)
		if lookupErr != nil {
			cleanupErrors = append(cleanupErrors, lookupErr)
		} else {
			for _, buildID := range buildIDs {
				if cleanupErr := m.destroyImages(buildID); cleanupErr != nil {
					cleanupErrors = append(cleanupErrors, cleanupErr)
				}
			}
		}
		if cleanupErr := m.deleteSchemaRecordIfEmpty(schema); cleanupErr != nil {
			cleanupErrors = append(cleanupErrors, cleanupErr)
		}
		err = errors.Join(append([]error{err}, cleanupErrors...)...)
	}
	return builds, err
}

// Creates a running "instance" of the given build and returns its identifier
// on success otherwise an error.
func (m *Manager) Start(build BuildId) (InstanceId, error) {
	// Get build metadata
	bMeta, err := m.lookupBuildMetadata(build)
	if err != nil {
		return 0, err
	}

	if bMeta.InstanceCount != DYNAMIC_INSTANCES {
		return 0, &ConflictError{Err: errors.New(
			"locked build: change the schema definition to start more instances",
		)}
	}

	return m.newInstance(bMeta)
}

func (m *Manager) newInstance(build *BuildMetadata) (id InstanceId, err error) {
	iMeta := &InstanceMetadata{
		Build:      build.Id,
		Ports:      make(map[string]int),
		Containers: []string{},
	}
	err = m.openInstance(iMeta)
	if err != nil {
		return 0, err
	}
	complete := false
	defer func() {
		if complete {
			return
		}
		var cleanupErrs []error
		if cleanupErr := m.stopContainers(iMeta); cleanupErr != nil {
			cleanupErrs = append(cleanupErrs, cleanupErr)
		}
		for _, containerID := range iMeta.Containers {
			if cleanupErr := m.retireContainer(containerID); cleanupErr != nil {
				cleanupErrs = append(cleanupErrs, cleanupErr)
			}
		}
		if cleanupErr := m.stopNetwork(iMeta); cleanupErr != nil {
			cleanupErrs = append(cleanupErrs, cleanupErr)
			if retireErr := m.retireNetwork(iMeta.getNetworkName()); retireErr != nil {
				cleanupErrs = append(cleanupErrs, retireErr)
			}
		}
		if cleanupErr := m.removeInstanceMetadata(iMeta.Id); cleanupErr != nil {
			cleanupErrs = append(cleanupErrs, cleanupErr)
		}
		err = errors.Join(append([]error{err}, cleanupErrs...)...)
	}()

	cMeta, err := m.GetChallengeMetadata(build.Challenge)
	if err != nil {
		return 0, err
	}

	err = m.startNetwork(iMeta, cMeta.ChallengeOptions.NetworkOptions)
	if err != nil {
		return 0, err
	}

	err = m.startContainers(build, iMeta, cMeta.ChallengeOptions.Overrides)
	if err != nil {
		return 0, err
	}

	complete = true
	return iMeta.Id, nil
}

// Stops the running "instance".
func (m *Manager) Stop(instance InstanceId) error {
	// Get instance metadata
	iMeta, err := m.lookupInstanceMetadata(instance)
	if err != nil {
		return err
	}

	// Get build metadata
	bMeta, err := m.lookupBuildMetadata(iMeta.Build)
	if err != nil {
		return err
	}

	if bMeta.InstanceCount != DYNAMIC_INSTANCES {
		return &ConflictError{Err: errors.New(
			"locked build: change the schema definition to stop this instance",
		)}
	}
	return m.stopInstance(iMeta)
}

func (m *Manager) stopInstance(instance *InstanceMetadata) error {
	err := m.stopContainers(instance)
	if err != nil {
		return err
	}

	err = m.stopNetwork(instance)
	if err != nil {
		return err
	}

	return m.removeInstanceMetadata(instance.Id)
}

// Destroys the assoicated "build".
func (m *Manager) Destroy(build BuildId) error {
	// Get build metadata
	bMeta, err := m.lookupBuildMetadata(build)
	if err != nil {
		return err
	}

	manual, err := m.schemaIsManual(bMeta.Schema)
	if err != nil {
		return err
	}
	if !manual {
		return &ConflictError{Err: errors.New(
			"locked build: change the schema definition to destroy this build",
		)}
	}

	if err := m.destroyImages(build); err != nil {
		return err
	}
	return m.deleteSchemaRecordIfEmpty(bMeta.Schema)
}

// Runs the automated solver against the designated instance.
func (m *Manager) CheckInstance(instance InstanceId) error {
	return m.runSolver(instance)
}

// Obtains a list of challenges with minimal version information filled into
// the metadata object.
func (m *Manager) ListChallenges() []*ChallengeMetadata {
	md, _ := m.listChallenges()
	return md
}

// ListChallengesWithError is the error-preserving form used by long-running
// services. ListChallenges is retained for CLI/API compatibility.
func (m *Manager) ListChallengesWithError() ([]*ChallengeMetadata, error) {
	return m.listChallenges()
}

// Obtains a list of challenges which match on all of the given tags.  If no
// tags are passed, then it returns the same results as `ListChallenges`.
// Wildcards are allowed as either '*' or '%' and the search is ASCII case
// insensitive.
func (m *Manager) SearchChallenges(tags []string) []*ChallengeMetadata {
	md, _ := m.searchChallenges(tags)
	return md
}

// SearchChallengesWithError is the error-preserving form used by long-running
// services. SearchChallenges is retained for CLI/API compatibility.
func (m *Manager) SearchChallengesWithError(
	tags []string,
) ([]*ChallengeMetadata, error) {
	return m.searchChallenges(tags)
}

func (m *Manager) MaxRequestBytes() int64 {
	if m.policy.MaxRequestBytes > 0 {
		return m.policy.MaxRequestBytes
	}
	return 1024 * 1024
}

// Lists all schemas as currently defined in the database.
func (m *Manager) ListSchemas() ([]string, error) {
	return m.queryForSchemas()
}

// Uses the schema as a definition of builds and instances that should be
// created/started.  Prevents management of those builds and instances from
// other API calls unless explicitly allowed by the schema.  This call is
// likely to be extremely time and resource intensive as it will start creating
// all of the requested builds immediately and not return until complete.
func (m *Manager) CreateSchema(schema *Schema) []error {
	if err := validateSchemaDefinition(schema); err != nil {
		return []error{invalidInput(err)}
	}
	for challenge, spec := range schema.Challenges {
		if err := m.validateSeedLimit(len(spec.Seeds)); err != nil {
			return []error{invalidInput(fmt.Errorf("challenge %q: %w", challenge, err))}
		}
	}
	exists, err := m.schemaExists(schema.Name)
	if err != nil {
		return []error{err}
	} else if exists {
		return []error{&ConflictError{Err: fmt.Errorf("schema '%s' already exists", schema.Name)}}
	}

	if err := m.createSchemaRecord(schema.Name, false); err != nil {
		return []error{err}
	}
	errs := m.convergeSchema(schema)
	if len(errs) != 0 {
		m.schemaMu.Lock()
		var activeBuilds int
		if err := m.db.Get(
			&activeBuilds,
			`SELECT COUNT(*) FROM builds
			 WHERE schema = ? AND instancecount != ?;`,
			schema.Name,
			LOCKED,
		); err != nil {
			errs = append(errs, err)
		} else if activeBuilds == 0 {
			if err := m.cleanupSchemaResources(schema.Name); err != nil {
				errs = append(errs, err)
			} else if err := m.deleteSchemaRecord(schema.Name); err != nil {
				errs = append(errs, err)
			}
		}
		m.schemaMu.Unlock()
	}
	return errs
}

// Updates the definition of the schema internally and then converges to the
// new definition.  Certain updates are more expensive than others.  In
// particular, updating the flag format will cause a complete rebuild of the
// state.
func (m *Manager) UpdateSchema(schema *Schema) []error {
	if err := validateSchemaDefinition(schema); err != nil {
		return []error{invalidInput(err)}
	}
	for challenge, spec := range schema.Challenges {
		if err := m.validateSeedLimit(len(spec.Seeds)); err != nil {
			return []error{invalidInput(fmt.Errorf("challenge %q: %w", challenge, err))}
		}
	}
	exists, err := m.schemaExists(schema.Name)
	if err != nil {
		return []error{err}
	} else if !exists {
		return []error{unknownSchemaIdError(schema.Name)}
	}

	return m.convergeSchema(schema)
}

func (m *Manager) convergeSchema(schema *Schema) []error {
	m.schemaMu.Lock()
	defer m.schemaMu.Unlock()

	// Recheck ownership while holding the same lock used by DeleteSchema.
	// UpdateSchema performs an early check for a useful client error, but the
	// schema may otherwise be deleted before convergence starts.
	exists, err := m.schemaExists(schema.Name)
	if err != nil {
		return []error{err}
	}
	if !exists {
		return []error{unknownSchemaIdError(schema.Name)}
	}

	createdInstances := []InstanceId{}
	failBeforeActivation := func(operationErr error) []error {
		errs := []error{operationErr}
		for i := len(createdInstances) - 1; i >= 0; i-- {
			instance, lookupErr := m.lookupInstanceMetadata(createdInstances[i])
			if lookupErr != nil {
				errs = append(errs, lookupErr)
				continue
			}
			if cleanupErr := m.stopInstance(instance); cleanupErr != nil {
				errs = append(
					errs,
					fmt.Errorf(
						"could not roll back staged instance %d: %w",
						createdInstances[i],
						cleanupErr,
					),
				)
			}
		}
		return errs
	}

	// Stage and validate the complete desired build set before changing the
	// active schema. Existing builds and instances remain available if any
	// build fails.
	state := make([]*BuildMetadata, 0)
	buildGroups := make([][]*BuildMetadata, 0, len(schema.Challenges))
	for challenge, spec := range schema.Challenges {
		group := make([]*BuildMetadata, 0, len(spec.Seeds))
		for _, seed := range spec.Seeds {
			build := &BuildMetadata{
				Seed:          seed,
				Format:        schema.FlagFormat,
				Challenge:     challenge,
				Schema:        schema.Name,
				InstanceCount: spec.InstanceCount,
			}
			if err := m.stageBuild(build); err != nil {
				return failBeforeActivation(err)
			}
			state = append(state, build)
			group = append(group, build)
		}
		buildGroups = append(buildGroups, group)
	}
	for _, builds := range buildGroups {
		if err := m.generateBuilds(builds); err != nil {
			return failBeforeActivation(err)
		}
	}

	// Scale up before the atomic activation. This preserves the prior schema
	// capacity throughout a replacement and avoids destructive convergence on
	// a build or container failure.
	for _, build := range state {
		target := build.InstanceCount
		if target == DYNAMIC_INSTANCES {
			continue
		}
		instances, err := m.getBuildInstances(build.Id)
		if err != nil {
			return failBeforeActivation(err)
		}
		for i := len(instances); i < target; i++ {
			instanceID, err := m.newInstance(build)
			if err != nil {
				return failBeforeActivation(err)
			}
			createdInstances = append(createdInstances, instanceID)
		}
	}

	targets := make(map[BuildId]int, len(state))
	for _, build := range state {
		targets[build.Id] = build.InstanceCount
	}
	if err := m.activateSchemaBuilds(schema.Name, targets); err != nil {
		return failBeforeActivation(err)
	}

	var errs []error
	if err := m.cleanupSchemaResources(schema.Name); err != nil {
		errs = append(errs, err)
	}

	// Reductions are deliberately last. At this point the desired definition is
	// durable, so any failed removals stay tracked and can be retried.
	for _, build := range state {
		target := build.InstanceCount
		if target == DYNAMIC_INSTANCES {
			continue
		}
		instances, err := m.getBuildInstances(build.Id)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for i := target; i < len(instances); i++ {
			instance, err := m.lookupInstanceMetadata(instances[i])
			if err == nil {
				err = m.stopInstance(instance)
			}
			if err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errs
}

// Tears down all instances and builds belonging to the schema.
func (m *Manager) DeleteSchema(name string) error {
	m.schemaMu.Lock()
	defer m.schemaMu.Unlock()

	manual, err := m.schemaIsManual(name)
	if err != nil {
		return err
	}
	if manual {
		return &ConflictError{Err: fmt.Errorf(
			"schema %q is owned by manual builds",
			name,
		)}
	}
	err = m.lockSchema(name)
	if err != nil {
		return err
	}

	if err := m.cleanupSchemaResources(name); err != nil {
		return err
	}
	return m.deleteSchemaRecord(name)
}

func (m *Manager) cleanupSchemaResources(name string) error {
	instances, err := m.removedSchemaInstances(name)
	if err != nil {
		return err
	}
	for _, id := range instances {
		iMeta, err := m.lookupInstanceMetadata(id)
		if err != nil {
			return err
		}

		err = m.stopInstance(iMeta)
		if err != nil {
			return err
		}
	}

	builds, err := m.removedSchemaBuilds(name)
	if err != nil {
		return err
	}
	for _, id := range builds {
		err = m.destroyImages(id)
		if err != nil {
			return err
		}
	}

	return nil
}

// Returns the fully-nested metadata for the schema from challenges to the
// associated builds which belong to the schema through to the instances
// currently running (to include dynamic instances).
func (m *Manager) GetSchemaState(name string) ([]*ChallengeMetadata, error) {
	exists, err := m.schemaExists(name)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, unknownSchemaIdError(name)
	}
	builds, err := m.getSchemaBuilds(name)
	if err != nil {
		return nil, err
	}

	challenges := []*ChallengeMetadata{}
	var challenge *ChallengeMetadata

	for _, buildId := range builds {
		build, err := m.lookupBuildMetadata(buildId)
		if err != nil {
			return nil, err
		}

		iids, err := m.getBuildInstances(build.Id)
		if err != nil {
			return nil, err
		}

		build.Instances = make([]*InstanceMetadata, len(iids))
		for i, iid := range iids {
			instance, err := m.lookupInstanceMetadata(iid)
			if err != nil {
				return nil, err
			}

			build.Instances[i] = instance
		}

		if challenge != nil && challenge.Id != build.Challenge {
			challenges = append(challenges, challenge)
			challenge = nil
		}

		if challenge == nil {
			challenge, err = m.lookupChallengeMetadata(build.Challenge)
			if err != nil {
				return nil, err
			}
			challenge.Builds = []*BuildMetadata{}
		}

		challenge.Builds = append(challenge.Builds, build)
	}

	if challenge != nil {
		challenges = append(challenges, challenge)
	}

	return challenges, nil
}

func (m *Manager) GetChallengeMetadata(challenge ChallengeId) (*ChallengeMetadata, error) {
	return m.lookupChallengeMetadata(challenge)
}

func (m *Manager) GetBuildMetadata(build BuildId) (*BuildMetadata, error) {
	return m.lookupBuildMetadata(build)
}

func (m *Manager) GetInstanceMetadata(instance InstanceId) (*InstanceMetadata, error) {
	return m.lookupInstanceMetadata(instance)
}

func (m *Manager) DumpState(challenges []ChallengeId) ([]*ChallengeMetadata, error) {
	allChallenges, err := m.dumpState()
	if len(challenges) == 0 {
		return allChallenges, err
	}

	chalMap := make(map[ChallengeId]*ChallengeMetadata)
	results := []*ChallengeMetadata{}
	for _, challenge := range allChallenges {
		chalMap[challenge.Id] = challenge
	}

	for _, cid := range challenges {
		meta, ok := chalMap[cid]
		if !ok {
			err = fmt.Errorf("could not find challenge '%s'", cid)
			m.log.error(err)
			return nil, err
		}
		results = append(results, meta)
	}

	return results, nil
}

// Returns a byte array with the contents of the Dockerfile associated with
// `challengeType` (if it exists).  If the challenge type does not exist, then
// an empty array is returned.
func (m *Manager) GetDockerfile(challengeType string) []byte {
	dockerfile, _ := dockerfiles.Get(challengeType)
	return dockerfile
}
