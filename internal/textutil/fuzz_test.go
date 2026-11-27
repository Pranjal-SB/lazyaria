package textutil

import (
	"net/url"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

func FuzzExtractLinks(f *testing.F) {
	for _, s := range []string{
		"see https://a.com/x.",
		"(https://en.wikipedia.org/wiki/Go_(language)) and more.",
		"magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=name",
		"file:///home/u/a.torrent",
		"https://a/1\r\nhttp://b/2\r\nftp://c/3\r\n",
		"[x](https://a.com/y) and [z](sftp://h/p)",
		"/tmp/a.torrent.bak /tmp/b.torrent",
		"https://a/\x0bb magnet:?xt=a\x00b",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		for _, l := range ExtractLinks(s) {
			low := strings.ToLower(l)
			ok := false
			for _, p := range []string{"http://", "https://", "ftp://", "sftp://", "magnet:"} {
				ok = ok || strings.HasPrefix(low, p)
			}
			ok = ok || (strings.HasPrefix(low, "file://") || strings.HasPrefix(l, "/")) && strings.HasSuffix(low, ".torrent")
			if !strings.Contains(s, l) || !ok {
				t.Fatalf("bad link %q from %q", l, s)
			}
			for _, r := range l {
				if unicode.IsSpace(r) || unicode.IsControl(r) {
					t.Fatalf("link %q holds %U", l, r)
				}
			}
		}
	})
}

func FuzzSanitizeName(f *testing.F) {
	f.Add("../a", true)
	f.Fuzz(func(t *testing.T, s string, fat bool) {
		got := SanitizeName(s, fat)
		if strings.Contains(got, "/") || got == ".." || got == "" || len(got) > MaxName || !utf8.ValidString(got) {
			t.Fatalf("bad %q -> %q", s, got)
		}
	})
}

func FuzzStripUserinfo(f *testing.F) {
	for _, s := range []string{
		"https://u:secretpw@h/x", "ftp://u@h", "sftp://u:p@[::1]:22/", "https://h/a@b",
		"https://u%40x:longpassword@h/", "https://u:pa%zzssword@h/f",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		clean, _, pass := SplitUserinfo(raw)
		if len(pass) < 8 {
			return
		}
		// Only the authority counts: an "@" later in the path is not userinfo.
		auth := clean
		if _, rest, ok := strings.Cut(clean, "://"); ok {
			auth = rest
		}
		if i := strings.IndexAny(auth, "/?#"); i >= 0 {
			auth = auth[:i]
		}
		if strings.Contains(auth, "@") {
			t.Fatalf("%q kept its userinfo: %q", raw, clean)
		}
	})
}

func FuzzNormalizeURL(f *testing.F) {
	for _, s := range []string{
		"HTTP://Example.COM:80/A?b=1&a=2#frag", "https://h:443/", "ftp://h:21/x", "sftp://[::1]:22/p",
		"magnet:?xt=urn:btih:0123456789ABCDEF0123456789ABCDEF01234567&dn=x", "https://h/?a=1;b=2",
		"https://h/?x&y=1", "https://H:8080",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		n, err := NormalizeURL(raw)
		if err != nil {
			return
		}
		if _, err := url.Parse(n); err != nil {
			t.Fatalf("%q -> %q does not parse: %v", raw, n, err)
		}
		if n2, err := NormalizeURL(n); err != nil || n2 != n {
			t.Fatalf("%q -> %q -> %q, %v", raw, n, n2, err)
		}
	})
}
