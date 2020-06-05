package cmgr

import (
	"context"
	"errors"

	"github.com/docker/docker/client"
)

func NewManager(logLevel LogLevel) *Manager {
	cli, err := client.NewEnvClient()
	if err != nil {
		return nil
	}

	mgr := new(Manager)
	mgr.client = cli
	mgr.ctx = context.Background()
	mgr.log = newLogger(logLevel)

	ping, err := cli.Ping(mgr.ctx)
	if err != nil {
		mgr.log.error(err)
		return nil
	}

	mgr.log.infof("connected to docker (API v%s)", ping.APIVersion)
	return mgr
}

func (m *Manager) DetectChanges(filepath string) ([]int, error) {
	return nil, errors.New("not implemented")
}

func (m *Manager) Update(filepath string) ([]int, error) {
	return nil, errors.New("not implemented")
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
