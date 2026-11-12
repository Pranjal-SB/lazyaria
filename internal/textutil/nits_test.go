package textutil

import (
	"reflect"
	"testing"
)

func TestExtractLinksTorrentBoundary(t *testing.T) {
	if got := ExtractLinks("see /a.torrent.bak now"); len(got) != 0 {
		t.Fatalf("got %q", got)
	}
	if got := ExtractLinks("see /a.torrent."); !reflect.DeepEqual(got, []string{"/a.torrent"}) {
		t.Fatalf("got %q", got)
	}
	if got := ExtractLinks("(/a.torrent)"); !reflect.DeepEqual(got, []string{"/a.torrent"}) {
		t.Fatalf("got %q", got)
	}
}

func TestExtractLinksKeepsInnerPunctuation(t *testing.T) {
	for _, in := range []string{"https://a.com/x?", "https://a.com/x!", "https://a.com/x;", "https://a.com/x:"} {
		if got := ExtractLinks(in); !reflect.DeepEqual(got, []string{in}) {
			t.Errorf("%q -> %q", in, got)
		}
	}
}

func TestNormalizeURLMalformedQueryKept(t *testing.T) {
	for _, q := range []string{"a=1;b=2", "a"} {
		got, err := NormalizeURL("http://h.com/p?" + q)
		if err != nil || got != "http://h.com/p?"+q {
			t.Errorf("%q -> %q, %v", q, got, err)
		}
	}
}

func TestSanitizeNameStripsBidi(t *testing.T) {
	if got := SanitizeName("a‮gpj.exe⁦‏", false); got != "agpj.exe" {
		t.Fatalf("got %q", got)
	}
}

func TestSanitizeNameBidiOnly(t *testing.T) {
	if got := SanitizeName("a\u061Cb\uFEFF.txt", false); got != "ab.txt" {
		t.Fatalf("got %q", got)
	}
	for _, in := range []string{
		"\u0915\u094D\u200D\u0937.txt",
		"\u0915\u094D\u200C\u0937.txt",
		"\U0001F468\u200D\U0001F469\u200D\U0001F467.txt",
	} {
		if got := SanitizeName(in, false); got != in {
			t.Errorf("%+q -> %+q", in, got)
		}
	}
}
