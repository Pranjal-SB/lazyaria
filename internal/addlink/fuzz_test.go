package addlink

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func FuzzAddlinkParams(f *testing.F) {
	tor := filepath.Join(f.TempDir(), "a.torrent")
	if err := os.WriteFile(tor, []byte("d4:infoe"), 0o600); err != nil {
		f.Fatal(err)
	}
	for _, s := range []string{
		"https://a/b.iso", "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567",
		"/tmp/x.torrent", "file:///x.TORRENT", "x.torrent://y", "http://a/b.torrent", "file://" + tor, tor, "", "hello",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		p, err := Params(in)
		if err != nil {
			return
		}
		if (p.URIs != nil) == (p.Torrent != nil) {
			t.Fatalf("%q: URIs %q and Torrent %q: want exactly one", in, p.URIs, p.Torrent)
		}
		if p.Torrent != nil && !strings.HasSuffix(strings.ToLower(in), ".torrent") {
			t.Fatalf("%q gave a Torrent without ending in .torrent", in)
		}
		if p.URIs != nil && (len(p.URIs) != 1 || p.URIs[0] != in) {
			t.Fatalf("%q gave URIs %q", in, p.URIs)
		}
	})
}
