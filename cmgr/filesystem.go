package cmgr

import (
	"errors"
	"os"
	"path/filepath"
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
