package cmgr

import (
	"embed"
)

func (m *Manager) getDockerfile(challengeType string) []byte {
	if challengeType == "custom" {
		return nil
	}

	data, _ := dockerfiles.ReadFile("dockerfiles/" + challengeType + ".Dockerfile")

	return data
}

//go:generate python3 ../support/generate_pybuild_dockerfiles.py
//go:embed dockerfiles/*.Dockerfile
var dockerfiles embed.FS
