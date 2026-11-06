package account

import "testing"

func TestFormatBalance(t *testing.T) {
	for in, want := range map[string]string{
		"62500.0000000000": "62500",
		"12.5000":          "12.5",
		"0.0000":           "0",
		"-0.00":            "0",
		"100":              "100",
		" 3.10 ":           "3.1",
		"1e5":              "1e5",
		"unlimited":        "unlimited",
		"":                 "",
	} {
		if got := FormatBalance(in); got != want {
			t.Errorf("FormatBalance(%q) = %q, want %q", in, got, want)
		}
	}
}
