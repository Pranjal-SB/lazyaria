package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/Pranjal-SB/lazyaria/internal/daemon"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

func TestPieceCells(t *testing.T) {
	g := NewTheme("unicode").G
	if got := pieceCells([]int{100, 99, 50, 1, 0}, g); got != "█▒▒▒░" {
		t.Fatalf("unicode cells %q", got)
	}
	if got := pieceCells([]int{100, 40, 0}, NewTheme("ascii").G); got != "#+." {
		t.Fatalf("ascii cells %q", got)
	}
}

// httpDetails is what the daemon sends for the ubuntu download: two
// servers, a piece map in progress and a few events.
func httpDetails() daemon.Details {
	d := daemon.Details{Pieces: 1536}
	for i := range 16 {
		host := "https://releases.example.com/ubuntu.iso"
		if i >= 10 {
			host = "https://mirror.example.org/ubuntu.iso"
		}
		d.Servers = append(d.Servers, host)
	}
	for i := range 2 * 36 {
		switch {
		case i < 50:
			d.Segments = append(d.Segments, 100)
		case i < 55:
			d.Segments = append(d.Segments, 40)
		default:
			d.Segments = append(d.Segments, 0)
		}
	}
	d.Events = []daemon.DetailEvent{
		{At: goldenNow.Add(-30 * time.Minute), Kind: "added"},
		{At: goldenNow.Add(-22 * time.Minute), Kind: "paused"},
		{At: goldenNow.Add(-21 * time.Minute), Kind: "resumed"},
	}
	return d
}

func torrentDetails() daemon.Details {
	d := daemon.Details{Peers: 12, Pieces: 2800, Segments: make([]int, 72)}
	for i := range d.Segments {
		d.Segments[i] = 100
	}
	for i := range 7 {
		d.Files = append(d.Files, daemon.DetailFile{Path: "debian/pool/part-" + string(rune('a'+i)) + ".deb", Length: 100 * mib, Done: 100 * mib, Selected: true})
	}
	d.Events = []daemon.DetailEvent{{At: goldenNow.Add(-3 * time.Hour), Kind: "added"}, {At: goldenNow.Add(-2 * time.Hour), Kind: "done"}}
	return d
}

func detailsDaemon() *fakeDaemon {
	d := sampleDaemon()
	d.details = map[string]daemon.Details{
		"ubuntu-26.04-desktop-amd64.iso":  httpDetails(),
		"debian-13.0.0-amd64-netinst.iso": torrentDetails(),
	}
	return d
}

// Ten frames within a second fetch the details at most twice.
func TestDetailsFetchLimited(t *testing.T) {
	d := newFakeDaemon(job(1, "a.iso", store.Active, daemon.ActPause))
	u := startTUI(t, options(t, d), 120, 32)
	u.waitFor(t, "a.iso")
	waitFor(t, "the first details call", func() bool { return d.detailsCalls.Load() == 1 })
	for range 10 {
		u.Send(frameMsg{f: daemon.Frame{Items: []daemon.ProgressItem{{ID: "a.iso", Done: 1, Total: 10}}}})
		time.Sleep(80 * time.Millisecond)
	}
	if n := d.detailsCalls.Load() - 1; n > 2 {
		t.Fatalf("%d details calls for ten frames in a second", n)
	}
	waitFor(t, "a details call after the frames", func() bool { return d.detailsCalls.Load() >= 2 })
	u.quit(t)
}

// With the pane closed on a narrow terminal nothing is fetched.
func TestDetailsNotFetchedWhenHidden(t *testing.T) {
	d := newFakeDaemon(job(1, "a.iso", store.Active, daemon.ActPause))
	u := startTUI(t, options(t, d), 90, 30)
	u.waitFor(t, "a.iso")
	for range 5 {
		u.Send(frameMsg{f: daemon.Frame{Items: []daemon.ProgressItem{{ID: "a.iso", Done: 1, Total: 10}}}})
	}
	time.Sleep(400 * time.Millisecond)
	if n := d.detailsCalls.Load(); n != 0 {
		t.Fatalf("%d details calls with the pane hidden", n)
	}
	u.Send(key("enter"))
	waitFor(t, "a details call once the pane opens", func() bool { return d.detailsCalls.Load() == 1 })
	u.quit(t)
}

func TestDetailsGolden(t *testing.T) {
	for _, tc := range []goldenCase{
		{name: "details_http_120x32_ansi", w: 120, h: 32, d: detailsDaemon(), wait: "10 connections"},
		{name: "details_torrent_120x32_ansi", w: 120, h: 32, d: detailsDaemon(), keys: []tea.Msg{key("3")}, wait: "12 peers"},
	} {
		tc.profile = colorprofile.ANSI
		t.Run(tc.name, func(t *testing.T) { runGolden(t, tc) })
	}
}

// A server link url.Parse rejects is still shown without its password.
func TestHostCountsHideCredentials(t *testing.T) {
	for _, h := range hostCounts([]string{"ftp://user:se cret@host/%zz"}) {
		if strings.Contains(h.host, "se cret") || strings.Contains(h.host, "user") {
			t.Fatalf("host %q shows the credentials", h.host)
		}
	}
}
