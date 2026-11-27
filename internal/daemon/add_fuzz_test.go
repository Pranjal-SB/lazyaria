package daemon

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/Pranjal-SB/lazyaria/internal/engine"
)

func FuzzManagerAdd(f *testing.F) {
	r := newRig(f)
	f.Add("http://example.com/a.iso", "", "", "", []byte(nil))
	f.Add("http://example.com/a.iso", "../x", "", "", []byte(nil))
	f.Add("http://example.com/a.iso", "", "out", "/etc/x", []byte(nil))
	f.Add("http://example.com/a.iso", "", "rpc-secret", "a", []byte(nil))
	f.Add("http://example.com/a\nrpc-secret=x", "", "", "", []byte(nil))
	f.Add("magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567", "", "split", "4", []byte(nil))
	f.Add("http://u:p@example.com/a.iso", "a.iso", "header", "X: y", []byte(nil))
	f.Add("", "", "", "", []byte("d8:announce1:a4:infod6:lengthi1e4:name1:xee"))
	f.Add("http://example.com/"+strings.Repeat("a", 1<<20), "", "", "", []byte(nil))
	f.Fuzz(func(t *testing.T, uri, out, key, val string, torrent []byte) {
		p := AddParams{Dir: r.dir, Out: out, Torrent: torrent, Force: true}
		if uri != "" {
			p.URIs = []string{uri}
		}
		if key != "" {
			p.Options = map[string]string{key: val}
		}
		j, err := r.m.Add(bg, p)
		if err != nil {
			return
		}
		if !filepath.IsAbs(j.Dir) || !filepath.IsLocal(j.Name) {
			t.Fatalf("Dir %q, Name %q for %+v", j.Dir, j.Name, p)
		}
		for k := range j.Options {
			if !slices.Contains(engine.AllowedOptions, k) {
				t.Fatalf("option %q reached the job for %+v", k, p)
			}
		}
	})
}

var hex40 = regexp.MustCompile(`^[0-9a-f]{40}$`)

func FuzzTorrentPaths(f *testing.F) {
	for _, s := range []string{
		"d4:infod6:lengthi1e4:name1:x12:piece lengthi1ee6:pieces0:ee",
		"d4:infod5:filesld6:lengthi1e4:pathl1:a1:beee4:name1:dee",
		"d4:infod5:filesld6:lengthi1e4:pathl2:..1:aeee4:name1:dee",
		"d4:infod5:filesld6:lengthi1e4:pathl1:/eee4:name1:dee",
		"d4:infod4:name2:..ee",
		"d4:infod5:filesld6:lengthi-1e4:pathl1:aeee4:name1:dee",
		"d4:infod0:i1e4:name1:xee",
		"d4:infod5:filesld4:pathl0:eee4:name1:dee",
		strings.Repeat("l", 10000),
		strings.Repeat("d1:a", 10000),
		"",
	} {
		f.Add([]byte(s))
	}
	dir := filepath.Join(string(filepath.Separator), "dl")
	f.Fuzz(func(t *testing.T, b []byte) {
		for _, p := range torrentPaths(b, dir) {
			if rel, err := filepath.Rel(dir, p); err != nil || !filepath.IsLocal(rel) {
				t.Fatalf("path %q leaves %q", p, dir)
			}
		}
		if h := infoHash(b); h != "" && !hex40.MatchString(h) {
			t.Fatalf("infoHash = %q", h)
		}
	})
}
