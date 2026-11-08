package engine

import (
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/Pranjal-SB/lazyaria/internal/textutil"
)

// AllowedOptions are the aria2 options a single download may set. Anything
// else (hooks, rpc-*, log paths) could run commands or weaken the daemon.
var AllowedOptions = []string{"split", "max-connection-per-server", "min-split-size", "max-download-limit", "max-upload-limit", "referer", "header", "user-agent", "checksum", "select-file", "seed-ratio", "seed-time", "out", "dir", "pause", "http-user", "http-passwd", "ftp-user", "ftp-passwd", "file-allocation"}

func checkOptions(opts map[string]string) error {
	for k := range opts {
		if !slices.Contains(AllowedOptions, k) {
			return ErrInvalidOption{Name: k}
		}
	}
	return nil
}

// SplitAuth strips userinfo from uris and moves it, or the explicit
// user/pass, into aria2's auth options so credentials never sit in a URI.
func SplitAuth(uris []string, user, pass string, opts map[string]string) []string {
	clean := make([]string, len(uris))
	var uu, up string
	for i, u := range uris {
		c, cu, cp := textutil.SplitUserinfo(u)
		clean[i] = c
		if uu == "" && up == "" {
			uu, up = cu, cp
		}
	}
	if user == "" && pass == "" {
		user, pass = uu, up
	}
	if user == "" && pass == "" {
		return clean
	}
	prefix := "http-"
	if len(clean) > 0 {
		if u, err := url.Parse(clean[0]); err == nil && (strings.EqualFold(u.Scheme, "ftp") || strings.EqualFold(u.Scheme, "sftp")) {
			prefix = "ftp-" // aria2 reads ftp-user/ftp-passwd for sftp too
		}
	}
	if user != "" {
		opts[prefix+"user"] = user
	}
	if pass != "" {
		opts[prefix+"passwd"] = pass
	}
	return clean
}

// checkRefreshURI applies the refresh-link rules: http/https/ftp/sftp only,
// parseable, under 8 KiB.
func checkRefreshURI(raw string) error {
	if len(raw) >= 8<<10 {
		return fmt.Errorf("the new link is longer than 8 KiB; paste the direct download link")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("the new link is not a valid URL; paste a full http(s), ftp or sftp link")
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "ftp", "sftp":
		return nil
	}
	return fmt.Errorf("links with scheme %q cannot be refreshed; use http(s), ftp or sftp", u.Scheme)
}
