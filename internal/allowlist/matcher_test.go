package allowlist

import "testing"

func TestParsePrefixRule(t *testing.T) {
	tests := []struct {
		line string
		want string
		ok   bool
	}{
		{"Google*", "google", true},
		{"_*", "_", true},
		{"@corp*", "corp", true},
		{"*@corp*", "corp", true},
		{"Google Chrome", "", false},
		{"*", "", false},
		{"  SLACK*  ", "slack", true},
	}
	for _, tt := range tests {
		got, ok := parsePrefixRule(tt.line)
		if ok != tt.ok || got != tt.want {
			t.Errorf("parsePrefixRule(%q) = %q, %v; want %q, %v", tt.line, got, ok, tt.want, tt.ok)
		}
	}
}

func TestParseExactDomain(t *testing.T) {
	for _, tc := range []struct {
		line, want string
	}{
		{"corp.io", "corp.io"},
		{"@corp.io", "corp.io"},
		{"*@corp.io", "corp.io"},
	} {
		got, ok := parseExactDomain(tc.line)
		if !ok || got != tc.want {
			t.Errorf("parseExactDomain(%q) = %q, %v; want %q, true", tc.line, got, ok, tc.want)
		}
	}
	if _, ok := parseExactDomain("corp*"); ok {
		t.Error("domain line with * should not parse as exact")
	}
}
