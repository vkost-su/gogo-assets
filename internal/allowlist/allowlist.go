// Package allowlist implements the file-driven whitelist that purges known-good
// entries from collector output as early as possible.
//
// # Filter syntax (all *.filter files)
//
// One entry per line. Blank lines and lines starting with '#' are ignored.
//
//	entry      exact match (case-insensitive, trimmed)
//	entry*     prefix match (case-insensitive)
//
// jc-saas-owner.filter applies rules to the domain part of an email. Exact
// domain entries also match subdomains (user@mail.corp.com matches corp.com).
// Prefix rules (corp*) use the same * semantics on the domain label.
//
// Listed entries are purged from all downstream data. Empty files keep everything.
package allowlist

import (
	"bufio"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Standard filter file names under the baseline directory.
const (
	JCAppsFile        = "jc-apps.filter"
	JCSystemFile      = "jc-system.filter"
	LocalUsersMacFile = "jc-localusers-macos.filter"
	LocalUsersWinFile = "jc-localusers-windows.filter"
	JCSaaSOwnerFile   = "jc-saas-owner.filter"
	GWAppsFile        = "gw-apps.filter"
)

// Paths holds resolved paths to every filter file. Zero values fall back to
// defaults under baselineDir when passed to LoadFromPaths.
type Paths struct {
	BaselineDir   string
	JCApps        string
	JCSystem      string
	LocalUsersMac string
	LocalUsersWin string
	JCSaaSOwner   string
	GWApps        string
}

// Set bundles the allowlists loaded from one baseline directory.
// A nil *List or *DomainList in any field resolves nothing (keeps all).
type Set struct {
	Software      *List       // apps & extensions (jc-apps + jc-system, merged)
	LocalUsersMac *List       // macOS local accounts
	LocalUsersWin *List       // Windows local accounts
	SaaSOwner     *DomainList // SaaS owner-account email domains
	GWApps        *List       // GWS connected app display names
}

// LoadSet loads every filter from baselineDir using the standard file names.
func LoadSet(baselineDir string) (Set, error) {
	return LoadFromPaths(Paths{BaselineDir: baselineDir})
}

// LoadFromPaths loads every filter from p. Empty path fields default to
// BaselineDir/<standard filename>.
func LoadFromPaths(p Paths) (Set, error) {
	def := func(path, file string) string {
		if path != "" {
			return path
		}
		return filepath.Join(p.BaselineDir, file)
	}
	sw, err := Load(def(p.JCApps, JCAppsFile), def(p.JCSystem, JCSystemFile))
	if err != nil {
		return Set{}, err
	}
	mac, err := Load(def(p.LocalUsersMac, LocalUsersMacFile))
	if err != nil {
		return Set{}, err
	}
	win, err := Load(def(p.LocalUsersWin, LocalUsersWinFile))
	if err != nil {
		return Set{}, err
	}
	saas, err := LoadDomains(def(p.JCSaaSOwner, JCSaaSOwnerFile))
	if err != nil {
		return Set{}, err
	}
	gw, err := Load(def(p.GWApps, GWAppsFile))
	if err != nil {
		return Set{}, err
	}
	return Set{Software: sw, LocalUsersMac: mac, LocalUsersWin: win, SaaSOwner: saas, GWApps: gw}, nil
}

// Loaded reports whether any filter file contributed rules (non-empty lists).
func (s Set) Loaded() bool {
	return !s.Software.Empty() || !s.LocalUsersMac.Empty() || !s.LocalUsersWin.Empty() ||
		!s.SaaSOwner.Empty() || !s.GWApps.Empty()
}

// List matches names with the shared exact / prefix* syntax.
type List struct {
	m *matcher
}

// DomainList matches email addresses by domain using the same * rules on the
// domain label, plus subdomain suffix matching for exact domain entries.
type DomainList struct {
	suffixExact []string // domain.com — matches that host and *.domain.com
	prefix      *matcher // corp* — prefix on the domain label
}

// Load reads and merges name-list files into one List.
func Load(paths ...string) (*List, error) {
	l := &List{m: newMatcher()}
	for _, p := range paths {
		if err := l.loadFile(p); err != nil {
			return nil, err
		}
	}
	return l, nil
}

// LoadDomains reads and merges domain filter files into one DomainList.
func LoadDomains(paths ...string) (*DomainList, error) {
	d := &DomainList{prefix: newMatcher()}
	for _, p := range paths {
		if err := d.loadFile(p); err != nil {
			return nil, err
		}
	}
	return d, nil
}

func (l *List) loadFile(path string) error {
	return loadFilterFile(path, l.m.addLine)
}

func (d *DomainList) loadFile(path string) error {
	return loadFilterFile(path, d.addLine)
}

func loadFilterFile(path string, add func(string)) error {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		add(sc.Text())
	}
	return sc.Err()
}

func (d *DomainList) addLine(line string) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return
	}
	if p, ok := parsePrefixRule(line); ok {
		d.prefix.addPrefix(p)
		return
	}
	if dom, ok := parseExactDomain(line); ok {
		d.suffixExact = append(d.suffixExact, dom)
	}
}

// Allowed reports whether name matches the list (exact or prefix*).
func (l *List) Allowed(name string) bool {
	if l == nil {
		return false
	}
	return l.m.match(name)
}

// Allowed reports whether email's domain matches (exact+subdomain or prefix*).
func (d *DomainList) Allowed(email string) bool {
	if d == nil || d.Empty() {
		return false
	}
	email = strings.ToLower(strings.TrimSpace(email))
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return false
	}
	dom := email[at+1:]
	if d.prefix.match(dom) {
		return true
	}
	for _, label := range d.suffixExact {
		if dom == label || strings.HasSuffix(dom, "."+label) {
			return true
		}
	}
	return false
}

// Empty reports whether the list has no entries.
func (l *List) Empty() bool {
	return l == nil || l.m.empty()
}

// Empty reports whether the domain list has no entries.
func (d *DomainList) Empty() bool {
	return d == nil || (len(d.suffixExact) == 0 && d.prefix.empty())
}

// Len returns the number of distinct entries loaded.
func (l *List) Len() int {
	if l == nil {
		return 0
	}
	return l.m.len()
}

// Unresolved returns items not matched by the list, preserving order.
func Unresolved[T any](l *List, items []T, name func(T) string) []T {
	out := make([]T, 0, len(items))
	for _, it := range items {
		if !l.Allowed(name(it)) {
			out = append(out, it)
		}
	}
	return out
}
