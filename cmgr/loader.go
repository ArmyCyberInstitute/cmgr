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
	"strconv"
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
	dfPath := filepath.Dir(md.Path) + "/Dockerfile"
	_, err := os.Stat(dfPath)
	customDockerfile := err == nil
	m.log.debugf("Dockerfile at %s: %t", dfPath, customDockerfile)

	// Validate challenge type
	err = nil
	if md.ChallengeType == "" {
		err = fmt.Errorf("invalid challenge (%s): missing the challenge type", md.Id)
	} else if md.ChallengeType == "custom" && !customDockerfile {
		err = fmt.Errorf("invalid challenge (%s): 'custom' challenge type is missing 'Dockerfile'", md.Id)
	} else if customDockerfile {
		err = fmt.Errorf("invalid challenge (%s): 'Dockerfile' forbidden except for 'custom' challenge type", md.Id)
	} else if m.getDockerfile(md.ChallengeType) == nil {
		err = fmt.Errorf("invalid challenge (%s): unrecognized type of '%s'", md.Id, md.ChallengeType)
	}

	if err != nil {
		m.log.error(err)
		return err
	}

	var data []byte
	if md.ChallengeType == "custom" {
		f, err := os.Open(dfPath)
		if err != nil {
			m.log.errorf("could not open custom Dockerfile for (%s): %s", md.Id, err)
			return err
		}

		data, err = ioutil.ReadAll(f)
		if err != nil {
			m.log.errorf("could not read custom Dockerfile for (%s): %s", md.Id, err)
			return err
		}
	} else {
		data = m.getDockerfile(md.ChallengeType)
	}

	if data == nil || len(data) == 0 {
		err = fmt.Errorf("could not find valid Dockerfile ")
	}

	dockerfile := string(data)

	re := regexp.MustCompile(`#\s*PUBLISH\s+(\d+)\s+AS\s+(\w+)\s*`)
	matches := re.FindAllStringSubmatch(dockerfile, -1)
	m.log.debugf("found %d ports", len(matches))
	if len(matches) > 0 {
		if md.PortMap == nil {
			md.PortMap = make(map[string]int)
		}
		for _, match := range matches {
			port, err := strconv.Atoi(match[1])
			if err != nil {
				m.log.errorf("could not convert Dockerfile port to int: %s", err)
				return err
			}
			md.PortMap[match[2]] = port
		}
	}

	return err
}

// BUG(jrolli): Need to actually implement more validation.

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

		metadata.ChallengeType = "hacksport"
		metadata.Details = metadata.Description
		metadata.Description = ""
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
