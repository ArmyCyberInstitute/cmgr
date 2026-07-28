// Package ociinterceptor modifies an OCI runtime configuration before passing
// it to the real runtime.
//
// The runtime-wrapper design and bundle discovery are adapted from
// picoCTF/oci-interceptor v0.2.2 at commit
// bcba3ad4a6f31be57659a9554a79fa5ad5efc7a6:
// https://github.com/picoCTF/oci-interceptor
//
// The original project is available under the Apache License 2.0. This file
// is a modified implementation for cmgr, also distributed under cmgr's
// Apache License 2.0.
package ociinterceptor

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	// RuntimeName is the Docker runtime name cmgr selects for containers that
	// need a built-in seccomp tweak.
	RuntimeName = "cmgr-oci-interceptor"

	// TweakEnvironmentVariable carries the requested tweaks from cmgr to the
	// interceptor. The interceptor removes it from the OCI process environment
	// before starting the container.
	TweakEnvironmentVariable = "CMGR_OCI_INTERCEPTOR_SECCOMP_TWEAKS"

	// RuntimeProtocolArgument is registered with Docker as a fixed runtime
	// argument. It makes stale or incorrectly targeted runtime registrations
	// fail instead of silently launching a container without its requested
	// tweaks.
	RuntimeProtocolArgument = "--cmgr-interceptor-protocol=seccomp-v1"

	TweakAllowDisableASLR = "allow-disable-aslr"
)

const maxOCIConfigSize = 16 * 1024 * 1024

// NormalizeTweaks validates, sorts, and copies a list of tweak names.
func NormalizeTweaks(tweaks []string) ([]string, error) {
	normalized := append([]string(nil), tweaks...)
	sort.Strings(normalized)
	for i, tweak := range normalized {
		if i > 0 && normalized[i-1] == tweak {
			return nil, fmt.Errorf("seccomp tweak %q is specified more than once", tweak)
		}
		switch tweak {
		case TweakAllowDisableASLR:
		default:
			return nil, fmt.Errorf("unsupported seccomp tweak %q", tweak)
		}
	}
	return normalized, nil
}

// FindBundlePath returns the OCI bundle passed to a runtime through runc's
// conventional -b or --bundle option.
func FindBundlePath(arguments []string) (string, bool) {
	for i, argument := range arguments {
		switch {
		case argument == "-b" || argument == "--bundle":
			if i+1 >= len(arguments) {
				return "", false
			}
			return arguments[i+1], true
		case strings.HasPrefix(argument, "-b="):
			return strings.TrimPrefix(argument, "-b="), true
		case strings.HasPrefix(argument, "--bundle="):
			return strings.TrimPrefix(argument, "--bundle="), true
		}
	}
	return "", false
}

// RewriteBundle applies any requested cmgr tweaks to config.json in bundle.
func RewriteBundle(bundle string) (bool, error) {
	configPath := filepath.Join(bundle, "config.json")
	info, err := os.Lstat(configPath)
	if err != nil {
		return false, fmt.Errorf("could not inspect OCI configuration: %v", err)
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("OCI configuration is not a regular file")
	}
	if info.Size() > maxOCIConfigSize {
		return false, fmt.Errorf("OCI configuration exceeds the %d byte size limit", maxOCIConfigSize)
	}

	original, err := ioutil.ReadFile(configPath)
	if err != nil {
		return false, fmt.Errorf("could not read OCI configuration: %v", err)
	}
	if len(original) > maxOCIConfigSize {
		return false, fmt.Errorf("OCI configuration exceeds the %d byte size limit", maxOCIConfigSize)
	}

	modified, changed, err := RewriteConfig(original)
	if err != nil || !changed {
		return changed, err
	}
	if err = replaceFile(configPath, modified, info.Mode().Perm()); err != nil {
		return false, fmt.Errorf("could not write OCI configuration: %v", err)
	}
	return true, nil
}

// RewriteConfig consumes the cmgr control environment variable and applies
// the requested changes while retaining unrecognized OCI fields.
func RewriteConfig(original []byte) ([]byte, bool, error) {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(original, &document); err != nil {
		return nil, false, fmt.Errorf("could not parse OCI configuration: %v", err)
	}

	processRaw, ok := document["process"]
	if !ok {
		return nil, false, fmt.Errorf("OCI configuration does not contain a process")
	}
	var process map[string]json.RawMessage
	if err := json.Unmarshal(processRaw, &process); err != nil {
		return nil, false, fmt.Errorf("could not parse OCI process configuration: %v", err)
	}

	tweaks, environment, requested, err := consumeTweakRequest(process["env"])
	if err != nil {
		return nil, false, err
	}
	if !requested {
		return original, false, nil
	}

	environmentJSON, err := json.Marshal(environment)
	if err != nil {
		return nil, false, fmt.Errorf("could not encode OCI process environment: %v", err)
	}
	process["env"] = environmentJSON
	processJSON, err := json.Marshal(process)
	if err != nil {
		return nil, false, fmt.Errorf("could not encode OCI process configuration: %v", err)
	}
	document["process"] = processJSON

	linuxRaw, ok := document["linux"]
	if !ok {
		return nil, false, fmt.Errorf("OCI configuration does not contain Linux settings")
	}
	var linux map[string]json.RawMessage
	if err = json.Unmarshal(linuxRaw, &linux); err != nil {
		return nil, false, fmt.Errorf("could not parse OCI Linux configuration: %v", err)
	}

	seccompRaw, hasSeccomp := linux["seccomp"]
	if !hasSeccomp || len(seccompRaw) == 0 || string(seccompRaw) == "null" {
		return nil, false, fmt.Errorf(
			"seccomp tweaks were requested but the OCI configuration does not contain an active seccomp profile",
		)
	}
	updatedSeccomp, updateErr := applyTweaks(seccompRaw, tweaks)
	if updateErr != nil {
		return nil, false, updateErr
	}
	linux["seccomp"] = updatedSeccomp
	linuxJSON, marshalErr := json.Marshal(linux)
	if marshalErr != nil {
		return nil, false, fmt.Errorf("could not encode OCI Linux configuration: %v", marshalErr)
	}
	document["linux"] = linuxJSON

	modified, err := json.Marshal(document)
	if err != nil {
		return nil, false, fmt.Errorf("could not encode OCI configuration: %v", err)
	}
	return modified, true, nil
}

func consumeTweakRequest(rawEnvironment json.RawMessage) ([]string, []string, bool, error) {
	if len(rawEnvironment) == 0 || string(rawEnvironment) == "null" {
		return nil, nil, false, nil
	}

	var environment []string
	if err := json.Unmarshal(rawEnvironment, &environment); err != nil {
		return nil, nil, false, fmt.Errorf("could not parse OCI process environment: %v", err)
	}

	prefix := TweakEnvironmentVariable + "="
	filtered := make([]string, 0, len(environment))
	request := ""
	requested := false
	requestCount := 0
	for _, variable := range environment {
		if strings.HasPrefix(variable, prefix) {
			request = strings.TrimPrefix(variable, prefix)
			requested = true
			requestCount++
			continue
		}
		filtered = append(filtered, variable)
	}
	if !requested {
		return nil, environment, false, nil
	}
	if request == "" {
		return nil, nil, false, fmt.Errorf("seccomp tweak request is empty")
	}
	if requestCount != 1 {
		return nil, nil, false, fmt.Errorf(
			"OCI configuration contains %d seccomp tweak requests; expected exactly one",
			requestCount,
		)
	}

	tweaks, err := NormalizeTweaks(strings.Split(request, ","))
	if err != nil {
		return nil, nil, false, err
	}
	return tweaks, filtered, true, nil
}

func applyTweaks(rawSeccomp json.RawMessage, tweaks []string) (json.RawMessage, error) {
	var seccomp map[string]json.RawMessage
	if err := json.Unmarshal(rawSeccomp, &seccomp); err != nil {
		return nil, fmt.Errorf("could not parse OCI seccomp configuration: %v", err)
	}

	var syscalls []json.RawMessage
	if rawSyscalls, ok := seccomp["syscalls"]; ok &&
		len(rawSyscalls) != 0 && string(rawSyscalls) != "null" {
		if err := json.Unmarshal(rawSyscalls, &syscalls); err != nil {
			return nil, fmt.Errorf("could not parse OCI seccomp syscall rules: %v", err)
		}
	}

	for _, tweak := range tweaks {
		switch tweak {
		case TweakAllowDisableASLR:
			rule, err := json.Marshal(map[string]interface{}{
				"names":  []string{"personality"},
				"action": "SCMP_ACT_ALLOW",
				"args": []map[string]interface{}{
					{
						"index": 0,
						"value": ^uint64(0x60008),
						"op":    "SCMP_CMP_MASKED_EQ",
					},
				},
			})
			if err != nil {
				return nil, fmt.Errorf("could not encode ASLR seccomp rule: %v", err)
			}
			syscalls = append(syscalls, rule)
		}
	}

	syscallsJSON, err := json.Marshal(syscalls)
	if err != nil {
		return nil, fmt.Errorf("could not encode OCI seccomp syscall rules: %v", err)
	}
	seccomp["syscalls"] = syscallsJSON
	updated, err := json.Marshal(seccomp)
	if err != nil {
		return nil, fmt.Errorf("could not encode OCI seccomp configuration: %v", err)
	}
	return updated, nil
}

func replaceFile(path string, contents []byte, mode os.FileMode) (err error) {
	temporary, err := ioutil.TempFile(filepath.Dir(path), ".cmgr-oci-config-")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() {
		temporary.Close()
		if err != nil {
			os.Remove(temporaryPath)
		}
	}()

	if err = temporary.Chmod(mode); err != nil {
		return err
	}
	if _, err = temporary.Write(contents); err != nil {
		return err
	}
	if err = temporary.Sync(); err != nil {
		return err
	}
	if err = temporary.Close(); err != nil {
		return err
	}
	if err = os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return nil
}
