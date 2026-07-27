package ociinterceptor

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/docker/docker/api/types"
)

func TestRegisterRuntimeMergesDockerConfiguration(t *testing.T) {
	directory, err := ioutil.TempDir("", "cmgr-register-runtime")
	if err != nil {
		t.Fatalf("could not create temporary directory: %s", err)
	}
	defer os.RemoveAll(directory)

	runtimePath := makeTestExecutable(t, directory)
	configPath := filepath.Join(directory, "daemon.json")
	original := []byte(`{
  "debug": true,
  "future-number": 18446744073709551615,
  "runtimes": {
    "other": {
      "path": "/usr/bin/other"
    }
  }
}`)
	if err = ioutil.WriteFile(configPath, original, 0600); err != nil {
		t.Fatalf("could not write Docker configuration: %s", err)
	}

	changed, err := RegisterRuntime(
		configPath,
		runtimePath,
		"/usr/bin/runc",
		false,
	)
	if err != nil {
		t.Fatalf("could not register runtime: %s", err)
	}
	if !changed {
		t.Fatal("new runtime registration was not reported as a change")
	}

	contents, err := ioutil.ReadFile(configPath)
	if err != nil {
		t.Fatalf("could not read updated Docker configuration: %s", err)
	}
	var document map[string]json.RawMessage
	if err = json.Unmarshal(contents, &document); err != nil {
		t.Fatalf("updated Docker configuration is invalid: %s", err)
	}
	if string(document["debug"]) != "true" {
		t.Fatalf("unrelated setting was not retained: %s", document["debug"])
	}
	if string(document["future-number"]) != "18446744073709551615" {
		t.Fatalf("large number was not retained exactly: %s", document["future-number"])
	}

	var runtimes map[string]json.RawMessage
	if err = json.Unmarshal(document["runtimes"], &runtimes); err != nil {
		t.Fatalf("could not decode runtimes: %s", err)
	}
	if _, ok := runtimes["other"]; !ok {
		t.Fatal("unrelated runtime was not retained")
	}
	var registration dockerRuntimeRegistration
	if err = json.Unmarshal(runtimes[RuntimeName], &registration); err != nil {
		t.Fatalf("could not decode cmgr runtime registration: %s", err)
	}
	expectedArguments := runtimeArguments("/usr/bin/runc")
	if registration.Path != runtimePath ||
		!reflect.DeepEqual(registration.RuntimeArgs, expectedArguments) {
		t.Fatalf("unexpected runtime registration: %#v", registration)
	}

	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("could not inspect updated Docker configuration: %s", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("configuration permissions changed to %o", info.Mode().Perm())
	}

	changed, err = RegisterRuntime(
		configPath,
		runtimePath,
		"/usr/bin/runc",
		false,
	)
	if err != nil {
		t.Fatalf("idempotent registration failed: %s", err)
	}
	if changed {
		t.Fatal("identical runtime registration was reported as a change")
	}
}

func TestRegisterRuntimeProtectsConflictingRegistration(t *testing.T) {
	directory, err := ioutil.TempDir("", "cmgr-register-runtime")
	if err != nil {
		t.Fatalf("could not create temporary directory: %s", err)
	}
	defer os.RemoveAll(directory)

	runtimePath := makeTestExecutable(t, directory)
	configPath := filepath.Join(directory, "daemon.json")
	if err = ioutil.WriteFile(
		configPath,
		[]byte(`{"runtimes":{"cmgr-oci-interceptor":{"path":"/old/cmgr","runtimeArgs":["oci-runtime"]}}}`),
		0644,
	); err != nil {
		t.Fatalf("could not write Docker configuration: %s", err)
	}

	_, err = RegisterRuntime(configPath, runtimePath, "/usr/bin/runc", false)
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("expected conflicting registration error, got: %v", err)
	}

	changed, err := RegisterRuntime(configPath, runtimePath, "/usr/bin/runc", true)
	if err != nil {
		t.Fatalf("could not force runtime registration: %s", err)
	}
	if !changed {
		t.Fatal("forced replacement was not reported as a change")
	}
}

func TestRegisterRuntimeCreatesConfiguration(t *testing.T) {
	directory, err := ioutil.TempDir("", "cmgr-register-runtime")
	if err != nil {
		t.Fatalf("could not create temporary directory: %s", err)
	}
	defer os.RemoveAll(directory)

	runtimePath := makeTestExecutable(t, directory)
	configPath := filepath.Join(directory, "docker", "daemon.json")
	changed, err := RegisterRuntime(configPath, runtimePath, "/usr/bin/runc", false)
	if err != nil {
		t.Fatalf("could not create Docker configuration: %s", err)
	}
	if !changed {
		t.Fatal("new configuration was not reported as a change")
	}
}

func TestRunRegisterCommandRegistersExplicitInterceptor(t *testing.T) {
	directory, err := ioutil.TempDir("", "cmgr-register-runtime")
	if err != nil {
		t.Fatalf("could not create temporary directory: %s", err)
	}
	defer os.RemoveAll(directory)

	interceptorPath := filepath.Join(directory, RuntimeName)
	if err = ioutil.WriteFile(interceptorPath, []byte("#!/bin/sh\n"), 0700); err != nil {
		t.Fatalf("could not create test interceptor: %s", err)
	}
	runcPath := filepath.Join(directory, "runc")
	if err = ioutil.WriteFile(runcPath, []byte("#!/bin/sh\n"), 0700); err != nil {
		t.Fatalf("could not create test OCI runtime: %s", err)
	}
	configPath := filepath.Join(directory, "daemon.json")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := RunRegisterCommand(
		[]string{
			"--config", configPath,
			"--runtime-path", interceptorPath,
			"--runc-path", runcPath,
			"--no-reload",
		},
		interceptorPath,
		&stdout,
		&stderr,
	)
	if exitCode != 0 {
		t.Fatalf(
			"registration command failed with %d: %s",
			exitCode,
			stderr.String(),
		)
	}
	if !strings.Contains(stdout.String(), "systemctl reload docker") {
		t.Fatalf("no-reload output did not provide reload command: %q", stdout.String())
	}

	contents, err := ioutil.ReadFile(configPath)
	if err != nil {
		t.Fatalf("could not read registered configuration: %s", err)
	}
	var document struct {
		Runtimes map[string]dockerRuntimeRegistration `json:"runtimes"`
	}
	if err = json.Unmarshal(contents, &document); err != nil {
		t.Fatalf("could not decode registered configuration: %s", err)
	}
	registration := document.Runtimes[RuntimeName]
	if registration.Path != interceptorPath {
		t.Fatalf("registered unexpected interceptor path %q", registration.Path)
	}
	if !reflect.DeepEqual(registration.RuntimeArgs, runtimeArguments(runcPath)) {
		t.Fatalf("registration has unexpected runtime arguments: %#v", registration.RuntimeArgs)
	}
}

func TestRuntimeRegistrationMatchesProtocolAndPath(t *testing.T) {
	expectedPath := "/usr/local/bin/cmgr-oci-interceptor"
	expectedArguments := runtimeArguments("/usr/bin/runc")
	info := types.Info{
		Runtimes: map[string]types.Runtime{
			RuntimeName: {
				Path: expectedPath,
				Args: expectedArguments,
			},
		},
	}
	if !runtimeRegistrationMatches(info, expectedPath, expectedArguments) {
		t.Fatal("matching runtime registration was rejected")
	}
	info.Runtimes[RuntimeName] = types.Runtime{Path: expectedPath}
	if runtimeRegistrationMatches(info, expectedPath, expectedArguments) {
		t.Fatal("registration without protocol arguments was accepted")
	}
}

func TestShellSafePath(t *testing.T) {
	if !shellSafePath("/usr/local/bin/runc") {
		t.Fatal("normal absolute path was rejected")
	}
	for _, path := range []string{
		"/usr/bin/runc;touch /tmp/pwned",
		"/usr/bin/runc $(id)",
		"/usr/bin/runc with-space",
	} {
		if shellSafePath(path) {
			t.Fatalf("unsafe runtime path was accepted: %q", path)
		}
	}
}

func makeTestExecutable(t *testing.T, directory string) string {
	t.Helper()
	path := filepath.Join(directory, "cmgr")
	if err := ioutil.WriteFile(path, []byte("#!/bin/sh\n"), 0700); err != nil {
		t.Fatalf("could not create test executable: %s", err)
	}
	return path
}
