package cmgr

import (
	"context"
	"github.com/docker/docker/client"
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

	return nil
}
