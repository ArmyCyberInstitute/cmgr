package cmgr

import "testing"

func TestParseNanoCPUs(t *testing.T) {
	tests := []struct {
		value string
		want  int64
	}{
		{value: "0", want: 0},
		{value: "0.001", want: 1_000_000},
		{value: "0.5", want: 500_000_000},
		{value: "1", want: 1_000_000_000},
		{value: "1.25", want: 1_250_000_000},
		{value: "3/2", want: 1_500_000_000},
		{value: "-0.5", want: -500_000_000},
	}

	for _, test := range tests {
		t.Run(test.value, func(t *testing.T) {
			got, err := parseNanoCPUs(test.value)
			if err != nil {
				t.Fatalf("parseNanoCPUs(%q) failed: %s", test.value, err)
			}
			if got != test.want {
				t.Fatalf("parseNanoCPUs(%q) = %d, want %d", test.value, got, test.want)
			}
		})
	}
}

func TestParseNanoCPUsRejectsInvalidValues(t *testing.T) {
	for _, value := range []string{
		"",
		"cpu",
		"0.0000000001",
		"9223372037",
	} {
		t.Run(value, func(t *testing.T) {
			if _, err := parseNanoCPUs(value); err == nil {
				t.Fatalf("parseNanoCPUs(%q) unexpectedly succeeded", value)
			}
		})
	}
}
