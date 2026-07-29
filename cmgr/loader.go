package cmgr

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/docker/go-units"
)

func (m *Manager) loadChallenge(path string, info os.FileInfo) (*ChallengeMetadata, error) {
	var md *ChallengeMetadata
	var err error

	// Screen out non-problem files
	isJSON := info.Name() == "problem.json"
	isMarkdown := info.Name() == "problem.md"
	if (isJSON || isMarkdown) && !info.Mode().IsRegular() {
		return nil, fmt.Errorf(
			"challenge metadata must be a regular file: %s",
			path,
		)
	}
	if isJSON {
		md, err = m.loadJsonChallenge(path, info)
	} else if isMarkdown {
		md, err = m.loadMarkdownChallenge(path, info)
	}

	if err != nil || md == nil {
		return md, err
	}

	prefix := ""
	if md.Namespace != "" {
		prefix = md.Namespace + "/"
	}
	if md.Id != "" {
		md.Id = ChallengeId(prefix + sanitizeName(string(md.Id)))
	} else {
		md.Id = ChallengeId(prefix + sanitizeName(md.Name))
	}
	md.Path = filepath.Dir(path)

	solverPath := filepath.Join(md.Path, "solver")
	info, err = os.Stat(solverPath)
	md.SolveScript = err == nil && info.IsDir()

	if md.ChallengeOptions.Overrides == nil {
		md.ChallengeOptions.Overrides = make(map[string]ContainerOptions)
	}
	md.ChallengeOptions.Overrides[""] = md.ChallengeOptions.ContainerOptions
	m.log.debugf("challenge options: %#v", md.ChallengeOptions)

	err = m.processDockerfile(md)
	if err != nil {
		return md, err
	}

	err = m.validateMetadata(md)

	return md, err
}

var templateRe *regexp.Regexp = regexp.MustCompile(`\{\{[^}]*\}\}`)

const filenamePattern string = "[a-zA-Z0-9_.-]+"
const displayTextPattern string = `[^<>'"]+`
const urlPathPattern string = `/?([a-zA-Z0-9_%/=?+&#!.,-]*)`

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
const linkRePattern string = `\{\{\s*link\(["'](\w+)["'],\s*["']` + urlPathPattern + `["']\)\s*\}\}`

var linkRe *regexp.Regexp = regexp.MustCompile(linkRePattern)

// {{link("/url/in/challenge")}}
const shortLinkRePattern string = `\{\{\s*link\(["']` + urlPathPattern + `["']\)\s*\}\}`

var shortLinkRe *regexp.Regexp = regexp.MustCompile(shortLinkRePattern)

// {{link_as("port_name", "/url/in/challenge", "display text")}}
const linkAsRePattern string = `\{\{\s*link_as\(["'](\w+)["'],\s*["']` + urlPathPattern + `["'],\s*["'](` + displayTextPattern + `)["']\s*\)\}\}`

var linkAsRe *regexp.Regexp = regexp.MustCompile(linkAsRePattern)

// {{link_as("/url/in/challenge", "display text")}}
const shortLinkAsRePattern string = `\{\{\s*link_as\(["']` + urlPathPattern + `["'],\s*["'](` + displayTextPattern + `)["']\s*\)\}\}`

var shortLinkAsRe *regexp.Regexp = regexp.MustCompile(shortLinkAsRePattern)

func (m *Manager) validateMetadata(md *ChallengeMetadata) error {
	var lastErr error
	var validationErrors []error
	record := func(err error) {
		if err != nil {
			validationErrors = append(validationErrors, err)
		}
	}
	// Require a challenge name
	if md.Name == "" {
		lastErr = fmt.Errorf("challenge file missing name: %s", md.Path)
		m.log.error(lastErr)
		record(lastErr)
	}

	// Validate Namespace
	re := regexp.MustCompile(`^([a-z0-9]+(/[a-z0-9]+)*)?$`)
	if !re.MatchString(md.Namespace) {
		lastErr = fmt.Errorf("invalid namespace (limited to lowercase letters, numerals, and '/') of '%s': %s",
			md.Namespace, md.Path)
		m.log.error(lastErr)
		record(lastErr)
	}

	// Validate Description
	templates := templateRe.FindAllString(md.Description, -1)
	if len(templates) > 0 {
		lastErr = fmt.Errorf("template strings not allowed in the 'description' field, but found: %s", strings.Join(templates, ", "))
		m.log.error(lastErr)
		record(lastErr)
	}
	if md.MaxUsers < 0 {
		lastErr = fmt.Errorf("max_users cannot be negative: %s", md.Path)
		m.log.error(lastErr)
		record(lastErr)
	}
	if md.Points < 0 {
		lastErr = fmt.Errorf("points cannot be negative: %s", md.Path)
		m.log.error(lastErr)
		record(lastErr)
	}

	// Validate (& lift) Hints
	onePort := len(md.PortMap) == 1
	// Insert port elision into the string
	var portName string
	refPort := make(map[string]bool)
	for k := range md.PortMap {
		// Only executes once because of above check
		portName = k
		refPort[k] = false
	}

	m.log.debugf("onePort=%t", onePort)

	normalizeAndCheckTemplated := func(s string) (string, error) {
		// Expand ShortUrlFor unconditionally
		r := `{{url_for("$1", "$1")}}`
		s = shortUrlForRe.ReplaceAllString(s, r)

		var err error
		if onePort {
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
		} else {
			base_msg := fmt.Sprintf("cannot use '%%s' in challenge type '%s' which publishes %d ports", md.ChallengeType, len(md.PortMap))

			matches := shortUrlForRe.FindAllString(s, -1)
			for _, match := range matches {
				err = fmt.Errorf(base_msg, match)
				m.log.error(err)
			}

			matches = shortHttpBaseRe.FindAllString(s, -1)
			for _, match := range matches {
				err = fmt.Errorf(base_msg, match)
				m.log.error(err)
			}

			matches = shortPortRe.FindAllString(s, -1)
			for _, match := range matches {
				err = fmt.Errorf(base_msg, match)
				m.log.error(err)
			}

			matches = shortServerRe.FindAllString(s, -1)
			for _, match := range matches {
				err = fmt.Errorf(base_msg, match)
				m.log.error(err)
			}

			matches = shortLinkRe.FindAllString(s, -1)
			for _, match := range matches {
				err = fmt.Errorf(base_msg, match)
				m.log.error(err)
			}

			matches = shortLinkAsRe.FindAllString(s, -1)
			for _, match := range matches {
				err = fmt.Errorf(base_msg, match)
				m.log.error(err)
			}
		}

		r = `<a href='{{url("${1}")}}' download>${2}</a>`
		s = urlForRe.ReplaceAllString(s, r)

		r = `<a href='{{http_base("${1}")}}:{{port("${1}")}}/${2}' target='_blank'>/${2}</a>`
		s = linkRe.ReplaceAllString(s, r)

		r = `<a href='{{http_base("${1}")}}:{{port("${1}")}}/${2}' target='_blank'>${3}</a>`
		s = linkAsRe.ReplaceAllString(s, r)

		templates := templateRe.FindAllString(s, -1)
		for _, tmpl := range templates {
			if urlRe.MatchString(tmpl) || lookupRe.MatchString(tmpl) {
				continue
			}

			isPortRef := false
			res := httpBaseRe.FindStringSubmatch(tmpl)
			if res == nil {
				res = portRe.FindStringSubmatch(tmpl)
				if res != nil {
					isPortRef = true
				}
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

			if isPortRef {
				refPort[res[1]] = true
			}
		}

		return s, err
	}

	// Validate (& lift) Details
	res, err := normalizeAndCheckTemplated(md.Details)
	if err != nil {
		lastErr = err
		record(lastErr)
	}
	md.Details = res

	for i, hint := range md.Hints {
		res, err = normalizeAndCheckTemplated(hint)

		if err != nil {
			lastErr = err
			record(lastErr)
		}

		md.Hints[i] = res
	}

	for port, used := range refPort {
		if !used && !isHacksportChallengeType(md.ChallengeType) {
			lastErr = fmt.Errorf("port '%s' published but not referenced: %s", port, md.Path)
			m.log.error(lastErr)
			record(lastErr)
		}
	}

	// Validate ContainerOptions
	for host, opts := range md.ChallengeOptions.Overrides {
		hostStr := ""
		if host != "" {
			hostStr = fmt.Sprintf("host %s: ", host)
		}

		if opts.Seccomp != nil {
			err := opts.Seccomp.resolve(md.Path)
			if err != nil {
				lastErr = fmt.Errorf("%serror resolving seccomp container option: %v", hostStr, err)
				m.log.error(lastErr)
				record(lastErr)
			}
			md.ChallengeOptions.Overrides[host] = opts
		}

		if opts.Cpus != "" {
			nanoCPUs, err := parseNanoCPUs(opts.Cpus)
			if err != nil || nanoCPUs <= 0 {
				if err == nil {
					err = errors.New("CPU value must be greater than zero")
				}
				lastErr = fmt.Errorf("%serror parsing cpus container option: %v", hostStr, err)
				m.log.error(lastErr)
				record(lastErr)
			}
		}

		if opts.Memory != "" {
			var memoryBytes int64
			memoryBytes, err = units.RAMInBytes(opts.Memory)
			if err != nil || memoryBytes <= 0 {
				if err == nil {
					err = errors.New("memory value must be greater than zero")
				}
				lastErr = fmt.Errorf("%serror parsing memory container option: %v", hostStr, err)
				m.log.error(lastErr)
				record(lastErr)
			}
		}

		for _, ulimit := range opts.Ulimits {
			limit, err := units.ParseUlimit(ulimit)
			if err != nil {
				lastErr = fmt.Errorf("%serror parsing ulimits container option: %v", hostStr, err)
				m.log.error(lastErr)
				record(lastErr)
				continue
			}
			// See https://docs.docker.com/engine/reference/commandline/run/#set-ulimits-in-container---ulimit
			if limit.Name == "nproc" {
				lastErr = fmt.Errorf("%snproc ulimits are not supported, use the pidslimit container option instead", hostStr)
				m.log.error(lastErr)
				record(lastErr)
			}
		}

		if opts.PidsLimit < -1 {
			lastErr = fmt.Errorf("%sinvalid pidslimit container option (must be >= -1)", hostStr)
			m.log.error(lastErr)
			record(lastErr)
		}

		droppable_capabilities := map[string]struct{}{
			"CAP_ALL":              {},
			"CAP_AUDIT_WRITE":      {},
			"CAP_CHOWN":            {},
			"CAP_DAC_OVERRIDE":     {},
			"CAP_FOWNER":           {},
			"CAP_FSETID":           {},
			"CAP_KILL":             {},
			"CAP_MKNOD":            {},
			"CAP_NET_BIND_SERVICE": {},
			"CAP_NET_RAW":          {},
			"CAP_SETFCAP":          {},
			"CAP_SETGID":           {},
			"CAP_SETPCAP":          {},
			"CAP_SETUID":           {},
			"CAP_SYS_CHROOT":       {},
		}
		for _, cap := range opts.DroppedCaps {
			if _, ok := droppable_capabilities[cap]; !ok {
				if _, ok = droppable_capabilities[fmt.Sprintf("CAP_%s", cap)]; !ok {
					lastErr = fmt.Errorf("%sinvalid DroppedCaps container option: %s", hostStr, cap)
					m.log.error(lastErr)
					record(lastErr)
				}
			}
		}

		if opts.DiskQuota != "" {
			diskBytes, err := units.RAMInBytes(opts.DiskQuota) // Despite its name, Docker uses this method to parse the size= storage option.
			if err != nil || diskBytes <= 0 {
				if err == nil {
					err = errors.New("disk quota must be greater than zero")
				}
				lastErr = fmt.Errorf("%serror parsing DiskQuota container option: %v", hostStr, err)
				m.log.error(lastErr)
				record(lastErr)
			}
		}
	}
	md.ChallengeOptions.ContainerOptions = md.ChallengeOptions.Overrides[""]

	return errors.Join(validationErrors...)
}

func (m *Manager) validateBuild(cMeta *ChallengeMetadata, md *BuildMetadata, files []string) error {
	var validationErrors []error
	refFile := make(map[string]bool)
	refLookup := make(map[string]bool)
	for _, k := range files {
		refFile[k] = false
	}
	m.log.debugf("files: %#v", refFile)

	for k := range md.LookupData {
		refLookup[k] = false
	}
	m.log.debugf("lookups: %#v", refLookup)

	checkTemplated := func(s string) error {
		var err error
		fileRefs := urlRe.FindAllStringSubmatch(s, -1)
		for _, ref := range fileRefs {
			_, ok := refFile[ref[1]]
			if !ok {
				err = fmt.Errorf("unknown artifact '%s' referenced with '%s': %s/%d", ref[1], ref[0], md.Challenge, md.Id)
				m.log.error(err)
				validationErrors = append(validationErrors, err)
			} else {
				refFile[ref[1]] = true
			}
		}

		lookupRefs := lookupRe.FindAllStringSubmatch(s, -1)
		for _, ref := range lookupRefs {
			_, ok := refLookup[ref[1]]
			if !ok {
				err = fmt.Errorf("unknown lookup key of '%s' referenced with '%s': %s/%d", ref[1], ref[0], md.Challenge, md.Id)
				m.log.error(err)
				validationErrors = append(validationErrors, err)
			} else {
				refLookup[ref[1]] = true
			}
		}

		return err
	}

	// Validate (& lift) Details
	err := checkTemplated(cMeta.Details)

	for _, hint := range cMeta.Hints {
		tmpErr := checkTemplated(hint)

		if tmpErr != nil {
			err = tmpErr
		}
	}

	for f, used := range refFile {
		if !used {
			err = fmt.Errorf("artifact file '%s' published but not referenced: %s/%d", f, md.Challenge, md.Id)
			m.log.error(err)
			validationErrors = append(validationErrors, err)
		}
	}

	for key, used := range refLookup {
		if !used {
			err = fmt.Errorf("lookup value '%s' published but not referenced: %s/%d", key, md.Challenge, md.Id)
			m.log.error(err)
			validationErrors = append(validationErrors, err)
		}
	}

	return errors.Join(validationErrors...)
}

// Validates the challenge metadata for compliance with expectations
func (m *Manager) processDockerfile(md *ChallengeMetadata) error {
	if isHacksportChallengeType(md.ChallengeType) {
		challengePath := filepath.Join(md.Path, "challenge.py")
		challengeInfo, err := os.Stat(challengePath)
		if err != nil {
			return fmt.Errorf(
				"invalid hacksport challenge (%s): could not read challenge.py: %v",
				md.Id,
				err,
			)
		}
		if !challengeInfo.Mode().IsRegular() {
			return fmt.Errorf(
				"invalid hacksport challenge (%s): challenge.py is not a regular file",
				md.Id,
			)
		}
	}

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
	} else if m.GetDockerfile(md.ChallengeType) == nil {
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
			m.log.errorf("could not open custom Dockerfile (%s): %s", md.Id, err)
			return err
		}
		const maxDockerfileBytes = 4 * 1024 * 1024
		data, err = ioutil.ReadAll(io.LimitReader(f, maxDockerfileBytes+1))
		closeErr := f.Close()
		if err != nil {
			m.log.errorf("could not read custom Dockerfile (%s): %s", md.Id, err)
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if len(data) > maxDockerfileBytes {
			return fmt.Errorf(
				"custom Dockerfile for %s exceeds %d bytes",
				md.Id,
				maxDockerfileBytes,
			)
		}
	} else {
		data = m.GetDockerfile(md.ChallengeType)
	}

	if data == nil || len(data) == 0 {
		err = fmt.Errorf("could not find valid Dockerfile (%s)", md.Id)
		m.log.error(err)
		return err
	}

	dockerfile := string(data)

	// Search for "FROM" lines and organize available targets
	tgtRe := regexp.MustCompile(`FROM +\S+(?: +[aA][sS] +(\w+))?`)
	stages := tgtRe.FindAllStringSubmatchIndex(dockerfile, -1)
	if stages == nil {
		err = fmt.Errorf("could not find 'FROM' line in Dockerfile (%s)", md.Id)
		m.log.error(err)
		return err
	}

	// Search for PUBLISH directives and associate with targets
	re := regexp.MustCompile(`# *PUBLISH +(\d+) +AS +(\w+)\s*`)
	publishedPorts := re.FindAllStringSubmatchIndex(dockerfile, -1)

	hasBuilder := false
	hostNames := []HostInfo{}
	for i, stage := range stages {
		target := ""
		name := ""
		if stage[2] != -1 {
			target = dockerfile[stage[2]:stage[3]]
			name = target
			m.log.debugf("dockerfile[%d:%d] %s", stage[2], stage[3], target)
		}

		if target == "" && i+1 == len(stages) {
			name = "challenge"
		}

		if target == "builder" {
			hasBuilder = true
		}

		hostNames = append(hostNames, HostInfo{name, target})
	}

	// Search for LAUNCH directive
	launchDirectiveRe := regexp.MustCompile(`# *LAUNCH +(\w+(?: +\w+)*)`)
	launchDirective := launchDirectiveRe.FindStringSubmatch(dockerfile)
	hostArray := []HostInfo{}
	if launchDirective == nil {
		hostArray = []HostInfo{hostNames[len(hostNames)-1]}
	} else {
		launchRe := regexp.MustCompile(`\w+`)
		launches := launchRe.FindAllString(launchDirective[1], -1)
		m.log.debugf("launches = %v", launches)
		launched := make(map[string]struct{}, len(launches))
		for _, launch := range launches {
			if _, duplicate := launched[launch]; duplicate {
				return fmt.Errorf("Docker target %q is listed in LAUNCH more than once", launch)
			}
			launched[launch] = struct{}{}
			validLaunch := false
			for _, hostInfo := range hostNames {
				if launch == hostInfo.Name {
					validLaunch = true
					hostArray = append(hostArray, hostInfo)
					break
				}
			}
			if !validLaunch {
				err = fmt.Errorf("cannot launch '%s' because it is not a Docker target", launch)
				m.log.error(err)
				return err
			}
		}
	}
	if hasBuilder {
		hostArray = append([]HostInfo{{"builder", "builder"}}, hostArray...)
	}
	md.Hosts = hostArray
	launchedHosts := make(map[string]struct{}, len(hostArray))
	for _, host := range hostArray {
		launchedHosts[host.Name] = struct{}{}
	}
	// Build md.Hosts and md.PortMap
	//   md.Hosts = builder + LAUNCH
	//            | builder + DEFAULT (last target in Dockerfile)
	//            | DEFAULT
	// Throw error if host in PortMap but not Hosts
	if len(publishedPorts) > 0 && md.PortMap == nil {
		md.PortMap = make(map[string]PortInfo)
	}
	endpointNames := make(map[PortInfo]string, len(md.PortMap)+len(publishedPorts))
	for portName, endpoint := range md.PortMap {
		if endpoint.Port <= 0 || endpoint.Port >= 65536 {
			return fmt.Errorf(
				"published port %q has invalid container port %d",
				portName,
				endpoint.Port,
			)
		}
		if _, willLaunch := launchedHosts[endpoint.Host]; !willLaunch ||
			endpoint.Host == "builder" {
			return fmt.Errorf(
				"published port %q is exposed on host %q which is not launched",
				portName,
				endpoint.Host,
			)
		}
		if existingName, exists := endpointNames[endpoint]; exists {
			err = fmt.Errorf(
				"published ports '%s' and '%s' both map to %s:%d",
				existingName,
				portName,
				endpoint.Host,
				endpoint.Port,
			)
			m.log.error(err)
			return err
		}
		endpointNames[endpoint] = portName
	}
	m.log.debugf("found %d ports", len(publishedPorts))

	stageIdx := 0
	for _, portMatch := range publishedPorts {
		// Advance to correct stage
		for (stageIdx+1) < len(stages) && stages[stageIdx+1][0] < portMatch[0] {
			stageIdx++
		}
		port, err := strconv.Atoi(dockerfile[portMatch[2]:portMatch[3]])
		if err != nil {
			m.log.errorf("could not convert Dockerfile port to int: %s", err)
			return err
		}
		portName := dockerfile[portMatch[4]:portMatch[5]]
		if port <= 0 || port >= 65536 {
			return fmt.Errorf(
				"published port %q has invalid container port %d",
				portName,
				port,
			)
		}
		host := hostNames[stageIdx]
		if host.Name == "" {
			err = fmt.Errorf("published port '%s' uses a stage with no reference name (and not last stage)", portName)
			m.log.error(err)
			return err
		} else if host.Name == "builder" {
			err = fmt.Errorf("published port '%s' is from 'builder' host which will not be launched", portName)
			m.log.error(err)
			return err
		}

		_, willLaunch := launchedHosts[host.Name]
		if !willLaunch {
			err = fmt.Errorf("published port '%s' is exposed on host '%s' which is not marked for launching", portName, host.Name)
			m.log.error(err)
			return err
		}

		endpoint := PortInfo{Host: host.Name, Port: port}
		if existingEndpoint, exists := md.PortMap[portName]; exists {
			err = fmt.Errorf(
				"published port name '%s' is declared more than once (%s:%d and %s:%d)",
				portName,
				existingEndpoint.Host,
				existingEndpoint.Port,
				endpoint.Host,
				endpoint.Port,
			)
			m.log.error(err)
			return err
		}
		if existingName, exists := endpointNames[endpoint]; exists {
			err = fmt.Errorf(
				"published port '%s' aliases '%s' at %s:%d",
				portName,
				existingName,
				endpoint.Host,
				endpoint.Port,
			)
			m.log.error(err)
			return err
		}

		endpointNames[endpoint] = portName
		md.PortMap[portName] = endpoint
	}

	// Validate ContainerOptions hosts
	for opt_host := range md.ChallengeOptions.Overrides {
		if opt_host == "" {
			continue
		}
		found := false
		_, found = launchedHosts[opt_host]
		found = found && opt_host != "builder"
		if !found {
			err = fmt.Errorf("container options are specified for host %s, which is not launched", opt_host)
			m.log.error(err)
			return err
		}
	}

	return err
}

type hacksportAttrs struct {
	Author       string `json:"author"`
	Event        string `json:"event"`
	Organization string `json:"organization"`
	Version      string `json:"version"`
	Score        int    `json:"score"`
}

func isHacksportChallengeType(challengeType string) bool {
	return challengeType == "hacksport" ||
		challengeType == "hacksport-ubuntu26" ||
		challengeType == "hacksport-legacy"
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

	// Validate a single JSON value and reject misspelled or internal fields.
	// Known fields from legacy challenge catalogs are accepted explicitly,
	// then removed before the strict ChallengeMetadata decode. Historically
	// json.Unmarshal ignored these fields on non-hacksport challenges, so
	// retaining them as no-ops preserves existing challenge corpora.
	var raw map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err = decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("could not decode challenge file: %w", err)
	}
	if err = decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			err = errors.New("multiple JSON values are not allowed")
		}
		return nil, fmt.Errorf("invalid trailing JSON data: %w", err)
	}
	allowedFields := map[string]struct{}{
		"id": {}, "name": {}, "namespace": {}, "challenge_type": {},
		"description": {}, "details": {}, "hints": {}, "templatable": {},
		"port_map": {}, "max_users": {}, "category": {}, "points": {},
		"tags": {}, "attributes": {}, "challenge_options": {},
		// Historical catalog and hacksport metadata. Some older catalogs
		// include these fields on custom and static-pybuild challenges even
		// though cmgr never consumed them for those challenge types.
		"author": {}, "event": {}, "organization": {}, "version": {},
		"score": {}, "walkthrough": {}, "pip_requirements": {},
		"pip_python_version": {}, "packages": {}, "pkg_dependencies": {},
		"section": {},
	}
	compatibilityFields := map[string]struct{}{
		"author": {}, "event": {}, "organization": {}, "version": {},
		"score": {}, "walkthrough": {}, "pip_requirements": {},
		"pip_python_version": {}, "packages": {}, "pkg_dependencies": {},
		"section": {},
	}
	var declaredType string
	if encodedType, present := raw["challenge_type"]; present {
		if err := json.Unmarshal(encodedType, &declaredType); err != nil {
			return nil, fmt.Errorf("challenge_type must be a string: %w", err)
		}
	}
	strictRaw := make(map[string]json.RawMessage, len(raw))
	for key, value := range raw {
		if _, allowed := allowedFields[key]; !allowed {
			return nil, fmt.Errorf("unknown challenge JSON field %q", key)
		}
		if _, compatibilityField := compatibilityFields[key]; compatibilityField {
			continue
		}
		strictRaw[key] = value
	}
	strictData, err := json.Marshal(strictRaw)
	if err != nil {
		return nil, err
	}

	metadata := new(ChallengeMetadata)
	decoder = json.NewDecoder(bytes.NewReader(strictData))
	decoder.DisallowUnknownFields()
	err = decoder.Decode(metadata)
	if err != nil {
		m.log.errorf("could not unmarshal challenge file: %s", err)
		return nil, err
	}

	// A missing type is the historical hacksport JSON format. Explicit
	// hacksport types use the same lifting rules when old fields are present,
	// while also allowing modern cmgr options such as seccomp configuration.
	implicitHacksport := metadata.ChallengeType == ""
	if implicitHacksport || isHacksportChallengeType(metadata.ChallengeType) {
		challengePath := filepath.Join(filepath.Dir(path), "challenge.py")
		challengeInfo, statErr := os.Stat(challengePath)
		err = statErr
		if err != nil {
			err = fmt.Errorf(
				"could not stat 'challenge.py' on hacksport challenge %s: %v",
				path,
				err,
			)
			m.log.error(err)
			return nil, err
		}
		if !challengeInfo.Mode().IsRegular() {
			err = fmt.Errorf(
				"'challenge.py' on hacksport challenge is not a regular file: %s",
				path,
			)
			m.log.error(err)
			return nil, err
		}

		var attrs hacksportAttrs
		err = json.Unmarshal(data, &attrs)
		if err != nil {
			m.log.error(err)
			return nil, err
		}

		if implicitHacksport {
			metadata.ChallengeType = "hacksport"
		}
		if metadata.Namespace == "" {
			metadata.Namespace = "hacksport"
		}
		if metadata.Details == "" {
			metadata.Details = metadata.Description
			metadata.Description = ""
		}
		metadata.SolveScript = false

		if metadata.Points == 0 {
			metadata.Points = attrs.Score
		}

		if metadata.Attributes == nil {
			metadata.Attributes = make(map[string]string)
		}
		if attrs.Author != "" {
			metadata.Attributes["author"] = attrs.Author
		}

		if attrs.Event != "" {
			metadata.Attributes["event"] = attrs.Event
		}

		if attrs.Organization != "" {
			metadata.Attributes["organization"] = attrs.Organization
		}

		if attrs.Version != "" {
			metadata.Attributes["version"] = attrs.Version
		}
	}

	metadata.MetadataChecksum, metadata.MetadataDigest, err = metadataHashes(
		data,
		path,
	)
	if err != nil {
		return nil, err
	}

	return metadata, nil
}
