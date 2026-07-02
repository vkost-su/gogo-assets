package sheets

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestClampCell(t *testing.T) {
	// Short content is returned untouched.
	if got := clampCell("hello"); got != "hello" {
		t.Errorf("short cell mutated: %q", got)
	}

	// Oversized content is truncated below the Sheets limit, marked, and stays
	// valid UTF-8 on a rune boundary.
	big := strings.Repeat("a", maxCellChars+5000)
	got := clampCell(big)
	if len(got) > maxCellChars {
		t.Errorf("clamped len = %d, want ≤ %d", len(got), maxCellChars)
	}
	if !strings.HasSuffix(got, _truncMarker) {
		t.Errorf("clamped cell missing truncation marker: %q", got[len(got)-40:])
	}
	if !utf8.ValidString(got) {
		t.Error("clamped cell is not valid UTF-8")
	}

	// A multi-byte rune straddling the cut must not be split.
	multi := strings.Repeat("é", maxCellChars) // 2 bytes each → well over the byte cap
	if !utf8.ValidString(clampCell(multi)) {
		t.Error("clamped multi-byte cell is not valid UTF-8")
	}
}
