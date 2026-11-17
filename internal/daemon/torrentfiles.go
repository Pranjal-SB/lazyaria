package daemon

import (
	"bytes"
	"context"
	"path/filepath"
	"strconv"

	"github.com/Pranjal-SB/lazyaria/internal/store"
)

// downloadFiles lists the files a torrent job writes: from aria2 while it
// still has the download, else from the stored .torrent. It is nil for
// other jobs and when nothing says (a magnet aria2 has forgotten, or a
// .torrent with select-file).
func (m *Manager) downloadFiles(ctx context.Context, j *store.Job) []string {
	if !isBT(j.Kind) {
		return nil
	}
	if j.EngineRef != nil {
		if d, err := m.e.Details(ctx, refOf(j)); err == nil {
			var out []string
			for _, f := range d.Files {
				// aria2 lists files select-file left out too; they are not ours.
				if f.Path != "" && f.Selected {
					out = append(out, f.Path)
				}
			}
			if len(out) > 0 {
				return out
			}
		}
	}
	if j.Options["select-file"] != "" {
		return nil // the .torrent cannot say which files were picked
	}
	return torrentPaths(j.Torrent, j.Dir)
}

// torrentPaths lists where the files of a .torrent land under dir. It is
// nil when the metadata cannot be read or a path would leave the folder.
func torrentPaths(b []byte, dir string) []string {
	d := &bdec{b: b}
	v, ok := d.value(0)
	top, _ := v.(map[string]any)
	info, _ := top["info"].(map[string]any)
	name := utf8Key[string](info, "name")
	if !ok || !local(name) {
		return nil
	}
	root := filepath.Join(dir, name)
	files, multi := info["files"].([]any)
	if !multi {
		return []string{root}
	}
	var out []string
	for _, f := range files {
		fd, _ := f.(map[string]any)
		var segs []string
		for _, p := range utf8Key[[]any](fd, "path") {
			s, _ := p.(string)
			segs = append(segs, s)
		}
		rel := filepath.Join(segs...)
		if !local(rel) {
			return nil
		}
		out = append(out, filepath.Join(root, rel))
	}
	return out
}

// local reports whether p names something strictly inside its folder.
func local(p string) bool { return filepath.IsLocal(p) && filepath.Clean(p) != "." }

// utf8Key prefers the key's ".utf-8" variant, as aria2 does.
func utf8Key[T any](d map[string]any, k string) T {
	if v, ok := d[k+".utf-8"].(T); ok {
		return v
	}
	v, _ := d[k].(T)
	return v
}

// bdec decodes bencode into strings, []any and map[string]any. Integers
// are not needed and decode as nil.
type bdec struct {
	b []byte
	i int
}

func (d *bdec) value(depth int) (any, bool) {
	if depth > 32 || d.i >= len(d.b) {
		return nil, false
	}
	switch c := d.b[d.i]; {
	case c == 'i':
		end := bytes.IndexByte(d.b[d.i:], 'e')
		if end < 0 {
			return nil, false
		}
		d.i += end + 1
		return nil, true
	case c == 'l' || c == 'd':
		d.i++
		var list []any
		dict := map[string]any{}
		for d.i < len(d.b) && d.b[d.i] != 'e' {
			v, ok := d.value(depth + 1)
			if !ok {
				return nil, false
			}
			if c == 'l' {
				list = append(list, v)
				continue
			}
			k, isStr := v.(string)
			if v, ok = d.value(depth + 1); !ok || !isStr {
				return nil, false
			}
			dict[k] = v
		}
		if d.i >= len(d.b) {
			return nil, false
		}
		d.i++
		if c == 'd' {
			return dict, true
		}
		return list, true
	case c >= '0' && c <= '9':
		colon := bytes.IndexByte(d.b[d.i:], ':')
		if colon < 0 {
			return nil, false
		}
		n, err := strconv.Atoi(string(d.b[d.i : d.i+colon]))
		start := d.i + colon + 1
		if err != nil || n < 0 || n > len(d.b)-start {
			return nil, false
		}
		d.i = start + n
		return string(d.b[start:d.i]), true
	}
	return nil, false
}
