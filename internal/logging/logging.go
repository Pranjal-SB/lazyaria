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
	// Redact turns URL credential and query stripping on and off at runtime
	// (nil means on). Secrets such as tokens and passwords are always masked.
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
	urls := o.Redact
	if urls == nil {
		urls = new(atomic.Bool)
		urls.Store(true)
	}
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: o.Level,
		// slog walks groups itself and calls this for every member.
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr { return redactAttr(a, urls.Load()) },
	}))
}

var secretKey = regexp.MustCompile(`(?i)passw|secret|token|header|cookie|authorization`)

// redactAttr always masks secrets; urls also strips URL credentials and
// queries (privacy.redact_logs).
func redactAttr(a slog.Attr, urls bool) slog.Attr {
	if secretKey.MatchString(a.Key) {
		return slog.String(a.Key, "***")
	}
	str := func(s string) string { return redact(s, urls) }
	switch v := a.Value; v.Kind() {
	case slog.KindDuration: // JSON would print nanoseconds
		return slog.String(a.Key, v.Duration().String())
	case slog.KindString:
		return slog.String(a.Key, str(v.String()))
	case slog.KindAny:
		switch x := v.Any().(type) {
		case error:
			return slog.String(a.Key, str(x.Error()))
		case fmt.Stringer:
			return slog.String(a.Key, str(x.String()))
		case map[string]string:
			m := make(map[string]string, len(x))
			for k, val := range x {
				if secretKey.MatchString(k) {
					val = "***"
				}
				m[k] = str(val)
			}
			return slog.Any(a.Key, m)
		case []string:
			l := make([]string, len(x))
			for i, val := range x {
				l[i] = str(val)
			}
			return slog.Any(a.Key, l)
		default:
			// Anything else is logged as text if that text needed redacting.
			if s := fmt.Sprintf("%+v", x); str(s) != s {
				return slog.String(a.Key, str(s))
			}
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
func Redact(s string) string { return redact(s, true) }

func redact(s string, urls bool) string {
	s = tokenRe.ReplaceAllStringFunc(s, func(m string) string { return m[:len("token:")] + "***" })
	if !urls {
		return s
	}
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
