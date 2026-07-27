package dockerfiles

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"strings"
)

const (
	Ubuntu24Image = "ubuntu:24.04@sha256:4fbb8e6a8395de5a7550b33509421a2bafbc0aab6c06ba2cef9ebffbc7092d90"
	Ubuntu26Image = "ubuntu:26.04@sha256:3131b4cc82a783df6c9df078f86e01819a13594b865c2cad47bd1bca2b7063bb"

	ubuntu26Suffix = "-ubuntu26"
)

func Get(challengeType string) ([]byte, error) {
	baseType, useUbuntu26 := baseChallengeType(challengeType)

	dockerfile, err := dockerfiles.ReadFile(baseType + ".Dockerfile")
	if err != nil {
		return nil, err
	}
	if !useUbuntu26 {
		return dockerfile, nil
	}

	defaultImage := []byte("FROM " + Ubuntu24Image)
	if !bytes.Contains(dockerfile, defaultImage) {
		return nil, fmt.Errorf(
			"challenge type %q does not support the Ubuntu 26.04 build environment",
			baseType,
		)
	}
	return bytes.Replace(
		dockerfile,
		defaultImage,
		[]byte("FROM "+Ubuntu26Image),
		1,
	), nil
}

type SupportFile struct {
	Name string
	Mode int64
	Data []byte
}

func SupportFiles(challengeType string) ([]SupportFile, error) {
	baseType, _ := baseChallengeType(challengeType)
	if baseType != "hacksport" {
		return nil, nil
	}

	files := []SupportFile{}
	err := fs.WalkDir(
		dockerfiles,
		"hacksport_compat",
		func(filename string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			data, err := dockerfiles.ReadFile(filename)
			if err != nil {
				return err
			}
			mode := int64(0644)
			if path.Ext(filename) == ".py" {
				mode = 0755
			}
			files = append(files, SupportFile{
				Name: path.Join(".cmgr", filename),
				Mode: mode,
				Data: data,
			})
			return nil
		},
	)
	return files, err
}

func baseChallengeType(challengeType string) (string, bool) {
	useUbuntu26 := strings.HasSuffix(challengeType, ubuntu26Suffix)
	if useUbuntu26 {
		return strings.TrimSuffix(challengeType, ubuntu26Suffix), true
	}
	return challengeType, false
}

//go:generate python3 ../../support/generate_pybuild_dockerfiles.py
//go:embed *.Dockerfile hacksport_compat
var dockerfiles embed.FS
