package cmgr

import (
	"bytes"
	"fmt"
	"hash/crc32"
	"io/ioutil"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	"gopkg.in/yaml.v2"
)

func parseBool(s string) (bool, error) {
	s = strings.ToLower(s)
	switch s {
	case "yes":
		fallthrough
	case "true":
		fallthrough
	case "1":
		fallthrough
	case "t":
		fallthrough
	case "y":
		return true, nil
	case "no":
		fallthrough
	case "false":
		fallthrough
	case "0":
		fallthrough
	case "f":
		fallthrough
	case "n":
		return false, nil
	default:
		return false, fmt.Errorf("cannot interpret '%s' as boolean", s)
	}
}

var sectionRe *regexp.Regexp = regexp.MustCompile(`^##\s*(.+)`)
var kvLineRe *regexp.Regexp = regexp.MustCompile(`^\s*-\s*(\w+):\s*(.*)`)
var tagLineRe *regexp.Regexp = regexp.MustCompile(`^\s*-\s*(\w+)\s*$`)
var optionLineRe *regexp.Regexp = regexp.MustCompile(`^\s*-\s*([\w=]+)\s*$`)

func (m *Manager) loadMarkdownChallenge(path string, info os.FileInfo) (*ChallengeMetadata, error) {
	m.log.debugf("Found challenge Markdown at %s", path)

	// Validate the file, and record the identifier
	data, err := ioutil.ReadFile(path)
	if err != nil {
		m.log.errorf("could not read challenge file: %s", err)
		return nil, err
	}

	md := new(ChallengeMetadata)
	md.Path = path
	md.Attributes = make(map[string]string)

	lines := strings.Split(string(data), "\n")
	idx := 0
	var line string

	// Find the name line
	nameRe := regexp.MustCompile(`^#\s*(.+)`)
	for idx < len(lines) {
		line = strings.TrimSpace(lines[idx])
		match := nameRe.FindStringSubmatch(line)
		idx++
		if match != nil {
			md.Name = match[1]
			break
		}
	}

	// Read the top-level metadata
	for idx < len(lines) {
		line = strings.TrimSpace(lines[idx])
		if sectionRe.MatchString(line) {
			break
		}

		match := kvLineRe.FindStringSubmatch(line)
		idx++

		if len(line) == 0 {
			continue
		}

		if match == nil {
			err = fmt.Errorf("unrecognized metadata text on line %d: %s", idx, path)
			m.log.error(err)
			continue
		}

		switch strings.ToLower(match[1]) {
		case "id":
			md.Id = ChallengeId(match[2])
		case "namespace":
			md.Namespace = match[2]
		case "type":
			md.ChallengeType = match[2]
		case "category":
			md.Category = match[2]
		case "templatable":
			val, tmpErr := parseBool(match[2])
			md.Templatable = val
			err = tmpErr
		case "points":
			i, tmpErr := strconv.Atoi(match[2])
			md.Points = i
			err = tmpErr
		case "maxusers":
			i, tmpErr := strconv.Atoi(match[2])
			md.MaxUsers = i
			err = tmpErr
		default:
			err = fmt.Errorf("unrecognized top-level attribute '%s' on line %d: %s", match[1], idx, path)
			m.log.error(err)
		}
	}

	section := ""
	startIdx := 0
	for idx < len(lines) {
		line = strings.TrimSpace(lines[idx])
		match := sectionRe.FindStringSubmatch(line)
		if match != nil && section != "" {
			err = m.processMarkdownSection(md, section, lines, startIdx, idx)
		}
		if match != nil {
			section = match[1]
			startIdx = idx + 1
		}
		idx++
	}

	if section != "" {
		err = m.processMarkdownSection(md, section, lines, startIdx, idx)
	}

	h := crc32.NewIEEE()
	_, err = h.Write(append(data, []byte(path)...))
	if err != nil {
		return nil, err
	}
	md.MetadataChecksum = h.Sum32()

	return md, nil
}

func (m *Manager) processMarkdownSection(md *ChallengeMetadata, section string, lines []string, startIdx, endIdx int) error {
	var err error
	m.log.debugf("processing markdown: section='%s' start=%d end=%d", section, startIdx, endIdx)
	sectionLower := strings.ToLower(section)
	switch {
	case sectionLower == "description":
		text, tmpErr := m.parseMarkdown(strings.Join(lines[startIdx:endIdx], "\n"))
		md.Description = text
		err = tmpErr
	case sectionLower == "details":
		text, tmpErr := m.parseMarkdown(strings.Join(lines[startIdx:endIdx], "\n"))
		md.Details = text
		err = tmpErr
	case sectionLower == "hints":
		hints, tmpErr := m.parseHints(lines[startIdx:endIdx])
		md.Hints = hints
		err = tmpErr
	case sectionLower == "tags":
		md.Tags = []string{}
		for i, rawLine := range lines[startIdx:endIdx] {
			line := strings.TrimSpace(rawLine)
			if line == "" {
				continue
			}

			match := tagLineRe.FindStringSubmatch(line)
			if match == nil {
				err = fmt.Errorf("unexpected text in 'tags' section on line %d: %s", i+1, md.Path)
				m.log.error(err)
				continue
			}

			md.Tags = append(md.Tags, match[1])
		}
	case sectionLower == "attributes":
		for i := startIdx; i < endIdx; i++ {
			line := strings.TrimSpace(lines[i])
			if line == "" {
				continue
			}

			match := kvLineRe.FindStringSubmatch(line)
			if match == nil {
				err = fmt.Errorf("unexpected text in 'attributes' section on line %d: %s", i+1, md.Path)
				m.log.error(err)
				continue
			}

			md.Attributes[match[1]] = match[2]
		}
	case strings.HasPrefix(sectionLower, "challenge options"):
		yamlStart := 0
		yamlEnd := 0
		for i := startIdx; i < endIdx; i++ {
			if lines[i] == "```yaml" {
				if yamlStart != 0 {
					err = fmt.Errorf("found multiple start markers for yaml at lines %d and %d", yamlStart-1, i)
					m.log.error(err)
				}
				yamlStart = i + 1
			} else if lines[i] == "```" {
				if yamlEnd != 0 {
					err = fmt.Errorf("found multiple end markers for yaml at lines %d and %d", yamlEnd, i)
					m.log.error(err)
				}
				yamlEnd = i
			}
		}

		if yamlStart == 0 && yamlEnd == 0 {
			m.log.debug("addining implicit delimiters for challenge options")
			yamlStart = startIdx
			yamlEnd = endIdx
		} else if (yamlStart == 0) != (yamlEnd == 0) {
			err = fmt.Errorf("found a start/end marker but missing its pair: startline=%d endline=%d", yamlStart, yamlEnd)
			m.log.error(err)
			yamlStart = 0
			yamlEnd = 0
		}

		opts := ChallengeOptions{}
		yamlData := []byte(strings.Join(lines[yamlStart:yamlEnd], "\n"))
		err = yaml.Unmarshal(yamlData, &opts)
		if err != nil {
			m.log.error(err)
		}

		md.ChallengeOptions = opts

	default:
		attrVal := strings.TrimSpace(strings.Join(lines[startIdx:endIdx], "\n"))
		md.Attributes[section] = attrVal
	}
	return err
}

var lineStartRe *regexp.Regexp = regexp.MustCompile(`^    |^\t`)

func (m *Manager) parseHints(lines []string) ([]string, error) {
	hints := []string{}
	hintLines := []string{}
	var err error
	for _, rawLine := range lines {
		if len(rawLine) > 0 && rawLine[0] == '-' {
			if len(hintLines) > 0 {
				hint, tmpErr := m.parseMarkdown(strings.Join(hintLines, "\n"))
				if tmpErr != nil {
					err = tmpErr
				}
				hint = strings.TrimSpace(hint)
				if hint != "" {
					hints = append(hints, hint)
				}
			}
			hintLines = []string{strings.TrimSpace(rawLine[1:])}
		} else {
			hintLines = append(hintLines, lineStartRe.ReplaceAllString(rawLine, ""))
		}
	}
	if len(hintLines) > 0 {
		hint, tmpErr := m.parseMarkdown(strings.Join(hintLines, "\n"))
		if tmpErr != nil {
			err = tmpErr
		}
		hint = strings.TrimSpace(hint)
		if hint != "" {
			hints = append(hints, hint)
		}
	}
	return hints, err
}

func (m *Manager) parseMarkdown(text string) (string, error) {
	var buff bytes.Buffer
	err := goldmark.Convert([]byte(text), &buff)
	if err != nil {
		return "", err
	}

	data, err := ioutil.ReadAll(&buff)
	section := strings.TrimSpace(string(data))
	templates := templateRe.FindAllStringIndex(section, -1)
	for i := range templates {
		pair := templates[len(templates)-(i+1)]
		start, stop := pair[0], pair[1]
		m.log.debugf("found template in range [%d, %d] (len(string) = %d", start, stop, len(section))
		section = section[:start] +
			strings.ReplaceAll(section[start:stop], "&quot;", `"`) +
			section[stop:]
	}
	return section, err
}
