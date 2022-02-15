package cmgr

import (
	"errors"
	"fmt"
	"math/rand"
	"time"

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
	mgr.rand = rand.New(rand.NewSource(time.Now().UnixNano()))

	mgr.log.infof("version: %s", Version())

	if err := mgr.setDirectories(); err != nil {
		return nil
	}

	if err := mgr.initDocker(); err != nil {
		return nil
	}

	if err := mgr.initDatabase(); err != nil {
		return nil
	}

	return mgr
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

		sourceChanged := curr.SourceChecksum != newMeta.SourceChecksum
		metadataChanged := curr.MetadataChecksum != newMeta.MetadataChecksum
		solvescriptChanged := curr.SolveScript != newMeta.SolveScript
		if !sourceChanged && !metadataChanged && !solvescriptChanged {
			cu.Unmodified = append(cu.Unmodified, curr)
		} else if !sourceChanged && m.safeToRefresh(newMeta) {
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

	cu.Errors = errs
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
	errs := m.addChallenges(cu.Added)
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
	schema := fmt.Sprintf("%s%x", manualSchemaPrefix, m.rand.Int63())
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
	err := m.generateBuilds(builds)
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
		return 0, errors.New("locked build: change the schema definition to start more instances")
	}

	return m.newInstance(bMeta)
}

func (m *Manager) newInstance(build *BuildMetadata) (InstanceId, error) {
	iMeta := &InstanceMetadata{
		Build:      build.Id,
		Ports:      make(map[string]int),
		Containers: []string{},
	}
	err := m.openInstance(iMeta)
	if err != nil {
		return 0, err
	}

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
		// It is possible we are in a partially deployed state.  Make sure
		// we are torn down, but ignore the returned error.
		m.stopInstance(iMeta)
	}

	return iMeta.Id, err
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
		return errors.New("locked build: change the schema definition to stop this instance")
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

	if bMeta.Schema[:len(manualSchemaPrefix)] != manualSchemaPrefix {
		return errors.New("locked build: change the schema definition to destroy this build")
	}

	return m.destroyImages(build)
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

// Obtains a list of challenges which match on all of the given tags.  If no
// tags are passed, then it returns the same results as `ListChallenges`.
// Wildcards are allowed as either '*' or '%' and the search is ASCII case
// insensitive.
func (m *Manager) SearchChallenges(tags []string) []*ChallengeMetadata {
	md, _ := m.searchChallenges(tags)
	return md
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
	exists, err := m.schemaExists(schema.Name)
	if err != nil {
		return []error{err}
	} else if exists {
		return []error{fmt.Errorf("schema '%s' already exists", schema.Name)}
	}

	return m.convergeSchema(schema)
}

// Updates the definition of the schema internally and then converges to the
// new definition.  Certain updates are more expensive than others.  In
// particular, updating the flag format will cause a complete rebuild of the
// state.
func (m *Manager) UpdateSchema(schema *Schema) []error {
	exists, err := m.schemaExists(schema.Name)
	if err != nil {
		return []error{err}
	} else if !exists {
		return []error{fmt.Errorf("schema '%s' does not exist", schema.Name)}
	}

	return m.convergeSchema(schema)
}

func (m *Manager) convergeSchema(schema *Schema) []error {
	// Mark existing state as locked/outdated
	err := m.lockSchema(schema.Name)
	if err != nil {
		return []error{err}
	}

	// Update builds to reflect request
	state := make([][]*BuildMetadata, 0, len(schema.Challenges))
	errs := []error{}
	for challenge, spec := range schema.Challenges {
		builds := make([]*BuildMetadata, len(spec.Seeds))
		for i, seed := range spec.Seeds {
			builds[i] = &BuildMetadata{
				Seed:          seed,
				Format:        schema.FlagFormat,
				Challenge:     challenge,
				Schema:        schema.Name,
				InstanceCount: spec.InstanceCount,
			}

			err := m.openBuild(builds[i])
			if err != nil {
				errs = append(errs, err)
				continue
			}
		}
		state = append(state, builds)
	}

	// Release obsolete builds
	err = m.cleanupSchemaResources(schema.Name)
	if err != nil {
		errs = append(errs, err)
	}

	// Create missing builds and converge instances
	for _, builds := range state {
		err := m.generateBuilds(builds)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		for _, build := range builds {
			target := schema.Challenges[build.Challenge].InstanceCount
			if target == DYNAMIC_INSTANCES || target == LOCKED {
				continue
			}

			instances, err := m.getBuildInstances(build.Id)
			m.log.debugf("converging %s/%d: %d found, need %d", build.Challenge, build.Id, len(instances), target)
			for i := target; i < len(instances); i++ {
				iMeta, err := m.lookupInstanceMetadata(instances[i])
				if err != nil {
					errs = append(errs, err)
					continue
				}

				err = m.stopInstance(iMeta)
				if err != nil {
					errs = append(errs, err)
				}
			}

			for i := len(instances); i < target; i++ {
				if len(build.Images) == 0 {
					// Lazy lookup for case where we resized
					build, err = m.lookupBuildMetadata(build.Id)
					if err != nil {
						errs = append(errs, err)
						break
					}
				}
				_, err = m.newInstance(build)
				if err != nil {
					errs = append(errs, err)
					break
				}
			}

		}
	}

	return errs
}

// Tears down all instances and builds belonging to the schema.
func (m *Manager) DeleteSchema(name string) error {
	err := m.lockSchema(name)
	if err != nil {
		return err
	}

	return m.cleanupSchemaResources(name)
}

func (m *Manager) cleanupSchemaResources(name string) error {
	instances, err := m.removedSchemaInstances(name)
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
