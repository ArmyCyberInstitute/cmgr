package cmgr

import (
	"errors"
)

// Creates a new instance of the challenge manager validating the appropriate
// environment variables in the process.  A return value of `nil` indicates
// a fatal error occurred during intitialization.
func NewManager(logLevel LogLevel) *Manager {
	mgr := new(Manager)
	mgr.log = newLogger(logLevel)

	if err := mgr.setChallengeDirectory(); err != nil {
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

func (m *Manager) DetectChanges(fp string) *ChallengeUpdates {
	if fp == "" {
		fp = m.chalDir
	}

	cu := new(ChallengeUpdates)

	challenges, errs := m.inventoryChallenges(fp)
	db_metadata := []*ChallengeMetadata{}

	err := m.db.Select(&db_metadata, "SELECT id, checksum FROM challenges;")

	if err != nil {
		cu.Errors = append(errs, err)
		return cu
	}

	for _, curr := range db_metadata {
		newMeta, ok := challenges[curr.Id]
		if !ok {
			cu.Removed = append(cu.Removed, curr)
		} else if curr.Checksum == newMeta.Checksum {
			cu.Unmodified = append(cu.Unmodified, curr)
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

func (m *Manager) Update(fp string) *ChallengeUpdates {
	cu := m.DetectChanges(fp)
	return cu
}

func (m *Manager) Build(challenge ChallengeId, seeds []string, flagFormat string) ([]BuildId, error) {
	return nil, errors.New("not implemented")
}

func (m *Manager) Start(build BuildId) (InstanceId, error) {
	return 0, errors.New("not implemented")
}

func (m *Manager) Stop(instance InstanceId) error {
	return errors.New("not implemented")
}

func (m *Manager) Destroy(build BuildId) error {
	return errors.New("not implemented")
}

func (m *Manager) CheckInstance(instance InstanceId) (bool, error) {
	return false, errors.New("not implemented")
}

func (m *Manager) ListChallenges() []ChallengeId {
	return nil
}

func (m *Manager) GetChallengeMetadata(challenge ChallengeId) (*ChallengeMetadata, error) {
	return nil, errors.New("not implemented")
}

func (m *Manager) GetBuildMetadata(build BuildId) (*BuildMetadata, error) {
	return nil, errors.New("not implemented")
}

func (m *Manager) GetInstanceMetadata(instance InstanceId) (*InstanceMetadata, error) {
	return nil, errors.New("not implemented")
}

func (m *Manager) DumpState(challenges []ChallengeId) ([]*ChallengeMetadata, error) {
	return nil, errors.New("not implemented")
}
