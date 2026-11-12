package textutil

import (
	"fmt"
	"net"
	"net/url"
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

// SplitUserinfo pulls credentials out of a URL so they never get logged or stored with it.
func SplitUserinfo(raw string) (clean string, user, pass string) {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
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
