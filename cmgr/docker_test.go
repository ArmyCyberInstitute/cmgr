package cmgr

import (
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

type dockerRoundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip dockerRoundTripFunc) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	return roundTrip(request)
}

func dockerTestResponse(
	request *http.Request,
	status int,
	body string,
) (*http.Response, error) {
	header := make(http.Header)
	header.Set("Content-Type", "application/json")
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    request,
	}, nil
}

func newDockerTestClient(
	t *testing.T,
	roundTrip dockerRoundTripFunc,
) *client.Client {
	t.Helper()
	cli, err := client.New(client.WithHTTPClient(&http.Client{
		Transport: roundTrip,
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cli.Close()
	})
	return cli
}

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

func TestInterruptedUpdateResourceNamesAreStrictlyRecognized(t *testing.T) {
	const qualifier = "cmgr-validate-0123456789abcdef0123456789abcdef"
	challenge := ChallengeId("example/challenge")

	imageTests := []struct {
		name    string
		tag     string
		wantID  BuildId
		wantHit bool
	}{
		{
			name:    "staged image",
			tag:     "example/challenge:" + qualifier + "-42-web",
			wantID:  42,
			wantHit: true,
		},
		{
			name: "canonical image",
			tag:  "example/challenge:42-web",
		},
		{
			name: "different challenge",
			tag:  "other/challenge:" + qualifier + "-42-web",
		},
		{
			name: "short random identifier",
			tag:  "example/challenge:cmgr-validate-0123-42-web",
		},
		{
			name: "invalid random identifier",
			tag:  "example/challenge:cmgr-validate-zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz-42-web",
		},
		{
			name: "missing host",
			tag:  "example/challenge:" + qualifier + "-42",
		},
	}
	for _, test := range imageTests {
		t.Run("image/"+test.name, func(t *testing.T) {
			buildID, found := stagedImageBuildID(test.tag, challenge)
			if found != test.wantHit || buildID != test.wantID {
				t.Fatalf(
					"stagedImageBuildID(%q) = (%d, %t), want (%d, %t)",
					test.tag,
					buildID,
					found,
					test.wantID,
					test.wantHit,
				)
			}
		})
	}

	artifactTests := []struct {
		name     string
		filename string
		wantID   BuildId
		wantHit  bool
	}{
		{
			name:     "staged artifacts",
			filename: "." + qualifier + "-42.tar.gz",
			wantID:   42,
			wantHit:  true,
		},
		{
			name:     "backup artifacts",
			filename: ".cmgr-old-" + qualifier + "-42.tar.gz",
			wantID:   42,
			wantHit:  true,
		},
		{
			name:     "canonical artifacts",
			filename: "42.tar.gz",
		},
		{
			name:     "near miss",
			filename: ".cmgr-old-" + qualifier + "-42.tar.gz.bak",
		},
	}
	for _, test := range artifactTests {
		t.Run("artifact/"+test.name, func(t *testing.T) {
			buildID, found := stagedArtifactBuildID(test.filename)
			if found != test.wantHit || buildID != test.wantID {
				t.Fatalf(
					"stagedArtifactBuildID(%q) = (%d, %t), want (%d, %t)",
					test.filename,
					buildID,
					found,
					test.wantID,
					test.wantHit,
				)
			}
		})
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

func TestStartupRecoveryRemovesMissingDynamicInstance(t *testing.T) {
	manager := newSchemaTestManager(t)
	insertConstraintChallenge(t, manager.db)
	insertConstraintBuild(t, manager.db)
	insertConstraintImage(t, manager.db)
	insertConstraintInstance(t, manager.db)
	requireExec(
		t,
		manager.db,
		"UPDATE builds SET instancecount=? WHERE id=1;",
		DYNAMIC_INSTANCES,
	)
	requireExec(
		t,
		manager.db,
		"INSERT INTO portAssignments(instance, name, port) VALUES (1, 'http', 31007);",
	)
	requireExec(
		t,
		manager.db,
		"INSERT INTO containers(instance, id) VALUES (1, 'missing');",
	)

	manager.ctx = t.Context()
	manager.cli = newDockerTestClient(t, func(
		request *http.Request,
	) (*http.Response, error) {
		switch {
		case request.Method == http.MethodGet &&
			strings.HasSuffix(request.URL.Path, "/containers/missing/json"):
			return dockerTestResponse(
				request,
				http.StatusNotFound,
				`{"message":"no such container"}`,
			)
		case request.Method == http.MethodDelete &&
			strings.HasSuffix(request.URL.Path, "/containers/missing"):
			return dockerTestResponse(
				request,
				http.StatusNotFound,
				`{"message":"no such container"}`,
			)
		case request.Method == http.MethodDelete &&
			strings.HasSuffix(request.URL.Path, "/networks/cmgr-1"):
			return dockerTestResponse(
				request,
				http.StatusNotFound,
				`{"message":"no such network"}`,
			)
		default:
			return dockerTestResponse(
				request,
				http.StatusInternalServerError,
				`{"message":"unexpected test request"}`,
			)
		}
	})

	if err := manager.retryRetiredResources(); err != nil {
		t.Fatalf("missing dynamic instance was not reconciled: %s", err)
	}
	requireRowCount(t, manager.db, "instances", 0)
	requireRowCount(t, manager.db, "containers", 0)
	requireRowCount(t, manager.db, "portAssignments", 0)
}

func TestStartupRecoveryRestoresFixedBuildCapacity(t *testing.T) {
	manager := newSchemaTestManager(t)
	insertConstraintChallenge(t, manager.db)
	insertConstraintBuild(t, manager.db)
	insertConstraintImage(t, manager.db)
	insertConstraintInstance(t, manager.db)
	requireExec(
		t,
		manager.db,
		"INSERT INTO containers(instance, id) VALUES (1, 'missing');",
	)

	manager.ctx = t.Context()
	manager.cli = newDockerTestClient(t, func(
		request *http.Request,
	) (*http.Response, error) {
		return dockerTestResponse(
			request,
			http.StatusNotFound,
			`{"message":"not found"}`,
		)
	})

	var replacements int
	err := manager.reconcileBrokenInstancesWith(
		[]InstanceId{1},
		func(build *BuildMetadata) (InstanceId, error) {
			replacements++
			if build.Id != 1 {
				t.Fatalf("unexpected replacement build: %d", build.Id)
			}
			return InstanceId(100 + replacements), nil
		},
	)
	if err != nil {
		t.Fatalf("fixed build capacity was not restored: %s", err)
	}
	if replacements != 1 {
		t.Fatalf("created %d replacement instances, want one", replacements)
	}
	requireRowCount(t, manager.db, "instances", 0)
	requireRowCount(t, manager.db, "containers", 0)
}

func TestStartupRecoveryPreservesStateOnTransientInspectFailure(t *testing.T) {
	manager := newSchemaTestManager(t)
	insertConstraintChallenge(t, manager.db)
	insertConstraintBuild(t, manager.db)
	insertConstraintImage(t, manager.db)
	insertConstraintInstance(t, manager.db)
	requireExec(
		t,
		manager.db,
		"INSERT INTO containers(instance, id) VALUES (1, 'unavailable');",
	)

	manager.ctx = t.Context()
	manager.cli = newDockerTestClient(t, func(
		request *http.Request,
	) (*http.Response, error) {
		return dockerTestResponse(
			request,
			http.StatusInternalServerError,
			`{"message":"daemon temporarily unavailable"}`,
		)
	})

	err := manager.retryRetiredResources()
	if err == nil || !strings.Contains(err.Error(), "inspect tracked container") {
		t.Fatalf("transient inspection failure was not reported: %v", err)
	}
	requireRowCount(t, manager.db, "instances", 1)
	requireRowCount(t, manager.db, "containers", 1)
}

func TestStartupRecoveryPreservesAllMetadataOnPartialCleanupFailure(t *testing.T) {
	manager := newSchemaTestManager(t)
	insertConstraintChallenge(t, manager.db)
	insertConstraintBuild(t, manager.db)
	insertConstraintImage(t, manager.db)
	requireExec(
		t,
		manager.db,
		"INSERT INTO images(id, build, host) VALUES (2, 1, 'database');",
	)
	insertConstraintInstance(t, manager.db)
	requireExec(
		t,
		manager.db,
		`INSERT INTO containers(instance, id)
		 VALUES (1, 'busy'), (1, 'missing');`,
	)

	manager.ctx = t.Context()
	manager.cli = newDockerTestClient(t, func(
		request *http.Request,
	) (*http.Response, error) {
		switch {
		case request.Method == http.MethodGet &&
			strings.HasSuffix(request.URL.Path, "/containers/busy/json"):
			return dockerTestResponse(
				request,
				http.StatusOK,
				`{"Id":"busy","State":{"Running":true,"Status":"running"}}`,
			)
		case request.Method == http.MethodGet &&
			strings.HasSuffix(request.URL.Path, "/containers/missing/json"):
			return dockerTestResponse(
				request,
				http.StatusNotFound,
				`{"message":"no such container"}`,
			)
		case request.Method == http.MethodDelete &&
			strings.HasSuffix(request.URL.Path, "/containers/busy"):
			return dockerTestResponse(
				request,
				http.StatusInternalServerError,
				`{"message":"container removal is temporarily unavailable"}`,
			)
		case request.Method == http.MethodDelete &&
			strings.HasSuffix(request.URL.Path, "/containers/missing"):
			return dockerTestResponse(
				request,
				http.StatusNotFound,
				`{"message":"no such container"}`,
			)
		default:
			return dockerTestResponse(
				request,
				http.StatusInternalServerError,
				`{"message":"unexpected test request"}`,
			)
		}
	})

	err := manager.retryRetiredResources()
	if err == nil ||
		!strings.Contains(err.Error(), "remove broken instance container busy") {
		t.Fatalf("partial cleanup failure was not reported: %v", err)
	}
	requireRowCount(t, manager.db, "instances", 1)
	requireRowCount(t, manager.db, "containers", 2)
}

func TestStartupRecoveryRejectsInspectionWithoutState(t *testing.T) {
	manager := newSchemaTestManager(t)
	insertConstraintChallenge(t, manager.db)
	insertConstraintBuild(t, manager.db)
	insertConstraintImage(t, manager.db)
	insertConstraintInstance(t, manager.db)
	requireExec(
		t,
		manager.db,
		"INSERT INTO containers(instance, id) VALUES (1, 'stateless');",
	)

	manager.ctx = t.Context()
	manager.cli = newDockerTestClient(t, func(
		request *http.Request,
	) (*http.Response, error) {
		return dockerTestResponse(request, http.StatusOK, `{}`)
	})

	err := manager.retryRetiredResources()
	if err == nil || !strings.Contains(err.Error(), "omitted runtime state") {
		t.Fatalf("missing inspection state was not reported: %v", err)
	}
	requireRowCount(t, manager.db, "instances", 1)
	requireRowCount(t, manager.db, "containers", 1)
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
