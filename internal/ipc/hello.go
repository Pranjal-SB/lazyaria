package ipc

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Pranjal-SB/lazyaria/internal/buildinfo"
)

// ErrProtocolMismatch means the daemon speaks another protocol version. Its
// message is the banner shown to the user.
type ErrProtocolMismatch struct {
	DaemonVersion string
	Protocol      int
}

func (e *ErrProtocolMismatch) Error() string {
	daemonV := strings.TrimPrefix(e.DaemonVersion, "v")
	if e.Protocol > buildinfo.Protocol {
		return fmt.Sprintf("this lazyaria (v%s) is older than the running daemon (v%s); update it",
			strings.TrimPrefix(buildinfo.Version, "v"), daemonV)
	}
	return fmt.Sprintf("daemon v%s is running; it will upgrade when idle (protocol %d, this client speaks %d)",
		daemonV, e.Protocol, buildinfo.Protocol)
}

// Hello does the version handshake, so a client never talks to a daemon
// that speaks another protocol. With upgrade set, an
// older but compatible daemon is asked to upgrade itself; upgraded is true
// when it agreed, and the caller should reconnect. A daemon that refuses
// (the binary at its path is no newer) is still compatible and is used as
// it is.
func (c *Client) Hello(ctx context.Context, upgrade bool) (res HelloResult, upgraded bool, err error) {
	return c.helloAs(ctx, buildinfo.Version, upgrade)
}

func (c *Client) helloAs(ctx context.Context, version string, upgrade bool) (res HelloResult, upgraded bool, err error) {
	if err := c.call(ctx, "hello", HelloParams{Client: "lazyaria", Version: version}, &res); err != nil {
		return res, false, err
	}
	if res.Protocol != buildinfo.Protocol {
		return res, false, &ErrProtocolMismatch{DaemonVersion: res.DaemonVersion, Protocol: res.Protocol}
	}
	if !upgrade || !Older(res.DaemonVersion, version) {
		return res, false, nil
	}
	if err := c.call(ctx, "daemon.upgrade", nil, nil); err != nil {
		return res, false, nil
	}
	return res, true, nil
}

// Older reports whether version a predates b. "dev" is newer than any
// release; pre-release suffixes and unparseable versions compare equal.
func Older(a, b string) bool {
	if a == "dev" || a == b {
		return false
	}
	if b == "dev" {
		return parseVersion(a) != nil
	}
	va, vb := parseVersion(a), parseVersion(b)
	if va == nil || vb == nil {
		return false
	}
	for i := range 3 {
		if va[i] != vb[i] {
			return va[i] < vb[i]
		}
	}
	return false
}

func parseVersion(s string) []int {
	s = strings.TrimPrefix(s, "v")
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) > 3 {
		return nil
	}
	v := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil
		}
		v[i] = n
	}
	return v
}
