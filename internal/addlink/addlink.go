// Package addlink turns what someone gives as a download, a link, a
// magnet or a .torrent file path, into the daemon's add parameters. The
// CLI and the TUI both use it.
package addlink

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Pranjal-SB/lazyaria/internal/daemon"
)

// ErrNotLink means the text is neither a link nor a .torrent file.
var ErrNotLink = errors.New("not a link or a .torrent file")

// MaxTorrent is the largest .torrent file Params reads. Real ones are a
// few hundred KiB; anything this big is something else.
const MaxTorrent = 16 << 20

// Params is one add for link. A .torrent file on this machine, as a path
// or a file:// link, is read and sent as the torrent itself, because the
// daemon may not see the same files. Reading may block on a slow disk, so
// interactive callers run it off their input loop.
func Params(link string) (daemon.AddParams, error) {
	path := strings.TrimPrefix(link, "file://")
	if strings.HasSuffix(strings.ToLower(path), ".torrent") {
		if b, err := readTorrent(path); err == nil {
			return daemon.AddParams{Torrent: b}, nil
		} else if !strings.Contains(link, "://") || strings.HasPrefix(link, "file://") {
			return daemon.AddParams{}, fmt.Errorf("read %s: %w", path, err)
		}
	}
	if !strings.Contains(link, "://") && !strings.HasPrefix(strings.ToLower(link), "magnet:") {
		return daemon.AddParams{}, fmt.Errorf("%q: %w", link, ErrNotLink)
	}
	return daemon.AddParams{URIs: []string{link}}, nil
}

// readTorrent reads a regular file of at most MaxTorrent bytes. A FIFO or
// a device named x.torrent would otherwise block or never end.
func readTorrent(path string) ([]byte, error) {
	st, err := os.Stat(path) // follows links: the target must be a plain file
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("read %s: not a regular file", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, MaxTorrent+1))
	switch {
	case err != nil:
		return nil, fmt.Errorf("read %s: %w", path, err)
	case len(b) > MaxTorrent:
		return nil, fmt.Errorf("read %s: larger than %d MiB, so not a .torrent file", path, MaxTorrent>>20)
	}
	return b, nil
}
