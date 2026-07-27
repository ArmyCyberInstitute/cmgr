package ociinterceptor

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindBundlePath(t *testing.T) {
	tests := []struct {
		name      string
		arguments []string
		expected  string
		found     bool
	}{
		{name: "short separate", arguments: []string{"create", "-b", "/bundle", "id"}, expected: "/bundle", found: true},
		{name: "short equals", arguments: []string{"create", "-b=/bundle", "id"}, expected: "/bundle", found: true},
		{name: "long separate", arguments: []string{"create", "--bundle", "/bundle", "id"}, expected: "/bundle", found: true},
		{name: "long equals", arguments: []string{"create", "--bundle=/bundle", "id"}, expected: "/bundle", found: true},
		{name: "missing value", arguments: []string{"create", "--bundle"}, found: false},
		{name: "not a bundle option", arguments: []string{"state", "id"}, found: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual, found := FindBundlePath(test.arguments)
			if actual != test.expected || found != test.found {
				t.Fatalf("FindBundlePath returned %q, %t; expected %q, %t", actual, found, test.expected, test.found)
			}
		})
	}
}

func TestRewriteConfigAllowsDisablingASLR(t *testing.T) {
	const original = `{
		"ociVersion": "1.2.1",
		"futureNumber": 18446744073709551615,
		"process": {
			"env": [
				"PATH=/usr/bin",
				"CMGR_OCI_INTERCEPTOR_SECCOMP_TWEAKS=allow-disable-aslr",
				"KEEP=value"
			],
			"futureProcessField": {"enabled": true}
		},
		"linux": {
			"futureLinuxField": "preserved",
			"seccomp": {
				"defaultAction": "SCMP_ACT_ERRNO",
				"futureSeccompField": [1, 2, 3],
				"syscalls": [
					{"names": ["read"], "action": "SCMP_ACT_ALLOW"}
				]
			}
		}
	}`

	modified, changed, err := RewriteConfig([]byte(original))
	if err != nil {
		t.Fatalf("RewriteConfig failed: %s", err)
	}
	if !changed {
		t.Fatal("RewriteConfig did not report a change")
	}

	var document map[string]json.RawMessage
	if err = json.Unmarshal(modified, &document); err != nil {
		t.Fatalf("modified OCI configuration is invalid: %s", err)
	}
	if string(document["futureNumber"]) != "18446744073709551615" {
		t.Fatalf("unknown numeric field was not preserved exactly: %s", document["futureNumber"])
	}

	var process map[string]json.RawMessage
	if err = json.Unmarshal(document["process"], &process); err != nil {
		t.Fatalf("could not decode process: %s", err)
	}
	var environment []string
	if err = json.Unmarshal(process["env"], &environment); err != nil {
		t.Fatalf("could not decode process environment: %s", err)
	}
	for _, variable := range environment {
		if strings.HasPrefix(variable, TweakEnvironmentVariable+"=") {
			t.Fatal("interceptor control variable was not removed")
		}
	}
	var futureProcessField struct {
		Enabled bool `json:"enabled"`
	}
	if err = json.Unmarshal(process["futureProcessField"], &futureProcessField); err != nil ||
		!futureProcessField.Enabled {
		t.Fatalf("unknown process field was not preserved: %s", process["futureProcessField"])
	}

	var linux map[string]json.RawMessage
	if err = json.Unmarshal(document["linux"], &linux); err != nil {
		t.Fatalf("could not decode Linux settings: %s", err)
	}
	if string(linux["futureLinuxField"]) != `"preserved"` {
		t.Fatalf("unknown Linux field was not preserved: %s", linux["futureLinuxField"])
	}
	var seccomp map[string]json.RawMessage
	if err = json.Unmarshal(linux["seccomp"], &seccomp); err != nil {
		t.Fatalf("could not decode seccomp settings: %s", err)
	}
	var futureSeccompField []int
	if err = json.Unmarshal(seccomp["futureSeccompField"], &futureSeccompField); err != nil ||
		len(futureSeccompField) != 3 ||
		futureSeccompField[0] != 1 ||
		futureSeccompField[1] != 2 ||
		futureSeccompField[2] != 3 {
		t.Fatalf("unknown seccomp field was not preserved: %s", seccomp["futureSeccompField"])
	}

	var rules []struct {
		Names  []string `json:"names"`
		Action string   `json:"action"`
		Args   []struct {
			Index uint   `json:"index"`
			Value uint64 `json:"value"`
			Op    string `json:"op"`
		} `json:"args"`
	}
	if err = json.Unmarshal(seccomp["syscalls"], &rules); err != nil {
		t.Fatalf("could not decode seccomp rules: %s", err)
	}
	found := false
	for _, rule := range rules {
		if len(rule.Names) == 1 && rule.Names[0] == "personality" &&
			rule.Action == "SCMP_ACT_ALLOW" && len(rule.Args) == 1 &&
			rule.Args[0].Index == 0 &&
			rule.Args[0].Value == ^uint64(0x60008) &&
			rule.Args[0].Op == "SCMP_CMP_MASKED_EQ" {
			found = true
		}
	}
	if !found {
		t.Fatal("ASLR personality rule was not added")
	}
}

func TestRewriteConfigWithoutRequestIsUnchanged(t *testing.T) {
	original := []byte(`{"process":{"env":["PATH=/usr/bin"]},"linux":{"seccomp":{"defaultAction":"SCMP_ACT_ERRNO"}}}`)
	modified, changed, err := RewriteConfig(original)
	if err != nil {
		t.Fatalf("RewriteConfig failed: %s", err)
	}
	if changed {
		t.Fatal("configuration without a request was changed")
	}
	if string(modified) != string(original) {
		t.Fatal("unchanged configuration was re-encoded")
	}
}

func TestRewriteConfigRejectsUnknownTweak(t *testing.T) {
	original := []byte(`{"process":{"env":["CMGR_OCI_INTERCEPTOR_SECCOMP_TWEAKS=allow-everything"]},"linux":{"seccomp":{"defaultAction":"SCMP_ACT_ERRNO"}}}`)
	_, _, err := RewriteConfig(original)
	if err == nil || !strings.Contains(err.Error(), "unsupported seccomp tweak") {
		t.Fatalf("expected unsupported tweak error, got: %v", err)
	}
}

func TestRewriteConfigRejectsDuplicateTweakRequests(t *testing.T) {
	original := []byte(`{
		"process": {
			"env": [
				"CMGR_OCI_INTERCEPTOR_SECCOMP_TWEAKS=allow-disable-aslr",
				"CMGR_OCI_INTERCEPTOR_SECCOMP_TWEAKS=allow-disable-aslr"
			]
		},
		"linux": {"seccomp": {"defaultAction": "SCMP_ACT_ERRNO"}}
	}`)
	_, _, err := RewriteConfig(original)
	if err == nil || !strings.Contains(err.Error(), "expected exactly one") {
		t.Fatalf("expected duplicate request error, got: %v", err)
	}
}

func TestRewriteConfigRequiresActiveSeccompProfile(t *testing.T) {
	tests := []string{
		`{"process":{"env":["CMGR_OCI_INTERCEPTOR_SECCOMP_TWEAKS=allow-disable-aslr"]},"linux":{}}`,
		`{"process":{"env":["CMGR_OCI_INTERCEPTOR_SECCOMP_TWEAKS=allow-disable-aslr"]},"linux":{"seccomp":null}}`,
	}
	for _, original := range tests {
		_, _, err := RewriteConfig([]byte(original))
		if err == nil || !strings.Contains(err.Error(), "active seccomp profile") {
			t.Fatalf("expected missing seccomp profile error, got: %v", err)
		}
	}
}

func TestRewriteBundleReplacesConfigAtomically(t *testing.T) {
	bundle, err := ioutil.TempDir("", "cmgr-oci-bundle")
	if err != nil {
		t.Fatalf("could not create temporary bundle: %s", err)
	}
	defer os.RemoveAll(bundle)

	configPath := filepath.Join(bundle, "config.json")
	original := []byte(`{"process":{"env":["CMGR_OCI_INTERCEPTOR_SECCOMP_TWEAKS=allow-disable-aslr"]},"linux":{"seccomp":{"defaultAction":"SCMP_ACT_ERRNO"}}}`)
	if err = ioutil.WriteFile(configPath, original, 0640); err != nil {
		t.Fatalf("could not write test OCI configuration: %s", err)
	}

	changed, err := RewriteBundle(bundle)
	if err != nil {
		t.Fatalf("RewriteBundle failed: %s", err)
	}
	if !changed {
		t.Fatal("RewriteBundle did not report a change")
	}
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("could not stat rewritten OCI configuration: %s", err)
	}
	if info.Mode().Perm() != 0640 {
		t.Fatalf("configuration permissions changed to %o", info.Mode().Perm())
	}
}
