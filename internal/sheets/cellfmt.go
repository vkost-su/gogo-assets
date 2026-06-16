package sheets

import (
	"fmt"
	"strings"
	"time"
)

// fmtDt renders a UTC time as "YYYY-MM-DD HH:MM" or "" if zero.
func fmtDt(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02 15:04")
}

// fmtDtRelative renders a UTC time as "YYYY-MM-DD HH:MM (Xd ago)".
func fmtDtRelative(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	t = t.UTC()
	days := int(time.Since(t).Hours() / 24)
	if days < 0 {
		days = 0
	}
	date := t.Format("2006-01-02 15:04")
	switch days {
	case 0:
		return date + " (today)"
	case 1:
		return date + " (yesterday)"
	default:
		return fmt.Sprintf("%s (%dd ago)", date, days)
	}
}

// fmtList joins a slice with "; ", returning "" for nil/empty.
func fmtList(items []string) string {
	if len(items) == 0 {
		return ""
	}
	return strings.Join(items, "; ")
}

// joinDot joins non-empty parts with " · ".
func joinDot(parts ...string) string {
	clean := parts[:0]
	for _, p := range parts {
		if p != "" {
			clean = append(clean, p)
		}
	}
	return strings.Join(clean, " · ")
}

// isPositiveInt reports whether a string parses to a positive integer.
func isPositiveInt(s string) bool {
	if s == "" {
		return false
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
		n = n*10 + int(r-'0')
	}
	return n > 0
}

// intGT parses an int and reports whether it is > limit.
func intGT(s string, limit int) bool {
	if s == "" {
		return false
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
		n = n*10 + int(r-'0')
	}
	return n > limit
}
