// Package addlink turns what someone gives as a download, a link, a
// magnet or a .torrent file path, into the daemon's add parameters. The
// CLI and the TUI both use it.
package addlink

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/Pranjal-SB/lazyaria/internal/daemon"
)

// ErrNotLink means the text is neither a link nor a .torrent file.
var ErrNotLink = errors.New("not a link or a .torrent file")

// Params is one add for link. A .torrent file on this machine, as a path
// or a file:// link, is read and sent as the torrent itself, because the
// daemon may not see the same files.
func Params(link string) (daemon.AddParams, error) {
	path := strings.TrimPrefix(link, "file://")
	if strings.HasSuffix(strings.ToLower(path), ".torrent") {
		if b, err := os.ReadFile(path); err == nil {
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
