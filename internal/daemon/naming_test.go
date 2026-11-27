package daemon

import (
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Pranjal-SB/lazyaria/internal/textutil"
)

// A long name loses the end of its stem, on a character boundary, so the
// name with its suffix, a clash number and the .aria2 beside it still fits
// a file name; the extension and the suffix stay.
func TestUnfinishedBaseFitsLongNames(t *testing.T) {
	for _, in := range []string{strings.Repeat("é", 122) + ".iso", strings.Repeat("a", 240) + ".tar.gz", strings.Repeat("ü", 124)} {
		got := unfinishedBase(in)
		ext := filepath.Ext(in)
		if strings.HasSuffix(in, ".tar.gz") {
			ext = ".tar.gz"
		}
		if len(got) > textutil.MaxName-len(".9999") || !utf8.ValidString(got) || !strings.HasSuffix(got, " (unfinished)"+ext) {
			t.Errorf("unfinishedBase(%d bytes) = %q (%d bytes)", len(in), got, len(got))
		}
		// The clash rule numbers before the last dot and must not cut the suffix.
		if name, err := freeName(filepath.Join(t.TempDir(), got), nil); err != nil || !strings.Contains(name, " (unfinished)") || len(name) > textutil.MaxName {
			t.Errorf("next free name %q, %v", name, err)
		}
	}
}

func TestUnfinishedBase(t *testing.T) {
	for in, want := range map[string]string{
		"a.iso":           "a (unfinished).iso",
		"movie.final.mkv": "movie.final (unfinished).mkv",
		"README":          "README (unfinished)",
		".bashrc":         ".bashrc (unfinished)",
		"src.tar.gz":      "src (unfinished).tar.gz",
		"src.TAR.XZ":      "src (unfinished).TAR.XZ",
		"src.tar.zst":     "src (unfinished).tar.zst",
		"src.tar.bz2":     "src (unfinished).tar.bz2",
		".tar.gz":         ".tar (unfinished).gz",
		"Music":           "Music (unfinished)",
		"dots.":           "dots (unfinished).",
	} {
		if got := unfinishedBase(in); got != want {
			t.Errorf("unfinishedBase(%q) = %q, want %q", in, got, want)
		}
	}
}
