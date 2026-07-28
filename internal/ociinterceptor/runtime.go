// The runtime-wrapper design in this file is inspired by and adapted from
// picoCTF/oci-interceptor v0.2.2 at commit
// bcba3ad4a6f31be57659a9554a79fa5ad5efc7a6:
// https://github.com/picoCTF/oci-interceptor
//
// The original project is available under the Apache License 2.0. This file
// is a modified implementation for cmgr, also distributed under cmgr's
// Apache License 2.0.
package ociinterceptor

import (
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	runtimePathOption     = "--cmgr-runtime-path"
	runtimeProtocolOption = "--cmgr-interceptor-protocol"
)

// RunRuntime rewrites a requested OCI bundle and forwards the invocation to
// the real OCI runtime.
func RunRuntime(
	arguments []string,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
) int {
	runtimePath, runtimeArguments, err := parseRuntimeArguments(arguments)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %s\n", RuntimeName, err)
		return 1
	}

	if invocationRequiresRewrite(runtimeArguments) {
		bundle, bundleErr := findBundlePathStrict(runtimeArguments)
		if bundleErr != nil {
			fmt.Fprintf(stderr, "%s: %s\n", RuntimeName, bundleErr)
			return 1
		}
		changed := false
		if changed, err = RewriteBundle(bundle); err != nil {
			fmt.Fprintf(stderr, "%s: %s\n", RuntimeName, err)
			return 1
		}
		if !changed {
			fmt.Fprintf(
				stderr,
				"%s: OCI create/run invocation did not contain a seccomp tweak request\n",
				RuntimeName,
			)
			return 1
		}
	}

	command := exec.Command(runtimePath, runtimeArguments...)
	command.Stdin = stdin
	command.Stdout = stdout
	command.Stderr = stderr
	if err = command.Run(); err == nil {
		return 0
	}
	if exitError, ok := err.(*exec.ExitError); ok {
		return exitError.ExitCode()
	}
	fmt.Fprintf(stderr, "%s: could not invoke %s: %s\n", RuntimeName, runtimePath, err)
	return 1
}

func parseRuntimeArguments(arguments []string) (string, []string, error) {
	runtimePath := ""
	forwarded := make([]string, 0, len(arguments))
	protocolCount := 0
	runtimePathCount := 0
	for i := 0; i < len(arguments); i++ {
		argument := arguments[i]
		switch {
		case argument == RuntimeProtocolArgument:
			protocolCount++
		case argument == runtimeProtocolOption:
			return "", nil, fmt.Errorf("%s requires a value", runtimeProtocolOption)
		case strings.HasPrefix(argument, runtimeProtocolOption+"="):
			return "", nil, fmt.Errorf(
				"unsupported interceptor protocol argument %q",
				argument,
			)
		case argument == runtimePathOption:
			if i+1 >= len(arguments) {
				return "", nil, fmt.Errorf("%s requires a path", runtimePathOption)
			}
			i++
			runtimePath = arguments[i]
			runtimePathCount++
		case strings.HasPrefix(argument, runtimePathOption+"="):
			runtimePath = strings.TrimPrefix(argument, runtimePathOption+"=")
			runtimePathCount++
		default:
			forwarded = append(forwarded, argument)
		}
	}
	if protocolCount != 1 {
		return "", nil, fmt.Errorf(
			"expected exactly one %s argument, found %d; rerun %s",
			RuntimeProtocolArgument,
			protocolCount,
			RegistrationCommand,
		)
	}
	if runtimePathCount != 1 {
		return "", nil, fmt.Errorf(
			"expected exactly one %s argument, found %d; rerun %s",
			runtimePathOption,
			runtimePathCount,
			RegistrationCommand,
		)
	}
	if runtimePath == "" {
		return "", nil, fmt.Errorf("%s cannot be empty", runtimePathOption)
	}
	if !filepath.IsAbs(runtimePath) || !shellSafePath(runtimePath) {
		return "", nil, fmt.Errorf(
			"%s must name a safe absolute path",
			runtimePathOption,
		)
	}
	return runtimePath, forwarded, nil
}

// RuntimeRegistrationCompatible reports whether Docker is advertising the
// fail-closed runtime shape emitted by the registration command.
func RuntimeRegistrationCompatible(
	interceptorPath string,
	arguments []string,
) bool {
	if !filepath.IsAbs(interceptorPath) || !shellSafePath(interceptorPath) {
		return false
	}
	_, forwarded, err := parseRuntimeArguments(arguments)
	return err == nil && len(forwarded) == 0
}

func invocationRequiresRewrite(arguments []string) bool {
	for _, argument := range arguments {
		if argument == "create" || argument == "run" {
			return true
		}
	}
	return false
}

func findBundlePathStrict(arguments []string) (string, error) {
	bundle := ""
	count := 0
	for i, argument := range arguments {
		var candidate string
		switch {
		case argument == "-b" || argument == "--bundle":
			if i+1 >= len(arguments) || arguments[i+1] == "" {
				return "", fmt.Errorf("%s requires a bundle path", argument)
			}
			candidate = arguments[i+1]
		case strings.HasPrefix(argument, "-b="):
			candidate = strings.TrimPrefix(argument, "-b=")
		case strings.HasPrefix(argument, "--bundle="):
			candidate = strings.TrimPrefix(argument, "--bundle=")
		default:
			continue
		}
		if candidate == "" {
			return "", fmt.Errorf("bundle path cannot be empty")
		}
		bundle = candidate
		count++
	}
	if count != 1 {
		return "", fmt.Errorf(
			"OCI create/run invocation contains %d bundle paths; expected exactly one",
			count,
		)
	}
	return bundle, nil
}
