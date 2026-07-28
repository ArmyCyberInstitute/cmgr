package cmgr

import (
	"io"
	"strings"
	"testing"
)

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

func TestConsumeDockerProgressAcceptsSuccessfulStream(t *testing.T) {
	response := io.NopCloser(strings.NewReader(
		"{\"stream\":\"step one\\n\"}\n" +
			"{\"aux\":{\"ID\":\"sha256:abc\"}}\n",
	))
	if err := consumeDockerProgress(response, "test build"); err != nil {
		t.Fatalf("successful Docker response was rejected: %s", err)
	}
}

func TestConsumeDockerProgressReturnsStructuredError(t *testing.T) {
	response := io.NopCloser(strings.NewReader(
		"{\"stream\":\"Traceback: useful context\\n\"}\n" +
			"{\"error\":\"executor failed\",\"errorDetail\":{\"message\":\"specific failure\"}}\n",
	))
	err := consumeDockerProgress(response, "test build")
	if err == nil ||
		!strings.Contains(err.Error(), "specific failure") ||
		!strings.Contains(err.Error(), "Traceback: useful context") {
		t.Fatalf("expected structured Docker error, got %v", err)
	}
}

func TestConsumeDockerProgressRejectsMalformedResponse(t *testing.T) {
	response := io.NopCloser(strings.NewReader("{not-json}\n"))
	err := consumeDockerProgress(response, "test build")
	if err == nil || !strings.Contains(err.Error(), "failed to decode") {
		t.Fatalf("expected malformed-response error, got %v", err)
	}
}
