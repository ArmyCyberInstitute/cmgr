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
	"strings"
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

	if err != nil || md == nil {
		return md, err
	}

	prefix := ""
	if md.Namespace != "" {
		prefix = md.Namespace + "/"
	}
	md.Id = ChallengeId(prefix + sanitizeName(md.Name))
	md.Path = filepath.Dir(path)

	solverPath := filepath.Join(md.Path, "solver")
	info, err = os.Stat(solverPath)
	md.SolveScript = err == nil && info.IsDir()

	err = m.processDockerfile(md)
	if err != nil {
		return md, err
	}

	err = m.validateMetadata(md)

	return md, err
}

var templateRe *regexp.Regexp = regexp.MustCompile(`\{\{[^}]*\}\}`)

func (m *Manager) validateMetadata(md *ChallengeMetadata) error {
	var lastErr error
	// Require a challenge name
	if md.Name == "" {
		lastErr = fmt.Errorf("challenge file missing name: %s", md.Path)
		m.log.error(lastErr)
	}

	// Validate Namespace
	re := regexp.MustCompile(`^([a-zA-Z0-9]+(/[a-zA-Z0-9]+)*)?$`)
	if !re.MatchString(md.Namespace) {
		lastErr = fmt.Errorf("invalid namespace (limited to ASCII alphanumeric + '/') of '%s': %s",
			md.Namespace, md.Path)
		m.log.error(lastErr)
	}

	// Validate Description
	templates := templateRe.FindAllString(md.Description, -1)
	if len(templates) > 0 {
		lastErr = fmt.Errorf("template strings not allowed in the 'description' field, but found: %s", strings.Join(templates, ", "))
		m.log.error(lastErr)
	}

	// Validate (& lift) Details
	res, err := m.normalizeAndCheckTemplated(md, md.Details)
	if err != nil {
		lastErr = err
	}
	md.Details = res

	// Validate (& lift) Hints
	for i, hint := range md.Hints {
		res, err = m.normalizeAndCheckTemplated(md, hint)
		if err != nil {
			lastErr = err
		}
		md.Hints[i] = res
	}

	return lastErr
}

const filenamePattern string = "[a-zA-Z0-9_.-]+"
const displayTextPattern string = `[^<>'"]+`
const urlPathPattern string = `[a-zA-Z0-9_%/=?+&#!.,-]+`

// {{url("file")}}
const urlRePattern string = `\{\{\s*url\(["'](` + filenamePattern + `)["']\)\s*\}\}`

var urlRe *regexp.Regexp = regexp.MustCompile(urlRePattern)

// {{url_for("file", "display text")}}
const urlForRePattern string = `\{\{\s*url_for\(["'](` + filenamePattern + `)["'],\s*["'](` + displayTextPattern + `)["']\)\s*\}\}`

var urlForRe *regexp.Regexp = regexp.MustCompile(urlForRePattern)

// This exists entirely for backwards compatability with hacksport
// {{url_for("file")}}
const shortUrlForRePattern string = `\{\{\s*url_for\(["'](` + filenamePattern + `)["']\)\s*\}\}`

var shortUrlForRe *regexp.Regexp = regexp.MustCompile(shortUrlForRePattern)

// {{http_base("port_name")}}
var httpBaseRe *regexp.Regexp = regexp.MustCompile(`\{\{\s*http_base\(["'](\w+)["']\)\s*\}\}`)

// {{http_base}}
var shortHttpBaseRe *regexp.Regexp = regexp.MustCompile(`\{\{\s*http_base\s*\}\}`)

// {{port("port_name")}}
var portRe *regexp.Regexp = regexp.MustCompile(`\{\{\s*port\(["'](\w+)["']\)\s*\}\}`)

// {{port}}
var shortPortRe *regexp.Regexp = regexp.MustCompile(`\{\{\s*port\s*\}\}`)

// {{server("port_name")}}
var serverRe *regexp.Regexp = regexp.MustCompile(`\{\{\s*server\(["'](\w+)["']\)\s*\}\}`)

// {{server}}
var shortServerRe *regexp.Regexp = regexp.MustCompile(`\{\{\s*server\s*\}\}`)

// {{lookup("key")}}
var lookupRe *regexp.Regexp = regexp.MustCompile(`\{\{\s*lookup\(["'](\w+)["']\)\s*\}\}`)

// {{link("port_name", "/url/in/challenge")}}
const linkRePattern string = `\{\{\s*link\(["'](\w+)["'],\s*["'](` + urlPathPattern + `)["']\)\s*\}\}`

var linkRe *regexp.Regexp = regexp.MustCompile(linkRePattern)

// {{link("/url/in/challenge")}}
const shortLinkRePattern string = `\{\{\s*link\(["'](` + urlPathPattern + `)["']\)\s*\}\}`

var shortLinkRe *regexp.Regexp = regexp.MustCompile(shortLinkRePattern)

// {{link_as("port_name", "/url/in/challenge", "display text")}}
const linkAsRePattern string = `\{\{\s*link_as\(["'](\w+)["'],\s*["'](` + urlPathPattern + `)["'],\s*["'](` + displayTextPattern + `)["']\s*\)\}\}`

var linkAsRe *regexp.Regexp = regexp.MustCompile(linkAsRePattern)

// {{link_as("/url/in/challenge", "display text")}}
const shortLinkAsRePattern string = `\{\{\s*link_as\(["'](` + urlPathPattern + `)["'],\s*["'](` + displayTextPattern + `)["']\s*\)\}\}`

var shortLinkAsRe *regexp.Regexp = regexp.MustCompile(shortLinkAsRePattern)

func (m *Manager) normalizeAndCheckTemplated(md *ChallengeMetadata, s string) (string, error) {
	onePort := len(md.PortMap) == 1
	// Insert port elision into the string
	var portName string
	// refPort := make(map[string]bool)
	for k := range md.PortMap {
		// Only executes once because of above check
		portName = k
		// refPort[k] = false
	}
	var r string
	m.log.debug(md.PortMap)
	m.log.debug(s)
	if onePort {

		r = `{{url_for("$1", "$1")}}`
		s = shortUrlForRe.ReplaceAllString(s, r)

		r = fmt.Sprintf(`{{http_base("%s")}}`, portName)
		s = shortHttpBaseRe.ReplaceAllString(s, r)

		r = fmt.Sprintf(`{{port("%s")}}`, portName)
		s = shortPortRe.ReplaceAllString(s, r)

		r = fmt.Sprintf(`{{server("%s")}}`, portName)
		s = shortServerRe.ReplaceAllString(s, r)

		r = fmt.Sprintf(`{{link("%s", "${1}")}}`, portName)
		s = shortLinkRe.ReplaceAllString(s, r)

		r = fmt.Sprintf(`{{link_as("%s", "${1}", "${2}")}}`, portName)
		s = shortLinkAsRe.ReplaceAllString(s, r)
	}
	m.log.debug(s)

	r = `<a href='{{url("${1}")}}'>${2}</a>`
	s = urlForRe.ReplaceAllString(s, r)

	r = `<a href='{{http_base("${1}")}}:{{port("${1}")}}/${2}' target='_blank'>${2}</a>`
	s = linkRe.ReplaceAllString(s, r)

	r = `<a href='{{http_base("${1}")}}:{{port("${1}")}}/${2}' target='_blank'>${3}</a>`
	s = linkAsRe.ReplaceAllString(s, r)

	var err error
	templates := templateRe.FindAllString(s, -1)
	for _, tmpl := range templates {
		if urlRe.MatchString(tmpl) || lookupRe.MatchString(tmpl) {
			continue
		}

		res := httpBaseRe.FindStringSubmatch(tmpl)
		if res == nil {
			res = portRe.FindStringSubmatch(tmpl)
		}
		if res == nil {
			res = serverRe.FindStringSubmatch(tmpl)
		}

		if res == nil || len(res) < 2 || len(md.PortMap) == 0 {
			err = fmt.Errorf("unrecognized template string of '%s': %s", tmpl, md.Path)
			m.log.error(err)
			continue
		}

		if _, ok := md.PortMap[res[1]]; !ok {
			err = fmt.Errorf("unrecognized port in template '%s': %s", tmpl, md.Path)
			m.log.error(err)
			continue
		}
	}

	return s, err
}

// Validates the challenge metadata for compliance with expectations
func (m *Manager) processDockerfile(md *ChallengeMetadata) error {
	dfPath := filepath.Join(md.Path, "Dockerfile")
	_, err := os.Stat(dfPath)
	customDockerfile := err == nil
	m.log.debugf("Dockerfile at %s: %t", dfPath, customDockerfile)

	// Validate challenge type
	err = nil
	if md.ChallengeType == "" {
		err = fmt.Errorf("invalid challenge (%s): missing the challenge type", md.Id)
	} else if md.ChallengeType == "custom" {
		if !customDockerfile {
			err = fmt.Errorf("invalid challenge (%s): 'custom' challenge type is missing 'Dockerfile'", md.Id)
		}
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

// BUG(jrolli): Need to actually implement more validation such as verifying
// that published ports are referenced and that there are no clearly invalid
// format strings in the details and hints.

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
		metadata.Namespace = "hacksport"
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

	return metadata, nil
}
