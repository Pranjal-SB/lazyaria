package daemon

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Pranjal-SB/lazyaria/internal/fsinfo"
	"github.com/Pranjal-SB/lazyaria/internal/textutil"
)

// existingTarget returns dir/out when a finished file already sits there:
// the file exists and has no .aria2 control file, which would mean an
// unfinished download that aria2 resumes.
func existingTarget(dir string, p AddParams) string {
	name := targetName(dir, p)
	if name == "" {
		return ""
	}
	path := filepath.Join(dir, name)
	if exists(path) && !exists(path+".aria2") {
		return path
	}
	return ""
}

// targetName is the sanitized name the add will write, or "" when only the
// server or the torrent can tell.
func targetName(dir string, p AddParams) string {
	clean := make([]string, len(p.URIs))
	for i, u := range p.URIs {
		clean[i], _, _ = textutil.SplitUserinfo(u)
	}
	name := p.Out
	if name == "" && len(p.Torrent) == 0 && len(clean) > 0 && jobKind(clean, nil) != "magnet" {
		name = displayName("", clean)
	}
	if name == "" {
		return ""
	}
	return textutil.SanitizeName(name, fsinfo.IsFAT(dir))
}

// unfinishedBase is the name an unfinished download goes by outside its
// staging folder: "<stem> (unfinished)<ext>", so it keeps its type and
// cannot pass for the finished file. The extension starts at the last dot
// after the first character, or covers a compressed tarball's two parts.
// A long stem is cut, on a character boundary, so the name still fits with
// a clash number (freeName's ".N", up to 4 digits) and the .aria2 beside it.
func unfinishedBase(name string) string {
	const suffix = " (unfinished)"
	stem, ext := name, ""
	if i := strings.LastIndexByte(name, '.'); i > 0 {
		stem, ext = name[:i], name[i:]
	}
	lower := strings.ToLower(name)
	for _, tar := range []string{".tar.gz", ".tar.xz", ".tar.zst", ".tar.bz2"} {
		if strings.HasSuffix(lower, tar) && len(name) > len(tar) {
			stem, ext = name[:len(name)-len(tar)], name[len(name)-len(tar):]
		}
	}
	if keep := textutil.MaxName - len(".9999") - len(suffix) - len(ext); len(stem) > keep {
		stem = stem[:max(keep, 0)]
		for !utf8.ValidString(stem) {
			stem = stem[:len(stem)-1]
		}
	}
	return stem + suffix + ext
}

// freeName picks the name aria2 itself would rename path to: the first
// base.N.ext not on disk and not held by a live job, shortened to fit
// textutil.MaxName.
func freeName(path string, held map[string]bool) (string, error) {
	dir, name := filepath.Split(path)
	base, ext := name, ""
	if i := strings.LastIndexByte(name, '.'); i > 0 && len(name)-i <= 32 {
		base, ext = name[:i], name[i:]
	}
	for n := 1; n < 10000; n++ {
		suffix := "." + strconv.Itoa(n) + ext
		b := base
		if keep := textutil.MaxName - len(suffix); len(b) > keep {
			b = b[:keep]
			for !utf8.ValidString(b) {
				b = b[:len(b)-1]
			}
		}
		cand := filepath.Join(dir, b+suffix)
		if !held[b+suffix] && !exists(cand) && !exists(cand+".aria2") {
			return b + suffix, nil
		}
	}
	return "", fmt.Errorf("%s already exists and no free name beside it was found; choose another name with --out", path)
}
