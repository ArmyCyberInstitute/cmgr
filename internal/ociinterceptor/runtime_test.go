package ociinterceptor

import (
	"bytes"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseRuntimeArguments(t *testing.T) {
	runtimePath, forwarded, err := parseRuntimeArguments([]string{
		RuntimeProtocolArgument,
		"--root", "/run/runc",
		"--cmgr-runtime-path=/usr/bin/runc",
		"create", "--bundle", "/bundle", "id",
	})
	if err != nil {
		t.Fatalf("parseRuntimeArguments failed: %s", err)
	}
	if runtimePath != "/usr/bin/runc" {
		t.Fatalf("unexpected runtime path: %q", runtimePath)
	}
	expected := []string{"--root", "/run/runc", "create", "--bundle", "/bundle", "id"}
	if !reflect.DeepEqual(forwarded, expected) {
		t.Fatalf("unexpected forwarded arguments: %#v", forwarded)
	}
}

func TestParseRuntimeArgumentsRequiresRuntimePath(t *testing.T) {
	_, _, err := parseRuntimeArguments([]string{
		RuntimeProtocolArgument,
		"state",
		"id",
	})
	if err == nil || !strings.Contains(err.Error(), runtimePathOption) {
		t.Fatalf("expected missing runtime path error, got: %v", err)
	}
}

func TestParseRuntimeArgumentsRequiresRuntimePathValue(t *testing.T) {
	_, _, err := parseRuntimeArguments([]string{
		RuntimeProtocolArgument,
		"--cmgr-runtime-path",
	})
	if err == nil || !strings.Contains(err.Error(), "requires a path") {
		t.Fatalf("expected missing runtime path error, got: %v", err)
	}
}

func TestParseRuntimeArgumentsRequiresProtocol(t *testing.T) {
	_, _, err := parseRuntimeArguments([]string{"state", "id"})
	if err == nil || !strings.Contains(err.Error(), RuntimeProtocolArgument) {
		t.Fatalf("expected missing protocol error, got: %v", err)
	}
}

func TestParseRuntimeArgumentsRejectsDuplicateOrRelativeRuntimePath(t *testing.T) {
	for _, arguments := range [][]string{
		{
			RuntimeProtocolArgument,
			runtimePathOption + "=/usr/bin/runc",
			runtimePathOption + "=/bin/runc",
		},
		{
			RuntimeProtocolArgument,
			runtimePathOption + "=runc",
		},
	} {
		if _, _, err := parseRuntimeArguments(arguments); err == nil {
			t.Fatalf("unsafe runtime arguments were accepted: %#v", arguments)
		}
	}
}

func TestRuntimeRegistrationCompatible(t *testing.T) {
	arguments := runtimeArguments("/usr/bin/runc")
	if !RuntimeRegistrationCompatible(
		"/usr/local/bin/cmgr-oci-interceptor",
		arguments,
	) {
		t.Fatal("registration command output was not considered compatible")
	}
	if RuntimeRegistrationCompatible("cmgr-oci-interceptor", arguments) {
		t.Fatal("relative interceptor path was considered compatible")
	}
	if RuntimeRegistrationCompatible("/usr/local/bin/cmgr interceptor", arguments) {
		t.Fatal("shell-unsafe interceptor path was considered compatible")
	}
}

func TestFindBundlePathStrict(t *testing.T) {
	bundle, err := findBundlePathStrict([]string{
		"create",
		"--bundle",
		"/bundle",
		"id",
	})
	if err != nil || bundle != "/bundle" {
		t.Fatalf("unexpected strict bundle result: %q, %v", bundle, err)
	}
	_, err = findBundlePathStrict([]string{
		"create",
		"--bundle=/one",
		"--bundle=/two",
		"id",
	})
	if err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("expected duplicate bundle error, got: %v", err)
	}
}

func TestRunRuntimeFailsClosedWithoutBundle(t *testing.T) {
	var stderr bytes.Buffer
	exitCode := RunRuntime(
		[]string{
			RuntimeProtocolArgument,
			runtimePathOption + "=/bin/true",
			"create",
			"id",
		},
		strings.NewReader(""),
		&bytes.Buffer{},
		&stderr,
	)
	if exitCode == 0 || !strings.Contains(stderr.String(), "bundle") {
		t.Fatalf("missing bundle did not fail closed: %d, %q", exitCode, stderr.String())
	}
}

func TestRunRuntimeRequiresTweakRequestForCreate(t *testing.T) {
	bundle, err := ioutil.TempDir("", "cmgr-runtime-bundle")
	if err != nil {
		t.Fatalf("could not create bundle: %s", err)
	}
	defer os.RemoveAll(bundle)
	if err = ioutil.WriteFile(
		filepath.Join(bundle, "config.json"),
		[]byte(`{"process":{"env":["PATH=/usr/bin"]},"linux":{"seccomp":{"defaultAction":"SCMP_ACT_ERRNO"}}}`),
		0600,
	); err != nil {
		t.Fatalf("could not write OCI configuration: %s", err)
	}

	var stderr bytes.Buffer
	exitCode := RunRuntime(
		[]string{
			RuntimeProtocolArgument,
			runtimePathOption + "=/bin/true",
			"create",
			"--bundle",
			bundle,
			"id",
		},
		strings.NewReader(""),
		&bytes.Buffer{},
		&stderr,
	)
	if exitCode == 0 || !strings.Contains(stderr.String(), "did not contain") {
		t.Fatalf("missing tweak request did not fail closed: %d, %q", exitCode, stderr.String())
	}
}

func TestRunRuntimeForwardsLifecycleCommand(t *testing.T) {
	var stderr bytes.Buffer
	exitCode := RunRuntime(
		[]string{
			RuntimeProtocolArgument,
			runtimePathOption + "=/bin/true",
			"state",
			"id",
		},
		strings.NewReader(""),
		&bytes.Buffer{},
		&stderr,
	)
	if exitCode != 0 {
		t.Fatalf("lifecycle command failed: %d, %q", exitCode, stderr.String())
	}
}
