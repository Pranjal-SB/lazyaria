package engine

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/Pranjal-SB/lazyaria/internal/aria2"
)

// Details asks aria2 for the piece map; the light status poll does not.
func TestDetailsBitfield(t *testing.T) {
	f, e := startFake(t, func(m string, n int) string {
		if m == "system.multicall" {
			return `"result":[[{"gid":"abc","status":"paused","dir":"/d","bitfield":"f0","numPieces":"8","files":[]}]]`
		}
		return `"result":"OK"`
	})
	d, err := e.Details(context.Background(), "abc")
	if err != nil {
		t.Fatal(err)
	}
	if d.Bitfield != "f0" || d.Pieces != 8 {
		t.Fatalf("bitfield %q pieces %d, want f0 and 8", d.Bitfield, d.Pieces)
	}
	if args := string(f.paramsOf("system.multicall", 1)[0]); !strings.Contains(args, `"bitfield"`) || !strings.Contains(args, `"numPieces"`) {
		t.Fatalf("details did not ask for the piece map: %s", args)
	}
	if slices.Contains(aria2.StatusKeys, "bitfield") || slices.Contains(aria2.StatusKeys, "numPieces") {
		t.Fatal("the status poll asks for the piece map")
	}
}
