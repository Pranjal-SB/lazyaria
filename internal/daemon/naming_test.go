package daemon

import "testing"

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
