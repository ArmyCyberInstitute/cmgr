package cmgr

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
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

func TestCloseBuildContextFileAllowsHTTPClientClose(t *testing.T) {
	contextFile, err := os.CreateTemp(t.TempDir(), "build-context-*")
	if err != nil {
		t.Fatal(err)
	}
	if err := contextFile.Close(); err != nil {
		t.Fatal(err)
	}

	if err := closeBuildContextFile(contextFile); err != nil {
		t.Fatalf("second close should tolerate os.ErrClosed: %v", err)
	}
}

func TestUnsupportedStorageQuotaError(t *testing.T) {
	if !unsupportedStorageQuotaError(errors.New(
		"--storage-opt is supported only for overlay over xfs with 'pquota' mount option",
	)) {
		t.Fatal("Docker pquota rejection was not recognized")
	}
	for _, err := range []error{
		nil,
		errors.New("image not found"),
		errors.New("invalid storage option value"),
	} {
		if unsupportedStorageQuotaError(err) {
			t.Fatalf("unrelated container-create error was treated as quota rejection: %v", err)
		}
	}
}

func TestInspectedPortAssignmentsRequiresRunningContainerAndEveryPort(
	t *testing.T,
) {
	httpPort, err := network.ParsePort("8080/tcp")
	if err != nil {
		t.Fatal(err)
	}
	expected := map[network.Port]string{httpPort: "http"}
	running := &container.State{
		Status:  container.StateRunning,
		Running: true,
	}

	tests := []struct {
		name       string
		inspection container.InspectResponse
		wantReady  bool
		wantErr    bool
	}{
		{
			name: "running with assignment",
			inspection: container.InspectResponse{
				State: running,
				NetworkSettings: &container.NetworkSettings{
					Ports: network.PortMap{
						httpPort: {{HostPort: "31007"}},
					},
				},
			},
			wantReady: true,
		},
		{
			name: "running before assignment",
			inspection: container.InspectResponse{
				State:           running,
				NetworkSettings: &container.NetworkSettings{},
			},
		},
		{
			name: "restart loop",
			inspection: container.InspectResponse{
				State: &container.State{
					Status:     container.StateRestarting,
					Restarting: true,
					ExitCode:   127,
				},
				NetworkSettings: &container.NetworkSettings{},
			},
			wantErr: true,
		},
		{
			name: "missing state",
			inspection: container.InspectResponse{
				NetworkSettings: &container.NetworkSettings{},
			},
			wantErr: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assignments, ready, err := inspectedPortAssignments(
				"container-id",
				expected,
				test.inspection,
			)
			if (err != nil) != test.wantErr {
				t.Fatalf("unexpected error: %v", err)
			}
			if ready != test.wantReady {
				t.Fatalf("ready=%t, want %t", ready, test.wantReady)
			}
			if ready && assignments["http"] != 31007 {
				t.Fatalf("unexpected assignments: %#v", assignments)
			}
		})
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

func TestChallengeNetworkDefaultsToNoEgress(t *testing.T) {
	const masqueradeOption = "com.docker.network.bridge.enable_ip_masquerade"
	options := challengeNetworkCreateOptions(NetworkOptions{})
	if options.Internal {
		t.Fatal("default challenge network suppresses published ingress")
	}
	if options.Options[masqueradeOption] != "false" {
		t.Fatal("default challenge network enables outbound masquerading")
	}
	options = challengeNetworkCreateOptions(NetworkOptions{AllowEgress: true})
	if _, disabled := options.Options[masqueradeOption]; disabled {
		t.Fatal("allow_egress challenge network disables masquerading")
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
