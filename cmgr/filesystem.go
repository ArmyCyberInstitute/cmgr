package cmgr

import (
	"errors"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Reads the environment variable CMGR_CHALLENGE_DIR and then normalizes it
// to an absolute path and validates that it is a directory.
func (m *Manager) setChallengeDirectory() error {
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

	return nil
}

// Performs error checking and calls out to `filepath.Walk` to traverse the directory.
func (m *Manager) inventoryChallenges(dir string) (map[ChallengeId]*ChallengeMetadata, []error) {
	// Normalize the directory we are passed in
	tgtDir, err := filepath.Abs(dir)
	if err != nil {
		m.log.errorf("bad directory string: %s", err)
		return nil, []error{err}
	}

	info, err := os.Stat(tgtDir)
	if err != nil {
		m.log.errorf("could not stat directory: %s", err)
		return nil, []error{err}
	}

	if !info.IsDir() {
		m.log.errorf("expected a directory: %s", tgtDir)
		return nil, []error{errors.New(tgtDir + " is not a directory")}
	}

	// Check that it is a sub-directory
	if len(tgtDir) < len(m.chalDir) || // Sub-directory cannot be shorter string
		tgtDir[:len(m.chalDir)] != m.chalDir || // Prefix must match
		(len(tgtDir) > len(m.chalDir) && tgtDir[len(m.chalDir)] != os.PathSeparator) { // Not a directory prefix

		err := fmt.Errorf("'%s' is not a sub-directory of '%s'", tgtDir, m.chalDir)
		m.log.error(err)
		return nil, []error{err}
	}

	// Crawl the directory
	challenges := make(map[ChallengeId]*ChallengeMetadata)
	errs := []error{}

	m.log.infof("searching %s for challenges", tgtDir)
	err = filepath.Walk(tgtDir, m.findChallenges(&challenges, &errs))
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

		h := crc32.NewIEEE()
		err = filepath.Walk(filepath.Dir(path), challengeChecksum(h))
		if err != nil {
			m.log.warnf("could not hash source files: %s", err)
			*errSlice = append(*errSlice, err)
			return nil
		}
		metadata.Checksum = h.Sum32()
		metadata.Path = filepath.Dir(path[len(m.chalDir)+1:])
		m.log.infof("found challenge %s (checksum: %x)",
			metadata.Id,
			metadata.Checksum)

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

// Strips the name field down to only alphanumeric runes.
func sanitizeName(dirty string) string {
	re := regexp.MustCompile(`[^a-zA-Z0-9]`)
	return re.ReplaceAllLiteralString(strings.ToLower(dirty), "-")
}

// The challenge checksum is a checksum of file properties (name, size, mode)
// for all filetypes as well as the actual file contents for non-directories.
// This is a stable checksum because the Go specification for `Walk` promises
// a lexicographical traversal of the directory structure.  Files that start
// with '.' are ignored.
func challengeChecksum(h hash.Hash) filepath.WalkFunc {
	return func(path string, info os.FileInfo, err error) error {
		// Consider any error during the walk a fatal problem.
		if err != nil {
			return err
		}

		// Ignore "hidden" files (from a *nix perspective)
		if info.Name()[0] == '.' {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Add the name, size, and mode fields to the checksum
		_, err = h.Write([]byte(info.Name() +
			fmt.Sprintf("%x", info.Size()) +
			fmt.Sprintf("%x", info.Mode())))
		if err != nil {
			return err
		}

		// If this is not a directory, add the contents to the checksum
		if !info.IsDir() {
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()

			_, err = io.Copy(h, f)
			if err != nil {
				return err
			}
		}

		return nil
	}
}
