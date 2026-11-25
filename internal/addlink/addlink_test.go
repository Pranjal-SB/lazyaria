package addlink

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestParams(t *testing.T) {
	tor := filepath.Join(t.TempDir(), "a.torrent")
	if err := os.WriteFile(tor, []byte("d4:infoe"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, link := range []string{tor, "file://" + tor} {
		p, err := Params(link)
		if err != nil || string(p.Torrent) != "d4:infoe" || p.URIs != nil {
			t.Fatalf("Params(%q) = %+v, %v; want the file's bytes", link, p, err)
		}
	}
	if p, err := Params("https://x.example/a.torrent"); err != nil || !slices.Equal(p.URIs, []string{"https://x.example/a.torrent"}) {
		t.Fatalf("a remote .torrent link gave %+v, %v", p, err)
	}
	if _, err := Params("/no/such.torrent"); err == nil || errors.Is(err, ErrNotLink) {
		t.Fatalf("a missing file gave %v", err)
	}
	if _, err := Params("hello"); !errors.Is(err, ErrNotLink) {
		t.Fatalf("plain text gave %v", err)
	}
	if p, err := Params("magnet:?xt=urn:btih:abc"); err != nil || len(p.URIs) != 1 {
		t.Fatalf("magnet gave %+v, %v", p, err)
	}
}
