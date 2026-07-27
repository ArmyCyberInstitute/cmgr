package cmgr

import "testing"

func TestBuildImageNamePreservesCanonicalConvention(t *testing.T) {
	build := &BuildMetadata{Id: 42}
	image := Image{Host: "web"}
	challenge := ChallengeId("example/challenge")
	canonical := buildImageName(challenge, build, image, "")
	if canonical != "example/challenge:42-web" {
		t.Fatalf("canonical image name changed: %q", canonical)
	}
	staged := buildImageName(
		challenge,
		build,
		image,
		"cmgr-validate-123",
	)
	if staged != "example/challenge:cmgr-validate-123-42-web" {
		t.Fatalf("unexpected staged image name: %q", staged)
	}
}

func TestSelectHostPortUsesPreferredAssignment(t *testing.T) {
	manager := new(Manager)
	hostPort, err := manager.selectHostPort(
		"http",
		map[string]int{"http": 31007},
	)
	if err != nil {
		t.Fatalf("could not select preferred host port: %s", err)
	}
	if hostPort != "31007" {
		t.Fatalf("selected unexpected host port %q", hostPort)
	}
}

func TestSelectHostPortRejectsInvalidPreferredAssignment(t *testing.T) {
	manager := new(Manager)
	if _, err := manager.selectHostPort(
		"http",
		map[string]int{"http": 65536},
	); err == nil {
		t.Fatal("invalid preferred host port was accepted")
	}
}
