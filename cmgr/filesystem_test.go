package cmgr

import (
	"archive/tar"
	"encoding/json"
	"hash/crc32"
	"io"
	"strings"
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

func TestMetadataHashFramingAvoidsConcatenationAmbiguity(t *testing.T) {
	firstLegacy, first, err := metadataHashes([]byte("ab"), "c")
	if err != nil {
		t.Fatal(err)
	}
	secondLegacy, second, err := metadataHashes([]byte("a"), "bc")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("framed metadata hashes collided for distinct field boundaries")
	}
	if firstLegacy != secondLegacy {
		t.Fatal("legacy CRC32 concatenation behavior unexpectedly changed")
	}
	if firstLegacy != crc32.ChecksumIEEE([]byte("abc")) {
		t.Fatal("legacy metadata checksum is not compatible with prior releases")
	}
}

func TestCreateBuildContextRejectsSymlinks(t *testing.T) {
	directory := t.TempDir()
	problemPath := filepath.Join(directory, "problem.md")
	if err := os.WriteFile(problemPath, []byte("# test"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "target"), []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target", filepath.Join(directory, "link")); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{log: newLogger(DISABLED)}
	_, err := manager.createBuildContext(
		&ChallengeMetadata{
			Path:          problemPath,
			ChallengeType: "custom",
		},
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "cannot contain symlink") {
		t.Fatalf("unexpected symlink error: %v", err)
	}
}

func TestCreateBuildContextHonorsConfigurableEntryLimit(t *testing.T) {
	directory := t.TempDir()
	problemPath := filepath.Join(directory, "problem.md")
	if err := os.WriteFile(problemPath, []byte("# test"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"one", "two"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	manager := &Manager{
		log: newLogger(DISABLED),
		policy: managerPolicy{
			MaxBuildContextFiles: 1,
			MaxBuildContextBytes: 1024 * 1024,
		},
	}
	_, err := manager.createBuildContext(
		&ChallengeMetadata{
			Path:          problemPath,
			ChallengeType: "custom",
		},
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "more than 1 entries") {
		t.Fatalf("unexpected entry-limit error: %v", err)
	}
}

func TestCreateHacksportBuildContextIncludesCompatibilityFiles(t *testing.T) {
	challengeDir, err := ioutil.TempDir("", "cmgr-hacksport-context")
	if err != nil {
		t.Fatalf("failed to create challenge directory: %s", err)
	}
	defer os.RemoveAll(challengeDir)

	problemPath := filepath.Join(challengeDir, "problem.json")
	if err = ioutil.WriteFile(
		problemPath,
		[]byte(`{
			"name": "Context Test",
			"category": "Testing",
			"description": "Download {{url_for(\"artifact.txt\")}}.",
			"hints": [],
			"score": 10,
			"author": "Tester",
			"organization": "cmgr",
			"event": "tests",
			"custom_legacy_value": "preserved"
		}`),
		0600,
	); err != nil {
		t.Fatalf("failed to write problem metadata: %s", err)
	}
	if err = ioutil.WriteFile(
		filepath.Join(challengeDir, "challenge.py"),
		[]byte("class Problem:\n    pass\n"),
		0600,
	); err != nil {
		t.Fatalf("failed to write challenge.py: %s", err)
	}
	if err = ioutil.WriteFile(
		filepath.Join(challengeDir, "requirements.txt"),
		[]byte("requests==2.34.2\n"),
		0600,
	); err != nil {
		t.Fatalf("failed to write requirements: %s", err)
	}

	manager := &Manager{log: newLogger(DISABLED)}
	metadata := &ChallengeMetadata{
		Id:            "hacksport/context-test",
		Name:          "Context Test",
		Category:      "Testing",
		ChallengeType: "hacksport",
		Details:       `Download {{url("artifact.txt")}}.`,
		Hints:         []string{"Test hint"},
		Points:        10,
		Path:          problemPath,
	}
	dockerfile := manager.GetDockerfile("hacksport")
	contextPath, err := manager.createBuildContext(metadata, dockerfile)
	if err != nil {
		t.Fatalf("failed to create build context: %s", err)
	}
	defer os.Remove(contextPath)

	contextFile, err := os.Open(contextPath)
	if err != nil {
		t.Fatalf("failed to open build context: %s", err)
	}
	defer contextFile.Close()

	files := make(map[string][]byte)
	reader := tar.NewReader(contextFile)
	for {
		header, readErr := reader.Next()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			t.Fatalf("failed to read build context: %s", readErr)
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		data, readErr := ioutil.ReadAll(reader)
		if readErr != nil {
			t.Fatalf("failed to read %s: %s", header.Name, readErr)
		}
		files[header.Name] = data
	}

	for _, required := range []string{
		"Dockerfile",
		".cmgr/hacksport_compat/LICENSE.picoCTF",
		".cmgr/hacksport_compat/cmgr_hacksport/runner.py",
		".cmgr/problem.json",
		".cmgr/packages.txt",
		".cmgr/requirements.txt",
		".cmgr/install_dependencies",
		"challenge.py",
		"problem.json",
	} {
		if _, present := files[required]; !present {
			t.Errorf("build context is missing %s", required)
		}
	}
	if string(files[".cmgr/requirements.txt"]) != "requests==2.34.2\n" {
		t.Fatalf(
			"unexpected injected requirements: %q",
			files[".cmgr/requirements.txt"],
		)
	}

	var injected map[string]interface{}
	if err = json.Unmarshal(files[".cmgr/problem.json"], &injected); err != nil {
		t.Fatalf("could not decode injected metadata: %s", err)
	}
	if injected["custom_legacy_value"] != "preserved" {
		t.Fatal("legacy-only problem metadata was not preserved")
	}
	if injected["details"] != metadata.Details {
		t.Fatalf("cmgr details were not injected: %#v", injected["details"])
	}
}
