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
				err = fmt.Errorf("unexpected text in 'tags' section on line %d: %s", i, md.Path)
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
				err = fmt.Errorf("unexpected text in 'attributes' section on line %d: %s", i, md.Path)
				m.log.error(err)
				continue
			}

			md.Attributes[match[1]] = match[2]
		}
	case sectionLower == "network options":
		for i := startIdx; i < endIdx; i++ {
			line := strings.TrimSpace(lines[i])
			if line == "" {
				continue
			}

			match := kvLineRe.FindStringSubmatch(line)
			if match == nil {
				err = fmt.Errorf("unexpected text in 'network options' section on line %d: %s", i, md.Path)
				m.log.error(err)
				continue
			}

			option := strings.ToLower(match[1])
			switch option {
			case "internal":
				value, err := parseBool(match[2])
				if err != nil {
					err = fmt.Errorf("unable to parse 'internal' option value on line %d: %s", i, md.Path)
					m.log.error(err)
					continue
				}
				md.NetworkOptions.Internal = value
				continue
			default:
				err = fmt.Errorf("unexpected option '%s' in 'network options' section on line %d: %s", option, i, md.Path)
				continue
			}
		}
	case strings.HasPrefix(sectionLower, "container options"):
		host := ""
		sectionParts := strings.SplitN(sectionLower, ":", 2)
		if len(sectionParts) == 2 {
			host = strings.TrimSpace(sectionParts[1])
		}
		opts := ContainerOptions{}
		for i := startIdx; i < endIdx; i++ {
			line := strings.TrimSpace(lines[i])
			if line == "" {
				continue
			}
			match := kvLineRe.FindStringSubmatch(line)
			if match == nil {
				err = fmt.Errorf("unexpected text in 'container options' section on line %d: %s", i, md.Path)
				m.log.error(err)
				continue
			}
			key := strings.ToLower(match[1])
			var value string
			if len(match) == 3 {
				value = strings.TrimSpace(match[2])
			}
			switch key {
			case "init":
				if value == "" {
					err = fmt.Errorf("missing value for 'init' option on line %d: %s", i, md.Path)
					m.log.error(err)
					continue
				}
				value, err := parseBool(value)
				if err != nil {
					err = fmt.Errorf("failed to parse 'init' option value as bool on line %d: %s", i, md.Path)
					m.log.error(err)
					continue
				}
				opts.Init = value
				continue
			case "cpus":
				if value == "" {
					err = fmt.Errorf("missing value for 'cpus' option on line %d: %s", i, md.Path)
					m.log.error(err)
					continue
				}
				opts.Cpus = value
				continue
			case "memory":
				if value == "" {
					err = fmt.Errorf("missing value for 'memory' option on line %d: %s", i, md.Path)
					m.log.error(err)
					continue
				}
				opts.Memory = value
				continue
			case "ulimits":
				if value != "" {
					err = fmt.Errorf("inline value provided for 'ulimits' option on line %d (use unordered list): %s", i, md.Path)
					m.log.error(err)
					continue
				}
				for i < endIdx-1 {
					i++
					valLine := strings.TrimSpace(lines[i])
					if valLine == "" {
						continue
					}
					valMatch := kvLineRe.FindStringSubmatch(valLine)
					if valMatch != nil {
						// Found next option line, rewind and break
						i--
						break
					}
					valMatch = optionLineRe.FindStringSubmatch(valLine)
					if valMatch == nil {
						err = fmt.Errorf("invalid value provided for 'ulimits' option on line %d: %s", i, md.Path)
						m.log.error(err)
						continue
					}
					opts.Ulimits = append(opts.Ulimits, valMatch[1])
				}
				continue
			case "pidslimit":
				if value == "" {
					err = fmt.Errorf("missing value for 'pidslimit' option on line %d: %s", i, md.Path)
					m.log.error(err)
					continue
				}
				value, err := strconv.ParseInt(value, 10, 64)
				if err != nil {
					err = fmt.Errorf("failed to parse 'pidslimit' option value as int64 on line %d: %s", i, md.Path)
					m.log.error(err)
					continue
				}
				opts.PidsLimit = value
				continue
			case "readonlyrootfs":
				if value == "" {
					err = fmt.Errorf("missing value for 'readonlyrootfs' option on line %d: %s", i, md.Path)
					m.log.error(err)
					continue
				}
				value, err := parseBool(value)
				if err != nil {
					err = fmt.Errorf("failed to parse 'readonlyrootfs' option value as bool on line %d: %s", i, md.Path)
					m.log.error(err)
					continue
				}
				opts.ReadonlyRootfs = value
				continue
			case "droppedcaps":
				if value != "" {
					err = fmt.Errorf("inline value provided for 'droppedcaps' option on line %d (use unordered list): %s", i, md.Path)
					m.log.error(err)
					continue
				}
				for i < endIdx-1 {
					i++
					valLine := strings.TrimSpace(lines[i])
					if valLine == "" {
						continue
					}
					valMatch := kvLineRe.FindStringSubmatch(valLine)
					if valMatch != nil {
						// Found next option line, rewind and break
						i--
						break
					}
					valMatch = optionLineRe.FindStringSubmatch(valLine)
					if valMatch == nil {
						err = fmt.Errorf("invalid value provided for 'droppedcaps' option on line %d: %s", i, md.Path)
						m.log.error(err)
						continue
					}
					opts.DroppedCaps = append(opts.DroppedCaps, valMatch[1])
				}
				continue
			case "nonewprivileges":
				if value == "" {
					err = fmt.Errorf("missing value for 'nonewprivileges' option on line %d: %s", i, md.Path)
					m.log.error(err)
					continue
				}
				value, err := parseBool(value)
				if err != nil {
					err = fmt.Errorf("failed to parse 'nonewprivileges' option value as bool on line %d: %s", i, md.Path)
					m.log.error(err)
					continue
				}
				opts.NoNewPrivileges = value
				continue
			case "storageopts":
				if value != "" {
					err = fmt.Errorf("inline value provided for 'storageopts' option on line %d (use unordered list): %s", i, md.Path)
					m.log.error(err)
					continue
				}
				for i < endIdx-1 {
					i++
					valLine := strings.TrimSpace(lines[i])
					if valLine == "" {
						continue
					}
					valMatch := kvLineRe.FindStringSubmatch(valLine)
					if valMatch != nil {
						// Found next option line, rewind and break
						i--
						break
					}
					valMatch = optionLineRe.FindStringSubmatch(valLine)
					if valMatch == nil {
						err = fmt.Errorf("invalid value provided for 'storageopts' option on line %d: %s", i, md.Path)
						m.log.error(err)
						continue
					}
					opts.StorageOpts = append(opts.StorageOpts, valMatch[1])
				}
				continue
			case "cgroupparent":
				if value == "" {
					err = fmt.Errorf("missing value for 'cgroupparent' option on line %d: %s", i, md.Path)
					m.log.error(err)
					continue
				}
				opts.CgroupParent = value
				continue
			default:
				err = fmt.Errorf("unrecognized container option '%s' on line %d: %s", match[1], i, md.Path)
				m.log.error(err)
				continue
			}
		}
		if md.ContainerOptions == nil {
			md.ContainerOptions = make(ContainerOptionsWrapper)
		}
		md.ContainerOptions[host] = opts

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
