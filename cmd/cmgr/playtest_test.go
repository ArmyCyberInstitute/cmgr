package main

import (
	"strings"
	"testing"

	"github.com/ArmyCyberInstitute/cmgr/cmgr"
)

func TestExpandPlaytestTextSanitizesChallengeAndLookupHTML(t *testing.T) {
	rendered := string(expandPlaytestText(
		`<strong>safe</strong><script>alert(1)</script>`+
			`<a href="javascript:alert(3)">bad link</a>`+
			`<svg onload="alert(4)"></svg>{{lookup("payload")}}`,
		"localhost",
		4242,
		&cmgr.BuildMetadata{
			LookupData: map[string]string{
				"payload": `<img src=x onerror="alert(2)">`,
			},
		},
		&cmgr.InstanceMetadata{},
	))
	if !strings.Contains(rendered, "<strong>safe</strong>") {
		t.Fatalf("safe markup was removed: %s", rendered)
	}
	for _, unsafe := range []string{
		"<script",
		"onerror",
		"javascript:",
		"<svg",
		"onload",
		"alert(1)",
		"alert(2)",
		"alert(3)",
		"alert(4)",
	} {
		if strings.Contains(strings.ToLower(rendered), unsafe) {
			t.Fatalf("unsafe content %q survived sanitization: %s", unsafe, rendered)
		}
	}
}
