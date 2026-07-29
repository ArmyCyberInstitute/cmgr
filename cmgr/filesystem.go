package cmgr

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"io/fs"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ArmyCyberInstitute/cmgr/cmgr/dockerfiles"
)

// Reads the environment variable CMGR_CHALLENGE_DIR and then normalizes it
// to an absolute path and validates that it is a directory.
func (m *Manager) setDirectories() error {
	var err error

	chalDir, isSet := os.LookupEnv(DIR_ENV)
	if !isSet {
		chalDir = "."
	}

	m.chalDir, err = filepath.Abs(chalDir)

	if err != nil {
		m.log.errorf("could not resolve challenge directory: %s", err)
		return err
	}

	m.log.infof("challenge directory: %s", m.chalDir)

	info, err := os.Stat(m.chalDir)
	if err != nil {
		m.log.errorf("could not stat the challenge directory: %s", err)
		return err
	}

	if !info.IsDir() {
		m.log.error("challenge directory must be a directory")
		return errors.New(m.chalDir + " is not a directory")
	}

	artifactsDir, isSet := os.LookupEnv(ARTIFACT_DIR_ENV)
	if !isSet {
		artifactsDir = "."
	}

	m.artifactsDir, err = filepath.Abs(artifactsDir)

	if err != nil {
		m.log.errorf("could not resolve artifacts directory: %s", err)
		return err
	}

	m.log.infof("artifacts directory: %s", m.artifactsDir)

	info, err = os.Stat(m.artifactsDir)
	if err != nil {
		m.log.errorf("could not stat the artifacts directory: %s", err)
		return err
	}

	if !info.IsDir() {
		m.log.error("artifacts directory must be a directory")
		return errors.New(m.artifactsDir + " is not a directory")
	}

	return nil
}

// Performs error checking and calls out to `filepath.Walk` to traverse the directory.
func (m *Manager) inventoryChallenges(dir string) (map[ChallengeId]*ChallengeMetadata, []error) {
	// Crawl the directory
	challenges := make(map[ChallengeId]*ChallengeMetadata)
	errs := []error{}

	m.log.infof("searching %s for challenges", dir)
	err := filepath.Walk(dir, m.findChallenges(&challenges, &errs))
	if err != nil {
		errs = append(errs, err)
		return nil, errs
	}

	return challenges, errs
}

// Wrapper around the function which implements the directory traversal logic.
func (m *Manager) findChallenges(challengeMap *map[ChallengeId]*ChallengeMetadata, errSlice *[]error) filepath.WalkFunc {
	return func(path string, info os.FileInfo, err error) error {
		// Skip errors (adding them to the error list)
		if err != nil {
			*errSlice = append(*errSlice, err)
			return nil
		}

		// Skip files and directories that start with "."
		if info.Name()[0] == '.' {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Don't need to do anything with directories
		if info.IsDir() {
			return nil
		}

		metadata, err := m.loadChallenge(path, info)
		if err != nil {
			*errSlice = append(*errSlice, err)
			return nil
		}

		if metadata == nil {
			return nil
		}

		legacyHash := crc32.NewIEEE()
		digestHash := sha256.New()
		err = filepath.Walk(
			filepath.Dir(path),
			challengeChecksum(
				filepath.Dir(path),
				legacyHash,
				digestHash,
			),
		)
		if err != nil {
			m.log.warnf("could not hash source files: %s", err)
			*errSlice = append(*errSlice, err)
			return nil
		}
		metadata.SourceChecksum = legacyHash.Sum32()
		metadata.SourceDigest = hex.EncodeToString(digestHash.Sum(nil))

		metadata.Path = path
		m.log.infof("found challenge %s", metadata.Id)

		if val, ok := (*challengeMap)[metadata.Id]; ok {
			err := fmt.Errorf("found multiple challenges with id '%s' at '%s' and '%s'",
				metadata.Id,
				val.Path,
				metadata.Path)
			m.log.error(err)
			return err
		}
		(*challengeMap)[metadata.Id] = metadata

		return nil
	}
}

// Strips the name field down to only alphanumeric runes with dashes.  Strips
// leading and trailing dashes to comply with docker naming conventions.
func sanitizeName(dirty string) string {
	re := regexp.MustCompile(`[^a-zA-Z0-9]`)
	return strings.Trim(re.ReplaceAllLiteralString(strings.ToLower(dirty), "-"), "-")
}

func (m *Manager) normalizeDirPath(dir string) (string, error) {
	// Normalize the directory we are passed in
	tgtDir, err := filepath.Abs(dir)
	if err != nil {
		m.log.errorf("bad directory string: %s", err)
		return "", err
	}

	info, err := os.Stat(tgtDir)
	if err != nil {
		m.log.errorf("could not stat directory: %s", err)
		return "", err
	}

	if !info.IsDir() {
		m.log.errorf("expected a directory: %s", tgtDir)
		return "", errors.New(tgtDir + " is not a directory")
	}

	// Check that it is a sub-directory
	if !pathInDirectory(tgtDir, m.chalDir) {
		err := fmt.Errorf("'%s' is not a sub-directory of '%s'", tgtDir, m.chalDir)
		m.log.error(err)
		return "", err
	}

	return tgtDir, nil
}

func pathInDirectory(path, dir string) bool {
	relative, err := filepath.Rel(dir, path)
	return err == nil &&
		relative != ".." &&
		!filepath.IsAbs(relative) &&
		!strings.HasPrefix(relative, ".."+string(os.PathSeparator))
}

func writeHashFrame(writer io.Writer, label string, data []byte) error {
	if err := binary.Write(writer, binary.BigEndian, uint32(len(label))); err != nil {
		return err
	}
	if _, err := io.WriteString(writer, label); err != nil {
		return err
	}
	if err := binary.Write(writer, binary.BigEndian, uint64(len(data))); err != nil {
		return err
	}
	_, err := writer.Write(data)
	return err
}

func metadataHashes(data []byte, path string) (uint32, string, error) {
	legacyHash := crc32.NewIEEE()
	if _, err := legacyHash.Write(data); err != nil {
		return 0, "", err
	}
	if _, err := io.WriteString(legacyHash, path); err != nil {
		return 0, "", err
	}

	digestHash := sha256.New()
	if err := writeHashFrame(digestHash, "metadata", data); err != nil {
		return 0, "", err
	}
	if err := writeHashFrame(digestHash, "path", []byte(path)); err != nil {
		return 0, "", err
	}
	return legacyHash.Sum32(), hex.EncodeToString(digestHash.Sum(nil)), nil
}

// The challenge digest is a framed hash of relative paths, file properties,
// symlink targets, and regular-file contents. Framing prevents different
// directory layouts from producing the same byte stream by concatenation.
// This is a stable checksum because the Go specification for `Walk` promises
// a lexicographical traversal of the directory structure.  Files that start
// with '.' are ignored.
func challengeChecksum(
	challDir string,
	legacyHash io.Writer,
	digestHash io.Writer,
) filepath.WalkFunc {
	return func(path string, info os.FileInfo, err error) error {
		// Consider any error during the walk a fatal problem.
		if err != nil {
			return err
		}

		// Ignore "hidden" files, READMEs, and problem configs
		if checksumIgnore(info.Name()) {

			if info.IsDir() {
				return filepath.SkipDir
			}

			return nil
		}

		// Keep the historical CRC32 byte stream stable for API compatibility
		// while independently hashing an unambiguous framed representation.
		if _, err := io.WriteString(
			legacyHash,
			info.Name()+
				fmt.Sprintf("%x", info.Size())+
				fmt.Sprintf("%x", info.Mode()),
		); err != nil {
			return err
		}

		relativePath, err := filepath.Rel(challDir, path)
		if err != nil {
			return err
		}
		if err := writeHashFrame(
			digestHash,
			"path",
			[]byte(filepath.ToSlash(relativePath)),
		); err != nil {
			return err
		}
		if err := writeHashFrame(
			digestHash,
			"mode",
			[]byte(info.Mode().String()),
		); err != nil {
			return err
		}
		if err := writeHashFrame(
			digestHash,
			"size",
			[]byte(fmt.Sprint(info.Size())),
		); err != nil {
			return err
		}

		// If this is not a directory, add the contents to the checksum
		if info.Mode()&os.ModeSymlink == os.ModeSymlink {
			linkTgt, err := os.Readlink(path)
			if err != nil {
				return fmt.Errorf("Invalid link found at %s: %s", path, err)
			}
			tgtPath, err := filepath.Abs(filepath.Join(filepath.Dir(path), linkTgt))
			if err != nil {
				return err
			}
			basePath, err := filepath.EvalSymlinks(challDir)
			if err != nil {
				return err
			}
			resolvedTarget, err := filepath.EvalSymlinks(tgtPath)
			if err != nil {
				return fmt.Errorf("invalid link found at %s: %w", path, err)
			}

			if !pathInDirectory(resolvedTarget, basePath) {
				return fmt.Errorf("encountered symlink at %q which points outside %q", path, challDir)
			}

			legacyTarget := filepath.Join(filepath.Dir(path), linkTgt)
			if _, err := io.WriteString(legacyHash, legacyTarget); err != nil {
				return err
			}
			if err := writeHashFrame(
				digestHash,
				"symlink",
				[]byte(linkTgt),
			); err != nil {
				return err
			}
		} else if info.Mode().IsRegular() {
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()

			if err := binary.Write(
				digestHash,
				binary.BigEndian,
				uint64(info.Size()),
			); err != nil {
				return err
			}
			_, err = io.Copy(io.MultiWriter(legacyHash, digestHash), f)
			if err != nil {
				return err
			}
		}

		return nil
	}
}

func checksumIgnore(name string) bool {
	return (name[0] == '.' && name != ".dockerignore") ||
		name == "README" ||
		name == "README.md" ||
		name == "problem.json" ||
		name == "problem.md" ||
		name == "solver" ||
		name == "cmgr.db"
}

func contextIgnore(name string) bool {
	return (name[0] == '.' && name != ".dockerignore") ||
		name == "README" ||
		name == "README.md" ||
		name == "problem.md" ||
		name == "solver" ||
		name == "cmgr.db"
}

type boundedWriter struct {
	writer    io.Writer
	remaining int64
	limit     int64
}

func (writer *boundedWriter) Write(data []byte) (int, error) {
	if int64(len(data)) > writer.remaining {
		return 0, fmt.Errorf("build context exceeds %d bytes", writer.limit)
	}
	written, err := writer.writer.Write(data)
	writer.remaining -= int64(written)
	return written, err
}

func (m *Manager) createBuildContext(cm *ChallengeMetadata, dockerfile []byte) (string, error) {
	tmpFile, err := os.CreateTemp("", "cmgr-build-context-*.tar")
	if err != nil {
		return "", err
	}
	closed := false
	defer func() {
		if !closed {
			_ = tmpFile.Close()
		}
	}()
	succeeded := false
	defer func() {
		if !succeeded {
			os.Remove(tmpFile.Name())
		}
	}()
	m.log.debug(tmpFile.Name())

	maxBytes := m.policy.MaxBuildContextBytes
	if maxBytes == 0 {
		maxBytes = 2 * 1024 * 1024 * 1024
	}
	maxFiles := m.policy.MaxBuildContextFiles
	if maxFiles == 0 {
		maxFiles = 10_000
	}
	limitedOutput := &boundedWriter{
		writer:    tmpFile,
		remaining: maxBytes,
		limit:     maxBytes,
	}
	newCtx := tar.NewWriter(limitedOutput)
	entryCount := 0
	accountEntry := func() error {
		entryCount++
		if entryCount > maxFiles {
			return fmt.Errorf("build context contains more than %d entries", maxFiles)
		}
		return nil
	}

	if dockerfile != nil {
		if err := accountEntry(); err != nil {
			return "", err
		}
		if err = writeBuildContextFile(
			newCtx,
			"Dockerfile",
			0644,
			dockerfile,
		); err != nil {
			return "", err
		}
	}

	supportFiles, err := dockerfiles.SupportFiles(cm.ChallengeType)
	if err != nil {
		return "", fmt.Errorf("could not load embedded build support files: %v", err)
	}
	for _, supportFile := range supportFiles {
		if err := accountEntry(); err != nil {
			return "", err
		}
		if err = writeBuildContextFile(
			newCtx,
			supportFile.Name,
			supportFile.Mode,
			supportFile.Data,
		); err != nil {
			return "", err
		}
	}

	if len(supportFiles) != 0 {
		if err = m.writeHacksportContextFiles(newCtx, cm); err != nil {
			return "", err
		}
	}

	// Iterate
	challengeDir := filepath.Dir(cm.Path)
	root, err := os.OpenRoot(challengeDir)
	if err != nil {
		return "", fmt.Errorf("could not open challenge root: %w", err)
	}
	defer root.Close()
	err = filepath.WalkDir(challengeDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}

		// Ignore "hidden" files, READMEs, and problem configs
		if contextIgnore(info.Name()) || challengeDir == path {

			if info.IsDir() && challengeDir != path {
				return filepath.SkipDir
			}

			return nil
		}
		if err := accountEntry(); err != nil {
			return err
		}

		m.log.debug(path)

		archivePath, err := filepath.Rel(challengeDir, path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("build context cannot contain symlink %q", archivePath)
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf(
				"build context contains unsupported file type %q (%s)",
				archivePath,
				info.Mode(),
			)
		}

		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}

		hdr.Name = strings.ReplaceAll(archivePath, `\`, `/`)
		hdr.Linkname = ""

		err = newCtx.WriteHeader(hdr)
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		fd, err := root.Open(archivePath)
		if err != nil {
			return err
		}
		currentInfo, err := fd.Stat()
		if err != nil {
			_ = fd.Close()
			return err
		}
		if !currentInfo.Mode().IsRegular() ||
			currentInfo.Size() != info.Size() ||
			currentInfo.Mode() != info.Mode() {
			_ = fd.Close()
			return fmt.Errorf("build context file changed while archiving: %q", archivePath)
		}
		_, err = io.CopyN(newCtx, fd, currentInfo.Size())
		closeErr := fd.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}

		return nil
	})

	if err != nil {
		return "", err
	}

	if err = newCtx.Close(); err != nil {
		return "", err
	}
	if err = tmpFile.Sync(); err != nil {
		return "", err
	}
	if err = tmpFile.Close(); err != nil {
		return "", err
	}
	closed = true
	succeeded = true
	return tmpFile.Name(), nil
}

func writeBuildContextFile(
	writer *tar.Writer,
	name string,
	mode int64,
	data []byte,
) error {
	header := tar.Header{
		Name: name,
		Mode: mode,
		Size: int64(len(data)),
	}
	if err := writer.WriteHeader(&header); err != nil {
		return err
	}
	_, err := writer.Write(data)
	return err
}

func (m *Manager) writeHacksportContextFiles(
	writer *tar.Writer,
	metadata *ChallengeMetadata,
) error {
	challengeDir := filepath.Dir(metadata.Path)
	problemData := map[string]interface{}{}
	problemPath := filepath.Join(challengeDir, "problem.json")
	if data, err := ioutil.ReadFile(problemPath); err == nil {
		if err = json.Unmarshal(data, &problemData); err != nil {
			return fmt.Errorf("could not prepare hacksport problem metadata: %v", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("could not read hacksport problem metadata: %v", err)
	}

	if attributes, ok := problemData["attributes"].(map[string]interface{}); ok {
		for key, value := range attributes {
			if _, present := problemData[key]; !present {
				problemData[key] = value
			}
		}
	}
	problemData["name"] = metadata.Name
	problemData["category"] = metadata.Category
	problemData["details"] = metadata.Details
	if metadata.Hints != nil {
		problemData["hints"] = metadata.Hints
	} else if _, present := problemData["hints"]; !present {
		problemData["hints"] = []string{}
	}
	problemData["score"] = metadata.Points
	problemData["unique_name"] = string(metadata.Id)
	if description, ok := problemData["description"].(string); !ok ||
		description == "" {
		problemData["description"] = metadata.Details
	}
	for key, value := range metadata.Attributes {
		if _, present := problemData[key]; !present {
			problemData[key] = value
		}
	}

	encodedProblem, err := json.MarshalIndent(problemData, "", "  ")
	if err != nil {
		return fmt.Errorf("could not encode hacksport problem metadata: %v", err)
	}
	encodedProblem = append(encodedProblem, '\n')
	if err = writeBuildContextFile(
		writer,
		".cmgr/problem.json",
		0644,
		encodedProblem,
	); err != nil {
		return err
	}

	for _, file := range []struct {
		source string
		target string
		mode   int64
	}{
		{"packages.txt", ".cmgr/packages.txt", 0644},
		{"requirements.txt", ".cmgr/requirements.txt", 0644},
		{"install_dependencies", ".cmgr/install_dependencies", 0755},
	} {
		data, readErr := ioutil.ReadFile(filepath.Join(challengeDir, file.source))
		if readErr != nil {
			if !os.IsNotExist(readErr) {
				return fmt.Errorf(
					"could not read hacksport support file %q: %v",
					file.source,
					readErr,
				)
			}
			data = nil
		}
		if err = writeBuildContextFile(
			writer,
			file.target,
			file.mode,
			data,
		); err != nil {
			return err
		}
	}
	return nil
}
