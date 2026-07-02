// Package logging configures slog for the inventory pipeline.
//
// Output format on stderr:
//
//	HH:MM:SS> LEVEL - <label> - message
//
// label is derived from a "module" attribute attached to the logger or from
// the call-site package name. Loggers obtained from For() are pre-tagged.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"
)

// _labels maps package-ish identifiers to short human display labels used in
// the log prefix. Unknown identifiers are passed through, capped at 9 chars.
var _labels = map[string]string{
	"main":      "main",
	"gws":       "GWS",
	"jc":        "JumpCloud",
	"sophos":    "Sophos",
	"sheets":    "Sheets",
	"inventory": "inventory",
	"config":    "config",
	"assemble":  "assemble",
	"snapshot":  "snapshot",
	"baseline":  "baseline",
	"drift":     "drift",
	"filter":    "filter",
}

// Configure installs the root slog handler with the given level
// ("DEBUG", "INFO", "WARN", "ERROR"). Subsequent calls replace the handler,
// so it is safe to call twice (once early, once after settings are loaded).
func Configure(level string) {
	slog.SetDefault(slog.New(newHandler(os.Stderr, parseLevel(level))))
}

// For returns a child logger pre-tagged with the given module label.
// The label is what appears in the "<label>" prefix on every line.
func For(module string) *slog.Logger {
	return slog.Default().With("module", module)
}

// Phase logs the start of a named pipeline phase and returns a done function.
//
// The banner uses ▶ / ✓ markers so the program flow is scannable in the log
// stream. Calling the returned function logs completion with elapsed time
// followed by any key/value pairs the caller passes (e.g. result counts):
//
//	done := logging.Phase(log, "collect", "target", target)
//	... do the work ...
//	done("users", len(users), "systems", len(systems))
//
// If a phase fails, log the error separately and simply don't call done — the
// absence of a ✓ line marks the phase that did not complete.
func Phase(log *slog.Logger, name string, args ...any) func(done ...any) {
	start := time.Now()
	log.Info("▶ "+name, args...)
	return func(done ...any) {
		log.Info("✓ "+name, append([]any{"elapsed", Elapsed(start)}, done...)...)
	}
}

// Elapsed returns a short human-readable duration since start.
// e.g. "12s", "3m 04s".
func Elapsed(start time.Time) string {
	d := time.Since(start).Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	mins := int(d / time.Minute)
	secs := int((d % time.Minute) / time.Second)
	return fmt.Sprintf("%dm %02ds", mins, secs)
}

func parseLevel(name string) slog.Level {
	switch strings.ToUpper(name) {
	case "DEBUG":
		return slog.LevelDebug
	case "WARN", "WARNING":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// handler renders one record per line in the project's canonical format.
type handler struct {
	w     io.Writer
	level slog.Level
	attrs []slog.Attr
	group string
}

func newHandler(w io.Writer, level slog.Level) *handler {
	return &handler{w: w, level: level}
}

func (h *handler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

func (h *handler) Handle(_ context.Context, r slog.Record) error {
	module := "main"
	for _, a := range h.attrs {
		if a.Key == "module" {
			module = a.Value.String()
		}
	}

	// Collect non-module attrs in declaration order.
	type kv struct{ k, v string }
	var kvs []kv
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "module" {
			module = a.Value.String()
			return true
		}
		kvs = append(kvs, kv{a.Key, formatAttr(a.Value)})
		return true
	})

	var tail strings.Builder
	for _, x := range kvs {
		tail.WriteString("  ")
		tail.WriteString(x.k)
		tail.WriteByte('=')
		tail.WriteString(x.v)
	}

	label := labelFor(module)
	t := r.Time.Format("15:04:05")
	line := fmt.Sprintf("%s> %s - %s - %s%s\n",
		t, r.Level.String(), label, r.Message, tail.String())
	_, err := io.WriteString(h.w, line)
	return err
}

// formatAttr renders an slog.Value the same way the eye would prefer to read it:
// strings as-is (quoted only if they contain spaces), numbers raw, others via %v.
func formatAttr(v slog.Value) string {
	switch v.Kind() {
	case slog.KindString:
		s := v.String()
		if s == "" {
			return `""`
		}
		if strings.ContainsAny(s, " \t") {
			return fmt.Sprintf("%q", s)
		}
		return s
	case slog.KindInt64:
		return fmt.Sprintf("%d", v.Int64())
	case slog.KindUint64:
		return fmt.Sprintf("%d", v.Uint64())
	case slog.KindFloat64:
		return fmt.Sprintf("%g", v.Float64())
	case slog.KindBool:
		if v.Bool() {
			return "true"
		}
		return "false"
	case slog.KindDuration:
		return v.Duration().String()
	default:
		return fmt.Sprintf("%v", v.Any())
	}
}

func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := *h
	clone.attrs = append(append([]slog.Attr(nil), h.attrs...), attrs...)
	return &clone
}

func (h *handler) WithGroup(name string) slog.Handler {
	clone := *h
	clone.group = name
	return &clone
}

func labelFor(module string) string {
	if l, ok := _labels[module]; ok {
		return l
	}
	if len(module) > 9 {
		return module[:9]
	}
	return module
}
