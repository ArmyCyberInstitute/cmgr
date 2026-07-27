package cmgr

import (
	"crypto/sha256"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ArmyCyberInstitute/cmgr/internal/ociinterceptor"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/system"
	"go.yaml.in/yaml/v3"
)

func TestSeccompOptionsYAML(t *testing.T) {
	var options ChallengeOptions
	err := yaml.Unmarshal([]byte(`
seccomp:
    tweaks:
        - allow-disable-aslr
`), &options)
	if err != nil {
		t.Fatalf("failed to decode seccomp options: %s", err)
	}
	if options.Seccomp == nil {
		t.Fatal("seccomp options were not decoded")
	}
	if len(options.Seccomp.Tweaks) != 1 || options.Seccomp.Tweaks[0] != seccompTweakAllowDisableASLR {
		t.Fatalf("unexpected seccomp tweaks: %#v", options.Seccomp.Tweaks)
	}
}

func TestSeccompChallengeFormatParity(t *testing.T) {
	const profile = `{"defaultAction":"SCMP_ACT_ERRNO","syscalls":[]}`
	tests := []struct {
		name       string
		yamlConfig string
		jsonConfig string
	}{
		{
			name: "legacy",
			yamlConfig: `seccomp:
    legacy: true`,
			jsonConfig: `"seccomp":{"legacy":true}`,
		},
		{
			name: "tweak",
			yamlConfig: `seccomp:
    tweaks:
        - allow-disable-aslr`,
			jsonConfig: `"seccomp":{"tweaks":["allow-disable-aslr"]}`,
		},
		{
			name: "custom profile",
			yamlConfig: `seccomp:
    profile: challenge-seccomp.json`,
			jsonConfig: `"seccomp":{"profile":"challenge-seccomp.json"}`,
		},
		{
			name: "host override",
			yamlConfig: `seccomp:
    tweaks:
        - allow-disable-aslr
overrides:
    worker:
        seccomp:
            legacy: true`,
			jsonConfig: `"seccomp":{"tweaks":["allow-disable-aslr"]},
"overrides":{"worker":{"seccomp":{"legacy":true}}}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			markdown := loadSeccompFormatChallenge(
				t,
				"markdown",
				test.yamlConfig,
				profile,
			)
			jsonChallenge := loadSeccompFormatChallenge(
				t,
				"json",
				test.jsonConfig,
				profile,
			)
			if !reflect.DeepEqual(
				markdown.ChallengeOptions,
				jsonChallenge.ChallengeOptions,
			) {
				t.Fatalf(
					"seccomp options differ by challenge format:\nmarkdown: %#v\njson: %#v",
					markdown.ChallengeOptions,
					jsonChallenge.ChallengeOptions,
				)
			}
		})
	}
}

func loadSeccompFormatChallenge(
	t *testing.T,
	format string,
	config string,
	profile string,
) *ChallengeMetadata {
	t.Helper()
	directory, err := ioutil.TempDir("", "cmgr-seccomp-format")
	if err != nil {
		t.Fatalf("could not create temporary challenge directory: %s", err)
	}
	t.Cleanup(func() { os.RemoveAll(directory) })

	if err = ioutil.WriteFile(
		filepath.Join(directory, "Dockerfile"),
		[]byte("FROM scratch AS worker\n# LAUNCH worker\n"),
		0600,
	); err != nil {
		t.Fatalf("could not write Dockerfile: %s", err)
	}
	if err = ioutil.WriteFile(
		filepath.Join(directory, "challenge-seccomp.json"),
		[]byte(profile),
		0600,
	); err != nil {
		t.Fatalf("could not write seccomp profile: %s", err)
	}

	var filename string
	var contents string
	switch format {
	case "markdown":
		filename = "problem.md"
		contents = fmt.Sprintf(`# Seccomp Format Parity

- Namespace: tests
- Type: custom

## Description

Format parity test.

## Details

Format parity test.

## Challenge Options

`+"```yaml\n%s\n```\n", config)
	case "json":
		filename = "problem.json"
		contents = fmt.Sprintf(`{
  "name": "Seccomp Format Parity",
  "namespace": "tests",
  "challenge_type": "custom",
  "description": "Format parity test.",
  "details": "Format parity test.",
  "challenge_options": {
    %s
  }
}`, config)
	default:
		t.Fatalf("unknown challenge format %q", format)
	}

	path := filepath.Join(directory, filename)
	if err = ioutil.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatalf("could not write challenge file: %s", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("could not inspect challenge file: %s", err)
	}
	manager := &Manager{log: newLogger(DISABLED)}
	metadata, err := manager.loadChallenge(path, info)
	if err != nil {
		t.Fatalf("could not load %s challenge: %s", format, err)
	}
	return metadata
}

func TestDefaultSeccompUsesDockerProfile(t *testing.T) {
	options := SeccompOptions{}
	if err := options.resolve("."); err != nil {
		t.Fatalf("empty seccomp options failed: %s", err)
	}
	if options.effectiveProfile != "" {
		t.Fatal("empty seccomp options should not override Docker's profile")
	}
	if options.ProfileHash != "" {
		t.Fatal("empty seccomp options should not have a profile hash")
	}
}

func TestLegacySeccompProfile(t *testing.T) {
	options := SeccompOptions{Legacy: true}
	if err := options.resolve("."); err != nil {
		t.Fatalf("legacy seccomp options failed: %s", err)
	}
	if options.effectiveProfile != seccompPolicy {
		t.Fatal("legacy mode did not retain the historical profile exactly")
	}
	if options.ProfileHash == "" {
		t.Fatal("legacy mode did not record a profile hash")
	}
}

func TestAllowDisableASLRTweak(t *testing.T) {
	options := SeccompOptions{Tweaks: []string{seccompTweakAllowDisableASLR}}
	if err := options.resolve("."); err != nil {
		t.Fatalf("ASLR seccomp tweak failed: %s", err)
	}
	if options.effectiveProfile != "" {
		t.Fatal("a built-in tweak should not replace Docker's live seccomp profile")
	}
	if options.ProfileHash != "" {
		t.Fatal("a built-in tweak should not have a static profile hash")
	}
	if len(options.Tweaks) != 1 || options.Tweaks[0] != seccompTweakAllowDisableASLR {
		t.Fatalf("unexpected normalized tweaks: %#v", options.Tweaks)
	}
}

func TestConfigureContainerSeccompTweaks(t *testing.T) {
	cConfig := container.Config{}
	hConfig := container.HostConfig{}
	options := &SeccompOptions{Tweaks: []string{seccompTweakAllowDisableASLR}}
	hostInfo := system.Info{
		OSType: "linux",
		Runtimes: map[string]system.RuntimeWithStatus{
			ociinterceptor.RuntimeName: {
				Runtime: system.Runtime{
					Path: "/usr/local/bin/cmgr-oci-interceptor",
					Args: []string{
						ociinterceptor.RuntimeProtocolArgument,
						"--cmgr-runtime-path=/usr/bin/runc",
					},
				},
			},
		},
	}

	if err := configureContainerSeccomp(&cConfig, &hConfig, options, hostInfo); err != nil {
		t.Fatalf("could not configure seccomp tweaks: %s", err)
	}
	if hConfig.Runtime != ociinterceptor.RuntimeName {
		t.Fatalf("unexpected OCI runtime: %q", hConfig.Runtime)
	}
	expectedEnvironment := ociinterceptor.TweakEnvironmentVariable + "=" + seccompTweakAllowDisableASLR
	if len(cConfig.Env) != 1 || cConfig.Env[0] != expectedEnvironment {
		t.Fatalf("unexpected interceptor environment: %#v", cConfig.Env)
	}
	if len(hConfig.SecurityOpt) != 0 {
		t.Fatalf("tweaks should not send a static seccomp profile: %#v", hConfig.SecurityOpt)
	}
}

func TestConfigureContainerSeccompRequiresInterceptor(t *testing.T) {
	cConfig := container.Config{}
	hConfig := container.HostConfig{}
	options := &SeccompOptions{Tweaks: []string{seccompTweakAllowDisableASLR}}

	err := configureContainerSeccomp(
		&cConfig,
		&hConfig,
		options,
		system.Info{OSType: "linux", Runtimes: map[string]system.RuntimeWithStatus{}},
	)
	if err == nil || !strings.Contains(err.Error(), ociinterceptor.RuntimeName) {
		t.Fatalf("expected missing interceptor runtime error, got: %v", err)
	}
	if !strings.Contains(err.Error(), ociinterceptor.RegistrationCommand) {
		t.Fatalf("missing interceptor error did not include registration command: %v", err)
	}
}

func TestSeccompRuntimeWarning(t *testing.T) {
	warning := seccompRuntimeWarning(
		system.Info{OSType: "linux", Runtimes: map[string]system.RuntimeWithStatus{}},
	)
	if !strings.Contains(warning, "seccomp tweaks are unavailable") {
		t.Fatalf("warning did not explain the unavailable capability: %q", warning)
	}
	if !strings.Contains(warning, ociinterceptor.RegistrationCommand) {
		t.Fatalf("warning did not include the registration command: %q", warning)
	}

	stale := seccompRuntimeWarning(system.Info{
		OSType: "linux",
		Runtimes: map[string]system.RuntimeWithStatus{
			ociinterceptor.RuntimeName: {
				Runtime: system.Runtime{
					Path: "/usr/local/bin/cmgr-oci-interceptor",
					Args: []string{ociinterceptor.RuntimeProtocolArgument},
				},
			},
		},
	})
	if stale == "" {
		t.Fatal("incomplete interceptor registration did not produce a warning")
	}

	available := seccompRuntimeWarning(system.Info{
		OSType: "linux",
		Runtimes: map[string]system.RuntimeWithStatus{
			ociinterceptor.RuntimeName: {
				Runtime: system.Runtime{
					Path: "/usr/local/bin/cmgr-oci-interceptor",
					Args: []string{
						ociinterceptor.RuntimeProtocolArgument,
						"--cmgr-runtime-path=/usr/bin/runc",
					},
				},
			},
		},
	})
	if available != "" {
		t.Fatalf("registered runtime produced warning: %q", available)
	}
	if warning := seccompRuntimeWarning(system.Info{OSType: "darwin"}); warning != "" {
		t.Fatalf("non-Linux daemon produced warning: %q", warning)
	}
}

func TestConfigureContainerSeccompCustomProfile(t *testing.T) {
	cConfig := container.Config{}
	hConfig := container.HostConfig{}
	options := &SeccompOptions{effectiveProfile: `{"defaultAction":"SCMP_ACT_ALLOW"}`}

	if err := configureContainerSeccomp(
		&cConfig,
		&hConfig,
		options,
		system.Info{OSType: "linux"},
	); err != nil {
		t.Fatalf("could not configure custom seccomp profile: %s", err)
	}
	if len(hConfig.SecurityOpt) != 1 ||
		hConfig.SecurityOpt[0] != "seccomp="+options.effectiveProfile {
		t.Fatalf("unexpected security options: %#v", hConfig.SecurityOpt)
	}
	if hConfig.Runtime != "" {
		t.Fatalf("custom profile unexpectedly selected runtime %q", hConfig.Runtime)
	}
}

func TestRequiredSeccompTweakOverlaysCustomProfile(t *testing.T) {
	base := &SeccompOptions{
		Profile:          "challenge-seccomp.json",
		ProfileHash:      "profile-hash",
		effectiveProfile: `{"defaultAction":"SCMP_ACT_ALLOW"}`,
	}
	combined, err := withRequiredSeccompTweaks(
		base,
		[]string{seccompTweakAllowDisableASLR},
	)
	if err != nil {
		t.Fatalf("could not merge required seccomp tweak: %s", err)
	}
	if combined == base {
		t.Fatal("required tweak merge mutated the configured options")
	}
	if combined.effectiveProfile != base.effectiveProfile {
		t.Fatal("required tweak merge discarded the configured profile")
	}
	if !reflect.DeepEqual(
		combined.Tweaks,
		[]string{seccompTweakAllowDisableASLR},
	) {
		t.Fatalf("unexpected merged tweaks: %#v", combined.Tweaks)
	}

	cConfig := container.Config{}
	hConfig := container.HostConfig{}
	hostInfo := system.Info{
		OSType: "linux",
		Runtimes: map[string]system.RuntimeWithStatus{
			ociinterceptor.RuntimeName: {
				Runtime: system.Runtime{
					Path: "/usr/local/bin/cmgr-oci-interceptor",
					Args: []string{
						ociinterceptor.RuntimeProtocolArgument,
						"--cmgr-runtime-path=/usr/bin/runc",
					},
				},
			},
		},
	}
	if err = configureContainerSeccomp(
		&cConfig,
		&hConfig,
		combined,
		hostInfo,
	); err != nil {
		t.Fatalf("could not configure overlaid profile: %s", err)
	}
	if hConfig.Runtime != ociinterceptor.RuntimeName {
		t.Fatalf("unexpected runtime %q", hConfig.Runtime)
	}
	if !reflect.DeepEqual(
		hConfig.SecurityOpt,
		[]string{"seccomp=" + base.effectiveProfile},
	) {
		t.Fatalf("configured profile was not retained: %#v", hConfig.SecurityOpt)
	}
}

func TestConsumeBuildSeccompTweaks(t *testing.T) {
	metadata := map[string]string{
		"flag":                        "flag{test}",
		buildMetadataSeccompTweaksKey: seccompTweakAllowDisableASLR,
	}
	tweaks, err := consumeBuildSeccompTweaks(metadata)
	if err != nil {
		t.Fatalf("could not consume build seccomp tweaks: %s", err)
	}
	if !reflect.DeepEqual(
		tweaks,
		SeccompTweakList{seccompTweakAllowDisableASLR},
	) {
		t.Fatalf("unexpected build seccomp tweaks: %#v", tweaks)
	}
	if _, present := metadata[buildMetadataSeccompTweaksKey]; present {
		t.Fatal("reserved build metadata was retained as lookup data")
	}

	invalid := map[string]string{
		buildMetadataSeccompTweaksKey: "unsupported",
	}
	if _, err = consumeBuildSeccompTweaks(invalid); err == nil {
		t.Fatal("unsupported build seccomp tweak was accepted")
	}
}

func TestCustomSeccompProfile(t *testing.T) {
	challengeDir, err := ioutil.TempDir("", "cmgr-seccomp")
	if err != nil {
		t.Fatalf("failed to create temporary challenge directory: %s", err)
	}
	defer os.RemoveAll(challengeDir)

	const profile = `{"defaultAction":"SCMP_ACT_ERRNO","syscalls":[]}`
	profilePath := filepath.Join(challengeDir, "custom.json")
	if err = ioutil.WriteFile(profilePath, []byte(profile), 0600); err != nil {
		t.Fatalf("failed to create custom profile: %s", err)
	}

	options := SeccompOptions{Profile: "custom.json"}
	if err = options.resolve(challengeDir); err != nil {
		t.Fatalf("custom seccomp profile failed: %s", err)
	}
	if options.effectiveProfile != profile {
		t.Fatal("custom profile was not snapshotted exactly")
	}
	if options.ProfileHash == "" {
		t.Fatal("custom profile did not record a profile hash")
	}
}

func TestSeccompProfileFilenameRules(t *testing.T) {
	valid := []string{"seccomp.json", "web.json", "worker-profile_2.json"}
	for _, filename := range valid {
		if err := validateSeccompProfileFilename(filename); err != nil {
			t.Errorf("valid filename %q was rejected: %s", filename, err)
		}
	}
	invalid := []string{
		".hidden.json",
		"profiles/web.json",
		"../web.json",
		"problem.json",
		"web",
		"web profile.json",
	}
	for _, filename := range invalid {
		if err := validateSeccompProfileFilename(filename); err == nil {
			t.Errorf("invalid filename %q was accepted", filename)
		}
	}
}

func TestSeccompProfileRejectsSymbolicLink(t *testing.T) {
	challengeDir, err := ioutil.TempDir("", "cmgr-seccomp")
	if err != nil {
		t.Fatalf("failed to create temporary challenge directory: %s", err)
	}
	defer os.RemoveAll(challengeDir)
	target := filepath.Join(challengeDir, "target.json")
	if err = ioutil.WriteFile(
		target,
		[]byte(`{"defaultAction":"SCMP_ACT_ALLOW"}`),
		0600,
	); err != nil {
		t.Fatalf("failed to create target profile: %s", err)
	}
	if err = os.Symlink(target, filepath.Join(challengeDir, "web.json")); err != nil {
		t.Fatalf("failed to create profile symlink: %s", err)
	}
	options := SeccompOptions{Profile: "web.json"}
	err = options.resolve(challengeDir)
	if err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("expected symbolic link error, got: %v", err)
	}
}

func TestSeccompProfilePathCannotEscapeChallenge(t *testing.T) {
	parent, err := ioutil.TempDir("", "cmgr-seccomp")
	if err != nil {
		t.Fatalf("failed to create temporary directory: %s", err)
	}
	defer os.RemoveAll(parent)

	challengeDir := filepath.Join(parent, "challenge")
	if err = os.Mkdir(challengeDir, 0700); err != nil {
		t.Fatalf("failed to create challenge directory: %s", err)
	}
	if err = ioutil.WriteFile(
		filepath.Join(parent, "outside.json"),
		[]byte(`{"defaultAction":"SCMP_ACT_ALLOW"}`),
		0600,
	); err != nil {
		t.Fatalf("failed to create outside profile: %s", err)
	}

	options := SeccompOptions{Profile: "../outside.json"}
	err = options.resolve(challengeDir)
	if err == nil || !strings.Contains(err.Error(), "challenge directory") {
		t.Fatalf("expected path escape error, got: %v", err)
	}
}

func TestInvalidCustomSeccompProfiles(t *testing.T) {
	tests := []struct {
		name    string
		profile string
		match   string
	}{
		{
			name:    "invalid JSON",
			profile: `{`,
			match:   "invalid seccomp profile JSON",
		},
		{
			name:    "missing default action",
			profile: `{"syscalls":[]}`,
			match:   "defaultAction",
		},
		{
			name:    "missing syscall names",
			profile: `{"defaultAction":"SCMP_ACT_ERRNO","syscalls":[{"action":"SCMP_ACT_ALLOW"}]}`,
			match:   "does not specify any names",
		},
		{
			name:    "missing syscall action",
			profile: `{"defaultAction":"SCMP_ACT_ERRNO","syscalls":[{"names":["read"]}]}`,
			match:   "does not specify an action",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateSeccompProfile(test.profile)
			if err == nil || !strings.Contains(err.Error(), test.match) {
				t.Fatalf("expected error containing %q, got: %v", test.match, err)
			}
		})
	}
}

func TestInvalidSeccompOptions(t *testing.T) {
	tests := []struct {
		name    string
		options SeccompOptions
		match   string
	}{
		{
			name: "conflicting modes",
			options: SeccompOptions{
				Legacy:  true,
				Tweaks:  []string{seccompTweakAllowDisableASLR},
				Profile: "custom.json",
			},
			match: "mutually exclusive",
		},
		{
			name:    "unsupported tweak",
			options: SeccompOptions{Tweaks: []string{"allow-everything"}},
			match:   "unsupported seccomp tweak",
		},
		{
			name: "duplicate tweak",
			options: SeccompOptions{Tweaks: []string{
				seccompTweakAllowDisableASLR,
				seccompTweakAllowDisableASLR,
			}},
			match: "specified more than once",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.options.resolve(".")
			if err == nil || !strings.Contains(err.Error(), test.match) {
				t.Fatalf("expected error containing %q, got: %v", test.match, err)
			}
		})
	}
}

func TestSeccompOptionsDatabaseSerialization(t *testing.T) {
	original := &SeccompOptions{
		Tweaks: []string{seccompTweakAllowDisableASLR},
	}
	data, err := marshalSeccompOptions(original)
	if err != nil {
		t.Fatalf("failed to serialize seccomp options: %s", err)
	}
	restored, err := unmarshalSeccompOptions(data)
	if err != nil {
		t.Fatalf("failed to deserialize seccomp options: %s", err)
	}
	if restored == nil ||
		len(restored.Tweaks) != 1 ||
		restored.Tweaks[0] != seccompTweakAllowDisableASLR ||
		restored.ProfileHash != "" ||
		restored.effectiveProfile != "" {
		t.Fatalf("seccomp options did not survive serialization: %#v", restored)
	}
}

func TestCustomSeccompProfileDatabaseSerialization(t *testing.T) {
	const profile = `{"defaultAction":"SCMP_ACT_ERRNO","syscalls":[]}`
	sum := sha256.Sum256([]byte(profile))
	original := &SeccompOptions{
		Profile:          "web.json",
		ProfileHash:      fmt.Sprintf("%x", sum),
		effectiveProfile: profile,
	}
	data, err := marshalSeccompOptions(original)
	if err != nil {
		t.Fatalf("failed to serialize custom seccomp profile: %s", err)
	}
	restored, err := unmarshalSeccompOptions(data)
	if err != nil {
		t.Fatalf("failed to deserialize custom seccomp profile: %s", err)
	}
	if restored == nil ||
		restored.Profile != original.Profile ||
		restored.ProfileHash != original.ProfileHash ||
		restored.effectiveProfile != profile {
		t.Fatalf("custom seccomp profile did not survive serialization: %#v", restored)
	}
}

func TestPersistedSeccompProfileRequiresValidSnapshot(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{
			name: "legacy without snapshot",
			data: `{"options":{"legacy":true}}`,
		},
		{
			name: "custom without snapshot",
			data: `{"options":{"profile":"web.json"}}`,
		},
		{
			name: "mismatched hash",
			data: `{"options":{"profile":"web.json","profile_hash":"wrong"},"effective_profile":"{\"defaultAction\":\"SCMP_ACT_ALLOW\"}"}`,
		},
		{
			name: "snapshot in default mode",
			data: `{"options":{},"effective_profile":"{\"defaultAction\":\"SCMP_ACT_ALLOW\"}"}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := unmarshalSeccompOptions(test.data); err == nil {
				t.Fatal("invalid persisted seccomp options were accepted")
			}
		})
	}
}

func TestEffectiveContainerOptionsSeccompInheritance(t *testing.T) {
	global := &SeccompOptions{Profile: "global.json"}
	worker := &SeccompOptions{Profile: "worker.json"}
	options := map[string]ContainerOptions{
		"": {
			Seccomp: global,
		},
		"web": {
			Memory: "128m",
		},
		"worker": {
			Seccomp: worker,
		},
	}

	webOptions, ok := effectiveContainerOptions(options, "web")
	if !ok || webOptions.Seccomp != global {
		t.Fatal("host without an explicit seccomp policy did not inherit the challenge policy")
	}
	workerOptions, ok := effectiveContainerOptions(options, "worker")
	if !ok || workerOptions.Seccomp != worker {
		t.Fatal("host-specific seccomp policy did not replace the challenge policy")
	}

	specificOnly := map[string]ContainerOptions{
		"worker": {Seccomp: worker},
	}
	if other, ok := effectiveContainerOptions(specificOnly, "web"); ok || other.Seccomp != nil {
		t.Fatal("unconfigured host did not retain Docker's default policy")
	}
}

func TestSeccompPolicyFingerprintIncludesPerHostProfiles(t *testing.T) {
	left := ChallengeOptions{Overrides: map[string]ContainerOptions{
		"web": {Seccomp: &SeccompOptions{Profile: "web.json", ProfileHash: "one"}},
	}}
	right := ChallengeOptions{Overrides: map[string]ContainerOptions{
		"web": {Seccomp: &SeccompOptions{Profile: "web.json", ProfileHash: "two"}},
	}}
	if seccompPoliciesEqual(left, right) {
		t.Fatal("different per-host profile hashes were considered equal")
	}

	withExplicitDefault := ChallengeOptions{Overrides: map[string]ContainerOptions{
		"": {
			Seccomp: &SeccompOptions{Profile: "global.json", ProfileHash: "one"},
		},
		"web": {Seccomp: &SeccompOptions{}},
	}}
	withoutExplicitDefault := ChallengeOptions{Overrides: map[string]ContainerOptions{
		"": {
			Seccomp: &SeccompOptions{Profile: "global.json", ProfileHash: "one"},
		},
	}}
	if seccompPoliciesEqual(withExplicitDefault, withoutExplicitDefault) {
		t.Fatal("an explicit per-host Docker-default policy was ignored")
	}
}
