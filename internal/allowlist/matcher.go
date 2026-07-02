package allowlist

import "strings"

// Filter line syntax (all *.filter files):
//
//   entry      exact match on the match key (case-insensitive, trimmed)
//   entry*     prefix match on the match key (case-insensitive)
//
// Match keys per file type:
//   jc-apps / jc-system / gw-apps  — app or display name
//   jc-localusers-*                — local username
//   jc-saas-owner                  — email domain part (see DomainList)
//
// Lines starting with '#' and blank lines are ignored. A lone "*" is rejected.

type matcher struct {
	exact    map[string]struct{}
	prefixes []string
}

func newMatcher() *matcher {
	return &matcher{exact: make(map[string]struct{})}
}

func (m *matcher) addPrefix(p string) {
	p = strings.ToLower(strings.TrimSpace(p))
	p = strings.TrimPrefix(p, "*@")
	p = strings.TrimPrefix(p, "@")
	if p == "" {
		return
	}
	m.prefixes = append(m.prefixes, p)
}

func (m *matcher) addLine(line string) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return
	}
	if p, ok := parsePrefixRule(line); ok {
		m.addPrefix(p)
		return
	}
	if e, ok := parseExactToken(line); ok {
		m.exact[e] = struct{}{}
	}
}

func (m *matcher) match(value string) bool {
	if m == nil || m.empty() {
		return false
	}
	n := strings.ToLower(strings.TrimSpace(value))
	if _, ok := m.exact[n]; ok {
		return true
	}
	for _, p := range m.prefixes {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	return false
}

func (m *matcher) empty() bool {
	return m == nil || (len(m.exact) == 0 && len(m.prefixes) == 0)
}

func (m *matcher) len() int {
	if m == nil {
		return 0
	}
	return len(m.exact) + len(m.prefixes)
}

// parsePrefixRule returns the lower-cased prefix for a line ending in '*'.
func parsePrefixRule(line string) (prefix string, ok bool) {
	low := strings.ToLower(strings.TrimSpace(line))
	if !strings.HasSuffix(low, "*") {
		return "", false
	}
	p, _ := strings.CutSuffix(low, "*")
	p = strings.TrimPrefix(p, "*@")
	p = strings.TrimPrefix(p, "@")
	if p == "" {
		return "", false
	}
	return p, true
}

// parseExactToken returns the lower-cased exact token (no trailing '*').
func parseExactToken(line string) (token string, ok bool) {
	low := strings.ToLower(strings.TrimSpace(line))
	if low == "" || strings.HasSuffix(low, "*") {
		return "", false
	}
	return low, true
}

// parseExactDomain returns a domain label for exact (suffix) domain rules.
// Accepts domain.com, @domain.com, and *@domain.com — all equivalent.
func parseExactDomain(line string) (domain string, ok bool) {
	low := strings.ToLower(strings.TrimSpace(line))
	if low == "" || strings.HasSuffix(low, "*") {
		return "", false
	}
	low = strings.TrimPrefix(low, "*@")
	low = strings.TrimPrefix(low, "@")
	if low == "" {
		return "", false
	}
	return low, true
}
