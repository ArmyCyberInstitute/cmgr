package cmgr

import (
	"testing"

	"io/ioutil"
	"os"
	"path/filepath"
)

// Uses three well known unix filepaths to verify that `setChallengeDir`
// properly validates the value on load.
func TestSetDirectories(t *testing.T) {
	cwd, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("error in test harness: %s", err)
	}

	tmpdir, err := ioutil.TempDir("", "cmgrtest")
	if err != nil {
		t.Fatalf("failed to create a temporary directory: %s", err)
	}
	defer os.RemoveAll(tmpdir)

	tmpfile, err := ioutil.TempFile(tmpdir, "file")
	if err != nil {
		t.Fatalf("failed to create a temporary file: %s", err)
	}
	tmpfile.Close() // Will be removed by deferred RemoveAll

	// Minimal stub of manager
	mgr := new(Manager)
	mgr.log = newLogger(DISABLED)

	os.Setenv(DIR_ENV, tmpdir)
	if err = mgr.setDirectories(); err != nil {
		t.Errorf("'/tmp' should be a valid challenge directory")
	}

	os.Setenv(DIR_ENV, tmpfile.Name())
	if mgr.setDirectories() == nil {
		t.Errorf("'/dev/null' is invalid (not a directory)")
	}

	os.Setenv(DIR_ENV, filepath.Join(tmpdir, "doesnotexist"))
	if mgr.setDirectories() == nil {
		t.Errorf("non-existent file should have failed")
	}

	os.Unsetenv(DIR_ENV)

	if err = mgr.setDirectories(); err != nil {
		t.Fatalf("current working directory should be valid: %s", err)
	}

	if !filepath.IsAbs(mgr.chalDir) {
		t.Fatalf("did not produce absolute path")
	}

	if cwd != mgr.chalDir {
		t.Fatalf("empty environment variable did not use working directory")
	}
}
