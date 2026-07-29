package cmgr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadTestMetadataFile(
	t *testing.T,
	manager *Manager,
	name string,
	contents string,
) (*ChallengeMetadata, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if name == "problem.json" {
		return manager.loadJsonChallenge(path, info)
	}
	return manager.loadMarkdownChallenge(path, info)
}

func TestJSONChallengeRejectsUnknownModernFields(t *testing.T) {
	manager := &Manager{log: newLogger(DISABLED)}
	_, err := loadTestMetadataFile(
		t,
		manager,
		"problem.json",
		`{
			"name": "Strict",
			"challenge_type": "custom",
			"challange_options": {}
		}`,
	)
	if err == nil || !strings.Contains(err.Error(), "challange_options") {
		t.Fatalf("unexpected strict JSON error: %v", err)
	}
}

func TestJSONChallengeAcceptsDefinedLegacyHacksportFields(t *testing.T) {
	manager := &Manager{log: newLogger(DISABLED)}
	metadata, err := loadTestMetadataFile(
		t,
		manager,
		"problem.json",
		`{
			"name": "Legacy",
			"score": 25,
			"author": "Author",
			"pip_requirements": ["requests"]
		}`,
	)
	if err == nil || !strings.Contains(err.Error(), "challenge.py") {
		// Reaching the challenge.py validation proves the legacy JSON fields
		// passed strict decoding.
		t.Fatalf("legacy fields failed before hacksport validation: metadata=%#v err=%v", metadata, err)
	}
}

func TestJSONChallengeAcceptsKnownCatalogFieldsForModernTypes(t *testing.T) {
	for _, challengeType := range []string{"custom", "static-pybuild"} {
		t.Run(challengeType, func(t *testing.T) {
			manager := &Manager{log: newLogger(DISABLED)}
			metadata, err := loadTestMetadataFile(
				t,
				manager,
				"problem.json",
				`{
					"name": "Catalog compatibility",
					"challenge_type": "`+challengeType+`",
					"points": 25,
					"author": "Author",
					"event": "Event",
					"organization": "Organization",
					"score": 100,
					"section": "Section",
					"pkg_dependencies": ["package"]
				}`,
			)
			if err != nil {
				t.Fatalf("known catalog fields were rejected: %v", err)
			}
			if metadata.ChallengeType != challengeType {
				t.Fatalf("challenge type changed to %q", metadata.ChallengeType)
			}
			if metadata.Points != 25 {
				t.Fatalf("legacy score replaced points: %d", metadata.Points)
			}
			if len(metadata.Attributes) != 0 {
				t.Fatalf("legacy catalog fields became attributes: %#v", metadata.Attributes)
			}
		})
	}
}

func TestMarkdownChallengeOptionsRejectUnknownYAMLFields(t *testing.T) {
	manager := &Manager{log: newLogger(DISABLED)}
	_, err := loadTestMetadataFile(
		t,
		manager,
		"problem.md",
		`# Strict

- Type: custom

## Challenge Options

unknown_option: true
`,
	)
	if err == nil || !strings.Contains(err.Error(), "field unknown_option not found") {
		t.Fatalf("unexpected strict YAML error: %v", err)
	}
}

func TestMarkdownParserReturnsAllIndependentErrors(t *testing.T) {
	manager := &Manager{log: newLogger(DISABLED)}
	_, err := loadTestMetadataFile(
		t,
		manager,
		"problem.md",
		`# Broken

- Points: not-a-number
- Unknown: value

## Tags

not-a-list-item
`,
	)
	if err == nil {
		t.Fatal("malformed Markdown was accepted")
	}
	for _, expected := range []string{
		`strconv.Atoi`,
		`unrecognized top-level attribute`,
		`unexpected text in 'tags' section`,
	} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("combined error is missing %q: %v", expected, err)
		}
	}
}

func TestChallengeMetadataRejectsSymlinks(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "outside.md")
	if err := os.WriteFile(target, []byte("# Outside"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "problem.md")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	manager := &Manager{log: newLogger(DISABLED)}
	if _, err := manager.loadChallenge(link, info); err == nil ||
		!strings.Contains(err.Error(), "regular file") {
		t.Fatalf("symlinked challenge metadata was accepted: %v", err)
	}
}
