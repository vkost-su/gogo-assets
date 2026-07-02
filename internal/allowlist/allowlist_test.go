package allowlist

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// app is a minimal stand-in for a software/extension record: the filter matches
// on the name only, so this keeps the package free of a collector import.
type app struct {
	Name    string
	Version string
}

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}

func TestAllowed(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "list.txt", "# a comment\n\nGoogle Chrome\n  Slack  \nzoom.us\n")
	l, err := Load(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	tests := []struct {
		give string
		want bool
	}{
		{give: "Google Chrome", want: true},     // exact
		{give: "google chrome", want: true},     // case-insensitive
		{give: "  GOOGLE CHROME  ", want: true}, // trimmed + case
		{give: "Slack", want: true},             // entry had surrounding spaces
		{give: "zoom.us", want: true},           // punctuation preserved
		{give: "# a comment", want: false},      // comment line not stored
		{give: "Firefox", want: false},          // unlisted
		{give: "", want: false},                 // empty
	}
	for _, tt := range tests {
		t.Run(tt.give, func(t *testing.T) {
			if got := l.Allowed(tt.give); got != tt.want {
				t.Errorf("Allowed(%q) = %v, want %v", tt.give, got, tt.want)
			}
		})
	}
}

func TestEmptyListKeepsAll(t *testing.T) {
	// A nil list and a list from a missing file both resolve nothing.
	for name, l := range map[string]*List{
		"nil":     nil,
		"missing": mustLoad(t, filepath.Join(t.TempDir(), "does-not-exist.txt")),
	} {
		t.Run(name, func(t *testing.T) {
			if !l.Empty() {
				t.Errorf("Empty() = false, want true")
			}
			if l.Allowed("anything") {
				t.Errorf("Allowed on empty list = true, want false")
			}
			items := []app{{Name: "A"}, {Name: "B"}}
			got := Unresolved(l, items, func(a app) string { return a.Name })
			if !reflect.DeepEqual(got, items) {
				t.Errorf("Unresolved on empty list = %v, want all %v", got, items)
			}
		})
	}
}

// TestUnresolvedDropsListedKeepsUnlisted is the Phase-1 exit criterion: adding a
// line to the allowlist shrinks the drift set (Unresolved) while the full set
// (the input) is unchanged; unlisted items always survive.
func TestUnresolvedDropsListedKeepsUnlisted(t *testing.T) {
	dir := t.TempDir()
	full := []app{
		{Name: "Google Chrome", Version: "1"},
		{Name: "Slack", Version: "2"},
		{Name: "Some Internal Tool", Version: "3"},
	}
	nameOf := func(a app) string { return a.Name }

	// Empty allowlist ⇒ drift set == full set.
	empty := writeFile(t, dir, "empty.txt", "# nothing yet\n")
	l0 := mustLoad(t, empty)
	if got := Unresolved(l0, full, nameOf); !reflect.DeepEqual(got, full) {
		t.Fatalf("empty allowlist: drift = %v, want full %v", got, full)
	}

	// Add two known-good names ⇒ they drop from the drift set; full unchanged.
	filled := writeFile(t, dir, "filled.txt", "google chrome\nSLACK\n")
	l1 := mustLoad(t, filled)
	wantDrift := []app{{Name: "Some Internal Tool", Version: "3"}}
	gotDrift := Unresolved(l1, full, nameOf)
	if !reflect.DeepEqual(gotDrift, wantDrift) {
		t.Errorf("filled allowlist: drift = %v, want %v", gotDrift, wantDrift)
	}
	if len(full) != 3 {
		t.Errorf("full view mutated: len = %d, want 3", len(full))
	}
}

func TestLoadMergesFiles(t *testing.T) {
	dir := t.TempDir()
	a := writeFile(t, dir, "software.txt", "Google Chrome\n")
	b := writeFile(t, dir, "system.txt", "sshd\n")
	// A third, absent path must be tolerated (empty), not error.
	l, err := Load(a, b, filepath.Join(dir, "absent.txt"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if l.Len() != 2 {
		t.Fatalf("Len() = %d, want 2", l.Len())
	}
	if !l.Allowed("Google Chrome") || !l.Allowed("sshd") {
		t.Errorf("merged list missing an entry: %+v", l)
	}
}

func mustLoad(t *testing.T, paths ...string) *List {
	t.Helper()
	l, err := Load(paths...)
	if err != nil {
		t.Fatalf("load %v: %v", paths, err)
	}
	return l
}

func TestAllowedDomain(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "domains.filter", `
# owner domains
superunlimited.com
@corp.io
*@partner.net
`)
	d, err := LoadDomains(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	tests := []struct {
		email string
		want  bool
	}{
		{"user@superunlimited.com", true},
		{"USER@SUPERUNLIMITED.COM", true},
		{"mail.superunlimited.com", false}, // not an email
		{"a@mail.superunlimited.com", true},
		{"bob@corp.io", true},
		{"x@sub.partner.net", true},
		{"other@example.com", false},
		{"not-an-email", false},
	}
	for _, tt := range tests {
		t.Run(tt.email, func(t *testing.T) {
			if got := d.Allowed(tt.email); got != tt.want {
				t.Errorf("Allowed(%q) = %v, want %v", tt.email, got, tt.want)
			}
		})
	}
}

func TestAllowedPrefixRule(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "apps.filter", "Google*\n")
	l, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Google Drive", "google chrome", "Firefox"} {
		got := l.Allowed(name)
		want := name != "Firefox"
		if got != want {
			t.Errorf("Allowed(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestLoadFromPathsDefaults(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, JCAppsFile, "Slack\n")
	writeFile(t, dir, JCSystemFile, "sshd\n")

	got, err := LoadFromPaths(Paths{BaselineDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Software.Allowed("Slack") || !got.Software.Allowed("sshd") {
		t.Errorf("merged software list missing entries: %+v", got.Software)
	}
}

func TestDomainPrefixWildcard(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "domains.filter", "super*\n")
	d, err := LoadDomains(p)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Allowed("user@superunlimited.com") {
		t.Error("super* should match superunlimited.com")
	}
	if d.Allowed("user@example.com") {
		t.Error("super* should not match example.com")
	}
}

func TestBareStarRejected(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "list.filter", "*\nGoogle Chrome\n")
	l, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if l.Allowed("anything") {
		t.Error("bare * must not match everything")
	}
	if !l.Allowed("Google Chrome") {
		t.Error("exact entries after bare * should still load")
	}
}
