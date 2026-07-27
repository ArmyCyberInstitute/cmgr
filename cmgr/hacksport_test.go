package cmgr

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestExplicitHacksportChallengeFormatParity(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		contents string
	}{
		{
			name:     "JSON",
			filename: "problem.json",
			contents: `{
				"name": "Explicit Hacksport",
				"namespace": "tests",
				"challenge_type": "hacksport",
				"description": "",
				"details": "Connect to {{server}}:{{port}}.",
				"hints": [],
				"challenge_options": {
					"seccomp": {
						"tweaks": ["allow-disable-aslr"]
					}
				}
			}`,
		},
		{
			name:     "Markdown",
			filename: "problem.md",
			contents: `# Explicit Hacksport

- Namespace: tests
- Type: hacksport

## Description

Compatibility test.

## Details

Connect to {{server}}:{{port}}.

## Challenge Options

` + "```yaml\n" + `seccomp:
    tweaks:
        - allow-disable-aslr
` + "```\n",
		},
	}

	var loaded []*ChallengeMetadata
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory, err := ioutil.TempDir("", "cmgr-hacksport-format")
			if err != nil {
				t.Fatalf("could not create challenge directory: %s", err)
			}
			defer os.RemoveAll(directory)

			if err = ioutil.WriteFile(
				filepath.Join(directory, "challenge.py"),
				[]byte("from hacksport.problem import Remote\n"),
				0600,
			); err != nil {
				t.Fatalf("could not write challenge.py: %s", err)
			}
			path := filepath.Join(directory, test.filename)
			if err = ioutil.WriteFile(
				path,
				[]byte(test.contents),
				0600,
			); err != nil {
				t.Fatalf("could not write challenge metadata: %s", err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("could not inspect challenge metadata: %s", err)
			}

			manager := &Manager{log: newLogger(DISABLED)}
			metadata, err := manager.loadChallenge(path, info)
			if err != nil {
				t.Fatalf("could not load explicit hacksport challenge: %s", err)
			}
			if metadata.ChallengeType != "hacksport" {
				t.Fatalf(
					"unexpected challenge type %q",
					metadata.ChallengeType,
				)
			}
			if metadata.ChallengeOptions.Seccomp == nil {
				t.Fatal("seccomp options were not decoded")
			}
			loaded = append(loaded, metadata)
		})
	}

	if !reflect.DeepEqual(
		loaded[0].ChallengeOptions,
		loaded[1].ChallengeOptions,
	) {
		t.Fatalf(
			"hacksport options differ by challenge format:\nJSON: %#v\nMarkdown: %#v",
			loaded[0].ChallengeOptions,
			loaded[1].ChallengeOptions,
		)
	}
}

func TestHacksportLegacyDriverRemainsExplicit(t *testing.T) {
	manager := &Manager{log: newLogger(DISABLED)}
	if len(manager.GetDockerfile("hacksport-legacy")) == 0 {
		t.Fatal("legacy hacksport Dockerfile is unavailable")
	}
	if isHacksportChallengeType("") {
		t.Fatal("missing challenge type was treated as an explicit driver")
	}
	if !isHacksportChallengeType("hacksport-legacy") {
		t.Fatal("legacy hacksport type was not recognized")
	}
}
