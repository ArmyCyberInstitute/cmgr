package dockerfiles

import (
	"embed"
)

func Get(challengeType string) ([]byte, error) {
	return dockerfiles.ReadFile(challengeType + ".Dockerfile")
}

//go:generate python3 ../../support/generate_pybuild_dockerfiles.py
//go:embed *.Dockerfile
var dockerfiles embed.FS
