package cmgr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProcessDockerfileRejectsDuplicatePublishedPortName(t *testing.T) {
	manager, metadata := newPublishedPortTestChallenge(
		t,
		`FROM alpine AS web
# PUBLISH 8080 AS http
# PUBLISH 9090 AS http
# LAUNCH web
`,
	)

	err := manager.processDockerfile(metadata)
	if err == nil {
		t.Fatal("duplicate published-port name was accepted")
	}
	if !strings.Contains(err.Error(), "declared more than once") {
		t.Fatalf("unexpected duplicate-name error: %s", err)
	}
}

func TestProcessDockerfileRejectsPublishedEndpointAlias(t *testing.T) {
	manager, metadata := newPublishedPortTestChallenge(
		t,
		`FROM alpine AS web
# PUBLISH 8080 AS http
# PUBLISH 8080 AS admin
# LAUNCH web
`,
	)

	err := manager.processDockerfile(metadata)
	if err == nil {
		t.Fatal("published endpoint alias was accepted")
	}
	if !strings.Contains(err.Error(), "aliases 'http' at web:8080") {
		t.Fatalf("unexpected endpoint-alias error: %s", err)
	}
}

func TestProcessDockerfileAllowsSamePortOnDifferentHosts(t *testing.T) {
	manager, metadata := newPublishedPortTestChallenge(
		t,
		`FROM alpine AS web
# PUBLISH 8080 AS http
FROM alpine AS admin
# PUBLISH 8080 AS admin
# LAUNCH web admin
`,
	)

	if err := manager.processDockerfile(metadata); err != nil {
		t.Fatalf("host-distinct published ports were rejected: %s", err)
	}
	if len(metadata.PortMap) != 2 {
		t.Fatalf("unexpected published-port count %d", len(metadata.PortMap))
	}
	if metadata.PortMap["http"].Host != "web" {
		t.Fatalf("http port assigned to %q", metadata.PortMap["http"].Host)
	}
	if metadata.PortMap["admin"].Host != "admin" {
		t.Fatalf("admin port assigned to %q", metadata.PortMap["admin"].Host)
	}
}

func TestProcessDockerfileRejectsPortOnUnlaunchedHost(t *testing.T) {
	manager, metadata := newPublishedPortTestChallenge(
		t,
		`FROM alpine AS web
# PUBLISH 8080 AS http
FROM alpine AS worker
# LAUNCH worker
`,
	)

	err := manager.processDockerfile(metadata)
	if err == nil || !strings.Contains(err.Error(), "not marked for launching") {
		t.Fatalf("unexpected unlaunched-host error: %v", err)
	}
}

func TestProcessDockerfileRejectsExplicitPortOnUnlaunchedHost(t *testing.T) {
	manager, metadata := newPublishedPortTestChallenge(
		t,
		`FROM alpine AS web
FROM alpine AS worker
# LAUNCH worker
`,
	)
	metadata.PortMap = map[string]PortInfo{
		"http": {Host: "web", Port: 8080},
	}

	err := manager.processDockerfile(metadata)
	if err == nil || !strings.Contains(err.Error(), "which is not launched") {
		t.Fatalf("unexpected explicit-port error: %v", err)
	}
}

func TestProcessDockerfileRejectsDuplicateLaunchTarget(t *testing.T) {
	manager, metadata := newPublishedPortTestChallenge(
		t,
		`FROM alpine AS web
# LAUNCH web web
`,
	)

	err := manager.processDockerfile(metadata)
	if err == nil || !strings.Contains(err.Error(), "more than once") {
		t.Fatalf("unexpected duplicate-launch error: %v", err)
	}
}

func TestProcessDockerfileRejectsInvalidPublishDirectivePort(t *testing.T) {
	for _, port := range []string{"0", "65536"} {
		t.Run(port, func(t *testing.T) {
			manager, metadata := newPublishedPortTestChallenge(
				t,
				"FROM alpine AS web\n# PUBLISH "+port+" AS http\n# LAUNCH web\n",
			)
			err := manager.processDockerfile(metadata)
			if err == nil || !strings.Contains(err.Error(), "invalid container port") {
				t.Fatalf("invalid PUBLISH port %s produced %v", port, err)
			}
		})
	}
}

func TestAddChallengesPreservesConstraintErrorAndSuccessfulResults(t *testing.T) {
	manager := newSchemaTestManager(t)
	invalid := newAddChallengeTestMetadata(
		"invalid",
		map[string]PortInfo{
			"http":  {Host: "web", Port: 8080},
			"admin": {Host: "web", Port: 8080},
		},
	)
	valid := newAddChallengeTestMetadata(
		"valid",
		map[string]PortInfo{
			"http": {Host: "web", Port: 8080},
		},
	)

	added, errs := manager.addChallenges(
		[]*ChallengeMetadata{invalid, valid},
	)
	if len(errs) != 1 {
		t.Fatalf("unexpected add error count %d: %v", len(errs), errs)
	}
	if !strings.Contains(
		errs[0].Error(),
		"UNIQUE constraint failed: portNames.challenge, portNames.host, portNames.port",
	) {
		t.Fatalf("original constraint error was not preserved: %s", errs[0])
	}
	if strings.Contains(errs[0].Error(), "already been committed or rolled back") {
		t.Fatalf("constraint error was replaced by transaction state: %s", errs[0])
	}

	if len(added) != 1 || added[0] != valid {
		t.Fatalf("unexpected successfully added challenges: %#v", added)
	}

	var challengeIds []ChallengeId
	if err := manager.db.Select(
		&challengeIds,
		"SELECT id FROM challenges ORDER BY id;",
	); err != nil {
		t.Fatalf("failed to inspect added challenges: %s", err)
	}
	if len(challengeIds) != 1 || challengeIds[0] != valid.Id {
		t.Fatalf("failed challenge was not rolled back: %#v", challengeIds)
	}
}

func newPublishedPortTestChallenge(
	t *testing.T,
	dockerfile string,
) (*Manager, *ChallengeMetadata) {
	t.Helper()

	challengePath := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(challengePath, "Dockerfile"),
		[]byte(dockerfile),
		0600,
	); err != nil {
		t.Fatalf("failed to write test Dockerfile: %s", err)
	}

	return &Manager{log: newLogger(DISABLED)}, &ChallengeMetadata{
		Id:            "published-port-test",
		ChallengeType: "custom",
		Path:          challengePath,
	}
}

func newAddChallengeTestMetadata(
	id ChallengeId,
	portMap map[string]PortInfo,
) *ChallengeMetadata {
	return &ChallengeMetadata{
		Id:            id,
		Name:          string(id),
		ChallengeType: "custom",
		Path:          "/" + string(id),
		Hosts: []HostInfo{
			{Name: "web", Target: "web"},
		},
		PortMap: portMap,
	}
}
