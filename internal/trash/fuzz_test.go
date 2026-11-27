package trash

import (
	"net/url"
	"strings"
	"testing"
)

func FuzzEncodePath(f *testing.F) {
	for _, s := range []string{"/a b/c", "/x\nDeletionDate=2020-01-01T00:00:00", "%", "/a/\xff\xfe/b", "", "/", "a\r\nb", "/ü/100%"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, p string) {
		enc := encodePath(p)
		if strings.ContainsAny(enc, "\r\n") {
			t.Fatalf("encodePath(%q) = %q holds a line break", p, enc)
		}
		got, want := strings.Split(enc, "/"), strings.Split(p, "/")
		if len(got) != len(want) {
			t.Fatalf("encodePath(%q) = %q changed the part count", p, enc)
		}
		for i, g := range got {
			if dec, err := url.PathUnescape(g); err != nil || dec != want[i] {
				t.Fatalf("part %d of %q: %q decodes to %q, %v", i, p, g, dec, err)
			}
		}
	})
}
