package engine

import (
	"strings"
	"testing"
	"unicode"
)

func FuzzCheckLinks(f *testing.F) {
	for _, s := range []string{
		"http://a/\nrpc-secret=x", "javascript:x", "HTTP://A", "https://a/b", "magnet:?xt=urn:btih:abc",
		"magnet:?xt=a\nrpc-secret=x", "ftp://h/\r\nx", "https://h/%zz", "", "https://a b/",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if CheckLinks([]string{s}) != nil {
			return
		}
		low := strings.ToLower(s)
		ok := false
		for _, p := range []string{"http://", "https://", "ftp://", "sftp://", "magnet:"} {
			ok = ok || strings.HasPrefix(low, p)
		}
		if !ok || strings.IndexFunc(s, unicode.IsControl) >= 0 {
			t.Fatalf("CheckLinks accepted %q", s)
		}
	})
}
