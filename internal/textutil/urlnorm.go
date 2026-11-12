package textutil

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
)

var defaultPorts = map[string]string{"http": "80", "https": "443", "ftp": "21", "sftp": "22"}

// NormalizeURL gives equivalent URLs the same string so duplicates can be spotted.
func NormalizeURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("not an absolute url: %q", raw)
	}
	u.Scheme = strings.ToLower(u.Scheme)
	host, port := strings.ToLower(u.Hostname()), u.Port()
	if port == defaultPorts[u.Scheme] {
		port = ""
	}
	if port != "" {
		u.Host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		u.Host = "[" + host + "]"
	} else {
		u.Host = host
	}
	u.Fragment = ""
	// A query that does not parse cleanly (a=1;b=2, bare keys) stays as written.
	if q, err := url.ParseQuery(u.RawQuery); err == nil && wellFormed(u.RawQuery) {
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}

// userinfoRe matches the scheme and userinfo of a URL url.Parse rejects.
var userinfoRe = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9+.-]*://)[^/?#]*@`)

// StripUserinfo replaces the userinfo of a URL that url.Parse rejects, and
// its "@", with mask.
func StripUserinfo(raw, mask string) string {
	m := userinfoRe.FindStringSubmatchIndex(raw)
	if m == nil {
		return raw
	}
	return raw[:m[3]] + mask + raw[m[1]:]
}

// SplitUserinfo pulls credentials out of a URL so they never get logged or stored with it.
func SplitUserinfo(raw string) (clean string, user, pass string) {
	u, err := url.Parse(raw)
	if err != nil {
		return StripUserinfo(raw, ""), "", ""
	}
	if u.User == nil {
		return raw, "", ""
	}
	user = u.User.Username()
	pass, _ = u.User.Password()
	u.User = nil
	return u.String(), user, pass
}

func wellFormed(q string) bool {
	for _, kv := range strings.Split(q, "&") {
		if kv != "" && !strings.Contains(kv, "=") {
			return false
		}
	}
	return true
}
