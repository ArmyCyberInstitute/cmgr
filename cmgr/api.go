package cmgr

import (
	"fmt"
)

// Creates a new instance of the challenge manager validating the appropriate
// environment variables in the process.  A return value of `nil` indicates
// a fatal error occurred during intitialization.
func NewManager(logLevel LogLevel) *Manager {
	mgr := new(Manager)
	mgr.log = newLogger(logLevel)

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

// Templates out a "challenge" and generates concrete images, flags, and
// lookup values for the seeds provided which is called a "build" and returns
// a list of identifiers that can be used to reference the build in other API
// functions.  This function may take a significant amount of time because it
// will implicitly download base docker images and build the artifacts.
func (m *Manager) Build(challenge ChallengeId, seeds []int, flagFormat string) ([]BuildId, error) {
	return m.buildImages(challenge, seeds, flagFormat)
}

// Creates a running "instance" of the given build and returns its identifier
// on success otherwise an error.
func (m *Manager) Start(build BuildId) (InstanceId, error) {
	return m.startContainers(build)
}

// Stops the running "instance".
func (m *Manager) Stop(instance InstanceId) error {
	return m.stopContainers(instance)
}

// Destroys the assoicated "build".
func (m *Manager) Destroy(build BuildId) error {
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
