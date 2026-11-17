package textutil

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSanitizeName(t *testing.T) {
	cases := []struct {
		in   string
		fat  bool
		want string
	}{
		{"../../etc/passwd", false, "etc_passwd"},
		{"-rf", false, "rf"},
		{"a\x00b", false, "ab"},
		{"x:y?.txt", true, "x_y_.txt"},
		{"x:y.txt", false, "x:y.txt"},
		{"..", false, "download"},
		{"", false, "download"},
		{`a\b`, false, "a_b"},
	}
	for _, c := range cases {
		if got := SanitizeName(c.in, c.fat); got != c.want {
			t.Errorf("SanitizeName(%q,%v)=%q want %q", c.in, c.fat, got, c.want)
		}
	}
}

// A long name is cut in its stem, so its extension survives.
func TestSanitizeName_LongKeepsExtension(t *testing.T) {
	long := strings.Repeat("a", 300)
	for in, suffix := range map[string]string{
		long + ".mkv":                         ".mkv",
		long + ".tar.gz":                      ".tar.gz",
		strings.Repeat("é", 150) + ".mkv":     ".mkv",
		long:                                  "aaaa",
		"x." + long:                           "aaaa", // no extension that long
		"show.s01e02.1080p." + long + ".webm": ".webm",
	} {
		got := SanitizeName(in, false)
		if len(got) > MaxName || len(got) < MaxName-1 || !utf8.ValidString(got) || !strings.HasSuffix(got, suffix) {
			t.Errorf("SanitizeName(%.20q...) = %.20q...%q (len %d); want <= %d bytes ending %q", in, got, got[max(0, len(got)-10):], len(got), MaxName, suffix)
		}
	}
}

func TestSanitizeName_LongUnicode(t *testing.T) {
	got := SanitizeName(strings.Repeat("é", 150), false)
	if len(got) != MaxName-1 || !utf8.ValidString(got) { // 249 cuts an é in half
		t.Fatalf("len=%d valid=%v", len(got), utf8.ValidString(got))
	}
}

func TestNormalizeURL(t *testing.T) {
	got, err := NormalizeURL("HTTPS://Example.com:443/a?b=2&a=1#x")
	if err != nil || got != "https://example.com/a?a=1&b=2" {
		t.Fatalf("got %q, %v", got, err)
	}
	want := "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"
	for _, m := range []string{"MAGNET:?dn=x&xt=urn:btih:0123456789ABCDEF0123456789ABCDEF01234567&tr=t",
		"magnet:?xt=urn:btih:aeruKZ4JVPG66AJDIVTYTK6N54ASGRLH"} {
		if got, err := NormalizeURL(m); err != nil || got != want {
			t.Fatalf("NormalizeURL(%q) = %q, %v; want %q", m, got, err, want)
		}
	}
	if _, err := NormalizeURL("magnet:?xt=urn:btih:xyz"); err == nil {
		t.Fatal("a magnet with a bad info-hash normalised")
	}
}

func TestSplitUserinfo(t *testing.T) {
	c, u, p := SplitUserinfo("sftp://u:p@h/f")
	if c != "sftp://h/f" || u != "u" || p != "p" {
		t.Fatalf("got %q %q %q", c, u, p)
	}
}

func TestExtractLinks(t *testing.T) {
	text := "grab https://a.com/x.iso, and (ftp://b.org/f) plus magnet:?xt=urn:btih:abc&dn=x " +
		"also /home/x/a.torrent and https://a.com/x.iso again."
	want := []string{"https://a.com/x.iso", "ftp://b.org/f", "magnet:?xt=urn:btih:abc&dn=x", "/home/x/a.torrent"}
	if got := ExtractLinks(text); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q", got)
	}
	if got := ExtractLinks("no links here"); len(got) != 0 {
		t.Fatalf("got %q", got)
	}
	if got := ExtractLinks("see https://en.wikipedia.org/wiki/Go_(language)."); !reflect.DeepEqual(got, []string{"https://en.wikipedia.org/wiki/Go_(language)"}) {
		t.Fatalf("got %q", got)
	}
}

func FuzzExtractLinks(f *testing.F) {
	f.Add("see https://a.com/x.")
	f.Fuzz(func(t *testing.T, s string) { ExtractLinks(s) })
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

// A URL that does not parse must still lose its userinfo.
func TestSplitUserinfoUnparseable(t *testing.T) {
	c, _, _ := SplitUserinfo("https://alice:p%zzword@host/f")
	if strings.Contains(c, "alice") || strings.Contains(c, "p%zzword") || c != "https://host/f" {
		t.Fatalf("clean = %q, want the userinfo gone", c)
	}
	if c, _, _ := SplitUserinfo("https://host/a@b%zz"); c != "https://host/a@b%zz" {
		t.Fatalf("an @ in the path was taken for userinfo: %q", c)
	}
}
