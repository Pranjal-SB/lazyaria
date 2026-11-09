// Package logging sets up the daemon's structured log: JSON lines in a
// rotated file, or on stderr for journald when run by systemd.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"sync/atomic"

	"gopkg.in/natefinch/lumberjack.v2"
)

// Options configure New. Zero values pick the spec defaults.
type Options struct {
	File    string
	MaxSize int64 // bytes before rotating; default 5 MiB
	Keep    int   // rotated files kept; default 3
	Level   slog.Leveler
	// Redact turns secret stripping on and off at runtime; nil means always on.
	Redact *atomic.Bool
}

// stderr is where logs go under systemd; tests swap it.
var stderr io.Writer = os.Stderr

// New returns the logger and the closer for its file. Under systemd
// (INVOCATION_ID is set) it writes to stderr so journald picks it up.
func New(o Options) (*slog.Logger, io.Closer) {
	if os.Getenv("INVOCATION_ID") != "" {
		return newLogger(stderr, o), io.NopCloser(nil)
	}
	if o.MaxSize <= 0 {
		o.MaxSize = 5 << 20
	}
	if o.Keep <= 0 {
		o.Keep = 3
	}
	lj := &lumberjack.Logger{
		Filename:   o.File,
		MaxSize:    int(max(1, (o.MaxSize+(1<<20)-1)>>20)), // lumberjack counts in MiB
		MaxBackups: o.Keep,
	}
	return newLogger(lj, o), lj
}

func newLogger(w io.Writer, o Options) *slog.Logger {
	on := o.Redact
	if on == nil {
		on = new(atomic.Bool)
		on.Store(true)
	}
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: o.Level,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if on.Load() {
				return redactAttr(a)
			}
			return a
		},
	}))
}

var secretKey = regexp.MustCompile(`(?i)passw|secret|token|header|cookie|authorization`)

func redactAttr(a slog.Attr) slog.Attr {
	if secretKey.MatchString(a.Key) {
		return slog.String(a.Key, "***")
	}
	switch v := a.Value; v.Kind() {
	case slog.KindString:
		return slog.String(a.Key, Redact(v.String()))
	case slog.KindAny:
		switch x := v.Any().(type) {
		case error:
			return slog.String(a.Key, Redact(x.Error()))
		case fmt.Stringer:
			return slog.String(a.Key, Redact(x.String()))
		}
	}
	return a
}

var (
	urlRe   = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.-]*://[^\s"'<>]+`)
	tokenRe = regexp.MustCompile(`(?i)\btoken:[^\s"',]+`)
)

// Redact strips credentials and query strings from URLs and masks aria2
// "token:" secrets in s.
func Redact(s string) string {
	s = tokenRe.ReplaceAllStringFunc(s, func(m string) string { return m[:len("token:")] + "***" })
	return urlRe.ReplaceAllStringFunc(s, func(u string) string {
		scheme, rest, _ := strings.Cut(u, "://")
		host, tail := rest, ""
		if i := strings.IndexAny(rest, "/?#"); i >= 0 {
			host, tail = rest[:i], rest[i:]
		}
		if i := strings.LastIndexByte(host, '@'); i >= 0 {
			host = host[i+1:]
		}
		tail, _, _ = strings.Cut(tail, "#")
		if p, _, ok := strings.Cut(tail, "?"); ok {
			tail = p + "?…"
		}
		return scheme + "://" + host + tail
	})
}
