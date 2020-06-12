package cmgr

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
)

func (m *Manager) loadChallenge(path string, info os.FileInfo) (*ChallengeMetadata, error) {
	var md *ChallengeMetadata
	var err error

	// Screen out non-problem files
	if info.Name() == "problem.json" {
		md, err = m.loadJsonChallenge(path, info)
	} else if info.Name() == "problem.md" {
		err = errors.New("'problem.md' not supported yet")
	}

	if err == nil && md != nil {
		err = m.validate(md)
	}

	return md, err
}

// Validates the challenge metadata for compliance with expectations
func (m *Manager) validate(md *ChallengeMetadata) error {
	return nil
}

// BUG(jrolli): Need to actually implement the validation.

type hacksportAttrs struct {
	Author       string `json:"author"`
	Event        string `json:"event"`
	Organization string `json:"organization"`
	Version      string `json:"version"`
}

// Loads the JSON information using the built-in encoding format.  This works
// but results in a less-than-desireable end-user experience because of opaque
// error codes.  It may be worth implementing a custom implementation that
// leverages the decoder iteratively in order to manually provide more useful
// debug information to challenge authors.  This would also allow us to avoid
// the double-pass to catch unknown attributes.
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

	h := crc32.NewIEEE()
	_, err = h.Write(append(data, []byte(path)...))
	if err != nil {
		return nil, err
	}
	metadata.MetadataChecksum = h.Sum32()

	metadata.Id = ChallengeId(prefix + sanitizeName(metadata.Name))
	return metadata, nil
}
