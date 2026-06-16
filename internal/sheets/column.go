// Package sheets writes the asset inventory to Google Sheets.
//
// Layout per tab:
//
//	row 0     group headers (merged across consecutive same-group columns, dark blue)
//	row 1     column headers (light blue, bold)
//	row 2..N  data rows
//	row N+2   "Updated: <ts>" footer
//
// Cell colouring on data rows is driven by AlertRed / AlertYellow on each Column.
// A "gray row" header (e.g. "Suspended") greys out the whole row when its cell
// equals "Yes".
package sheets

// Column is one column in a tab's registry.
//
// Extract is invoked once per record and must never panic — return "" for
// missing data instead. AlertRed / AlertYellow may be nil; when set, they
// receive the already-Extracted string and decide if the cell should be
// painted red / yellow. Wrap=true enables WRAP + TOP-align (used for cells
// that contain multi-line content).
type Column[T any] struct {
	Group       string
	Header      string
	Extract     func(T) string
	AlertRed    func(string) bool
	AlertYellow func(string) bool
	Wrap        bool
}

// ── Shared formatting helpers ────────────────────────────────────────────────

// Bool maps optional booleans to "Yes" / "No" / "" — the convention used by
// all tabs. Pass a *bool so that "unknown" stays distinct from "false".
func Bool(b *bool) string {
	if b == nil {
		return ""
	}
	if *b {
		return "Yes"
	}
	return "No"
}

// BoolValue is Bool for non-pointer booleans (always known).
func BoolValue(b bool) string {
	if b {
		return "Yes"
	}
	return "No"
}
