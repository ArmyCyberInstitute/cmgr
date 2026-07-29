package cmgr

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateFlagFormat(t *testing.T) {
	valid := []string{"flag{%s}", "%s", "prefix-%s-suffix"}
	for _, format := range valid {
		if err := validateFlagFormat(format); err != nil {
			t.Errorf("valid format %q was rejected: %v", format, err)
		}
	}

	invalid := []string{
		"",
		"flag",
		"flag{%08s}",
		"flag{%s-%s}",
		"flag{%s}-%d",
		"flag{%%-%s}",
		strings.Repeat("x", maxFlagFormatBytes) + "%s",
	}
	for _, format := range invalid {
		if err := validateFlagFormat(format); err == nil {
			t.Errorf("invalid format %q was accepted", format)
		}
	}
}

func TestMakeFlagUsesOnlyLiteralPlaceholder(t *testing.T) {
	build := &BuildMetadata{
		Challenge: "challenge",
		Format:    "flag{%s}",
		Seed:      7,
	}
	first := *build.makeFlag()
	second := *build.makeFlag()
	if first != second {
		t.Fatalf("deterministic flag changed: %q != %q", first, second)
	}
	if !strings.HasPrefix(first, "flag{") || !strings.HasSuffix(first, "}") {
		t.Fatalf("unexpected generated flag %q", first)
	}
}

func TestValidateSchemaDefinitionRejectsUnsafeShapes(t *testing.T) {
	if err := validateSchemaDefinition(nil); err == nil {
		t.Fatal("nil schema was accepted")
	}
	if err := validateSchemaDefinition(&Schema{
		Name:       manualSchemaPrefix + "forged",
		FlagFormat: "flag{%s}",
	}); err == nil {
		t.Fatal("reserved manual schema prefix was accepted")
	}
	if err := validateSchemaDefinition(&Schema{
		Name:       "negative",
		FlagFormat: "flag{%s}",
		Challenges: map[ChallengeId]BuildSpecification{
			"challenge": {Seeds: []int{1}, InstanceCount: -2},
		},
	}); err == nil {
		t.Fatal("invalid negative instance count was accepted")
	}
	if err := validateSchemaDefinition(&Schema{
		Name:       "duplicate",
		FlagFormat: "flag{%s}",
		Challenges: map[ChallengeId]BuildSpecification{
			"challenge": {Seeds: []int{1, 1}, InstanceCount: 0},
		},
	}); err == nil {
		t.Fatal("duplicate seed was accepted")
	}
}

func TestSchemaValidationDoesNotCapActiveInstances(t *testing.T) {
	err := validateSchemaDefinition(&Schema{
		Name:       "large-deployment",
		FlagFormat: "flag{%s}",
		Challenges: map[ChallengeId]BuildSpecification{
			"challenge": {
				Seeds:         []int{1},
				InstanceCount: 10_000,
			},
		},
	})
	if err != nil {
		t.Fatalf("large instance target was rejected: %v", err)
	}
}

func TestMergeRuntimeDefaultsAllowsChallengeOverrides(t *testing.T) {
	defaults := ContainerOptions{
		Cpus:      "1",
		Memory:    "512m",
		PidsLimit: 256,
		Ulimits:   []string{"nofile=4096:4096"},
	}
	merged := mergeRuntimeDefaults(defaults, ContainerOptions{
		Cpus:      "8",
		Memory:    "8g",
		PidsLimit: 4096,
		Ulimits:   []string{"nofile=65536:65536"},
	})
	if merged.Cpus != "8" || merged.Memory != "8g" ||
		merged.PidsLimit != 4096 ||
		len(merged.Ulimits) != 1 ||
		merged.Ulimits[0] != "nofile=65536:65536" {
		t.Fatalf("challenge override was not preserved: %#v", merged)
	}
}

func TestConvergeSchemaRechecksOwnershipUnderLock(t *testing.T) {
	manager := newSchemaTestManager(t)
	errs := manager.convergeSchema(&Schema{
		Name:       "deleted-before-convergence",
		FlagFormat: "flag{%s}",
	})
	if len(errs) != 1 {
		t.Fatalf("unexpected convergence errors: %v", errs)
	}
	var unknown *UnknownIdentifierError
	if !errors.As(errs[0], &unknown) {
		t.Fatalf("missing schema was not reported as an unknown identifier: %v", errs[0])
	}
}
