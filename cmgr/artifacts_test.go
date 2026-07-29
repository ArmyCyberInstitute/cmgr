package cmgr

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type artifactTestEntry struct {
	name     string
	typeflag byte
	body     string
	linkname string
}

func artifactTestArchive(t *testing.T, entries []artifactTestEntry) []byte {
	t.Helper()
	var output bytes.Buffer
	gzipWriter := gzip.NewWriter(&output)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, entry := range entries {
		header := &tar.Header{
			Name:     entry.name,
			Typeflag: entry.typeflag,
			Mode:     0644,
			Size:     int64(len(entry.body)),
			Linkname: entry.linkname,
		}
		if entry.typeflag == tar.TypeDir || entry.typeflag == tar.TypeSymlink {
			header.Size = 0
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if header.Size != 0 {
			if _, err := tarWriter.Write([]byte(entry.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func TestCacheArtifactsValidatesAndInstallsAtomically(t *testing.T) {
	directory := t.TempDir()
	manager := &Manager{
		artifactsDir: directory,
		policy: managerPolicy{
			MaxArtifactFiles:     10,
			MaxArtifactBytes:     1024,
			MaxArtifactFileBytes: 512,
		},
	}
	destination := filepath.Join(directory, "1.tar.gz")
	files, err := manager.cacheArtifacts(
		bytes.NewReader(artifactTestArchive(t, []artifactTestEntry{
			{name: "docs", typeflag: tar.TypeDir},
			{name: "docs/readme.txt", typeflag: tar.TypeReg, body: "hello"},
		})),
		destination,
	)
	if err != nil {
		t.Fatalf("valid archive was rejected: %v", err)
	}
	if len(files) != 1 || files[0] != "docs/readme.txt" {
		t.Fatalf("unexpected artifact list: %#v", files)
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("artifact mode is %o, expected 600", info.Mode().Perm())
	}
}

func TestCacheArtifactsRejectsUnsafeOrOversizedArchives(t *testing.T) {
	tests := []struct {
		name    string
		entries []artifactTestEntry
		policy  managerPolicy
		match   string
	}{
		{
			name: "traversal",
			entries: []artifactTestEntry{
				{name: "../escape", typeflag: tar.TypeReg, body: "bad"},
			},
			match: "escapes",
		},
		{
			name: "symlink",
			entries: []artifactTestEntry{
				{name: "link", typeflag: tar.TypeSymlink, linkname: "/etc/passwd"},
			},
			match: "unsupported tar type",
		},
		{
			name: "entry count",
			entries: []artifactTestEntry{
				{name: "one", typeflag: tar.TypeReg, body: "1"},
				{name: "two", typeflag: tar.TypeReg, body: "2"},
			},
			policy: managerPolicy{
				MaxArtifactFiles:     1,
				MaxArtifactBytes:     100,
				MaxArtifactFileBytes: 100,
			},
			match: "more than 1 entries",
		},
		{
			name: "uncompressed bytes",
			entries: []artifactTestEntry{
				{name: "large", typeflag: tar.TypeReg, body: "12345"},
			},
			policy: managerPolicy{
				MaxArtifactFiles:     10,
				MaxArtifactBytes:     4,
				MaxArtifactFileBytes: 10,
			},
			match: "exceeds 4 uncompressed bytes",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			manager := &Manager{artifactsDir: directory, policy: test.policy}
			destination := filepath.Join(directory, "existing.tar.gz")
			if err := os.WriteFile(destination, []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			_, err := manager.cacheArtifacts(
				bytes.NewReader(artifactTestArchive(t, test.entries)),
				destination,
			)
			if err == nil || !strings.Contains(err.Error(), test.match) {
				t.Fatalf("unexpected error: %v", err)
			}
			data, readErr := os.ReadFile(destination)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(data) != "original" {
				t.Fatal("failed archive replaced the prior artifact")
			}
		})
	}
}

func TestArtifactDefaultLimitsMatchReleasePolicy(t *testing.T) {
	manager := new(Manager)
	files, total, _ := manager.artifactLimits()
	if files != 10_000 {
		t.Fatalf("default artifact entry limit is %d", files)
	}
	if total != 5*1024*1024*1024 {
		t.Fatalf("default artifact byte limit is %d", total)
	}
}

func TestRollbackStagedBuildDoesNotDeleteUntouchedArtifact(t *testing.T) {
	directory := t.TempDir()
	canonical := filepath.Join(directory, "artifact.tar.gz")
	if err := os.WriteFile(canonical, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	manager := new(Manager)
	if err := manager.rollbackStagedBuild(&stagedBuildPromotion{
		canonicalArtifact: canonical,
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "original" {
		t.Fatalf("untouched artifact changed during rollback: %q", data)
	}
}
