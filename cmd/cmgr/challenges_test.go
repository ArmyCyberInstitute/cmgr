package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ArmyCyberInstitute/cmgr/cmgr"
)

func TestConvertToCustomAtomicallyReplacesShorterProblemFile(t *testing.T) {
	directory := t.TempDir()
	problemPath := filepath.Join(directory, "problem.md")
	original := `# Conversion

- type: service-pybuild

## Details

Trailing content must remain exactly once.
`
	if err := os.WriteFile(problemPath, []byte(original), 0640); err != nil {
		t.Fatal(err)
	}
	if code := convertToCustom(new(cmgr.Manager), []string{directory}); code != NO_ERROR {
		t.Fatalf("conversion returned %d", code)
	}
	updated, err := os.ReadFile(problemPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updated), "- type: custom") {
		t.Fatalf("type was not converted: %s", updated)
	}
	if strings.Contains(string(updated), "service-pybuild") {
		t.Fatalf("old bytes remained after shorter rewrite: %s", updated)
	}
	if bytes.Count(updated, []byte("Trailing content")) != 1 {
		t.Fatalf("problem content was duplicated or lost: %s", updated)
	}
	if _, err := os.Stat(filepath.Join(directory, "Dockerfile")); err != nil {
		t.Fatalf("Dockerfile was not installed: %v", err)
	}
	info, err := os.Stat(problemPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0640 {
		t.Fatalf("problem mode changed to %o", info.Mode().Perm())
	}
}

func TestConvertToCustomRefusesExistingDockerfile(t *testing.T) {
	directory := t.TempDir()
	problemPath := filepath.Join(directory, "problem.md")
	original := []byte("# Existing\n\n- type: flask\n")
	if err := os.WriteFile(problemPath, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(directory, "Dockerfile"),
		[]byte("FROM scratch\n"),
		0600,
	); err != nil {
		t.Fatal(err)
	}

	if code := convertToCustom(new(cmgr.Manager), []string{directory}); code != RUNTIME_ERROR {
		t.Fatalf("conversion returned %d", code)
	}
	after, err := os.ReadFile(problemPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, original) {
		t.Fatal("problem file changed despite existing Dockerfile")
	}
}
