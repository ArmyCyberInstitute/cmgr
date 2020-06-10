package cmgr

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
)

func (m *Manager) loadChallenge(path string, info os.FileInfo) (*ChallengeMetadata, error) {
	// Screen out non-problem files
	if info.Name() == "problem.json" {
		return m.loadJsonChallenge(path, info)
	}

	return nil, nil
}

type hacksportAttrs struct {
	Author       string `json:"author"`
	Event        string `json:"event"`
	Organization string `json:"organization"`
	Version      string `json:"version"`
}

func (m *Manager) loadJsonChallenge(path string, info os.FileInfo) (*ChallengeMetadata, error) {
	m.log.debugf("Found challenge JSON at %s", path)

	// Validate the file, and record the identifier
	data, err := ioutil.ReadFile(path)
	if err != nil {
		m.log.errorf("could not read challenge file: %s", err)
		return nil, err
	}

	// Unmarshal the JSON file
	metadata := new(ChallengeMetadata)
	err = json.Unmarshal(data, metadata)
	if err != nil {
		m.log.errorf("could not unmarshal challenge file: %s", err)
		return nil, err
	}

	// Require a challenge name
	if metadata.Name == "" {
		err := fmt.Errorf("challenge file missing name: %s", path)
		m.log.error(err)
		return nil, err
	}

	// Validate Namespace
	re := regexp.MustCompile(`^([a-zA-Z0-9](/[a-zA-Z0-9])*)?$`)
	if !re.MatchString(metadata.Namespace) {
		err := fmt.Errorf("invalid namespace (limited to ASCII alphanumeric + '/'): %s",
			metadata.Namespace)
		m.log.error(err)
		return nil, err
	}

	prefix := ""
	if metadata.Namespace != "" {
		prefix = metadata.Namespace + "/"
	}

	// Indicates that this is a legacy hacksport challenge that needs lifting
	if metadata.ChallengeType == "" {
		_, err := os.Stat(filepath.Join(filepath.Dir(path), "challenge.py"))
		if err != nil {
			err := fmt.Errorf("could not stat 'challenge.py' on implicit hacksport challenge: %s", path)
			m.log.error(err)
			return nil, err
		}

		var attrs hacksportAttrs
		err = json.Unmarshal(data, &attrs)
		if err != nil {
			m.log.error(err)
			return nil, err
		}

		metadata.Details = metadata.Description
		metadata.Description = ""
		metadata.Templatable = true
		metadata.SolveScript = false

		metadata.Attributes = make(map[string]string)
		if attrs.Author != "" {
			metadata.Attributes["author"] = attrs.Author
		}

		if attrs.Event != "" {
			metadata.Attributes["event"] = attrs.Event
		}

		if attrs.Author != "" {
			metadata.Attributes["organization"] = attrs.Organization
		}

		if attrs.Version != "" {
			metadata.Attributes["version"] = attrs.Version
		}
	}

	metadata.Id = ChallengeId(prefix + sanitizeName(metadata.Name))
	return metadata, nil
}
