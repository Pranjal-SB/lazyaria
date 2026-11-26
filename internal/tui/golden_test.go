package tui

import (
	"bytes"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/Pranjal-SB/lazyaria/internal/daemon"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

var goldenNow = time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

func ptr[T any](v T) *T { return &v }

const (
	kib = int64(1) << 10
	mib = int64(1) << 20
	gib = int64(1) << 30
)

// progressJob is a job with sizes and speeds as the daemon sends them.
func progressJob(num int64, name string, st store.State, label string, done, total, down, eta int64, actions ...string) *store.Job {
	j := &store.Job{ID: name, Num: num, Name: name, State: st, Label: label, Actions: actions, Kind: "http",
		URIs: []string{"https://example.com/files/" + name}, Dir: "/home/me/Downloads", SpeedDown: down}
	if total > 0 {
		j.TotalBytes = &total
	}
	j.DoneBytes = &done
	if eta > 0 {
		j.ETASeconds = &eta
	}
	return j
}

// sampleJobs is one job per state, in the daemon's order.
func sampleJobs() []*store.Job {
	pr := []string{daemon.ActPause, daemon.ActRemove, daemon.ActRefresh}
	rs := []string{daemon.ActResume, daemon.ActRemove, daemon.ActRefresh}
	done := progressJob(9, "paper.pdf", store.Done, "done", 1258291, 1258291, 0, 0, daemon.ActRemove, daemon.ActOpen)
	done.FinishedAt = ptr(goldenNow.Add(-2 * time.Minute))
	seed := progressJob(8, "debian-13.0.0-amd64-netinst.iso", store.Seeding, "seeding", 700*mib, 700*mib, 0, 0, daemon.ActStopSeeding, daemon.ActRemove, daemon.ActOpen)
	seed.Kind, seed.SpeedUp = "torrent", 512*kib
	failed := progressJob(17, "driver.zip", store.Error, "link expired", 0, 0, 0, 0, rs...)
	failed.ErrorMsg = ptr("the server says the link has expired (403)")
	return []*store.Job{
		failed,
		progressJob(16, "ubuntu-26.04-desktop-amd64.iso", store.Active, "downloading", 4700*mib, 6*gib, 9540*kib, 120, pr...),
		progressJob(15, "conference-talk-1080p.mkv", store.Active, "downloading", 1100*mib, 3600*mib, 2867*kib, 540, pr...),
		progressJob(14, "a-file-whose-name-is-far-too-long-to-fit-in-the-row-at-all-even-wide.tar.gz", store.Active, "starting", 0, 0, 0, 0, pr...),
		progressJob(12, "dataset-2026.tar.zst", store.Paused, "paused", 200*mib, 2400*mib, 0, 0, rs...),
		progressJob(11, "fedora-43-workstation.iso", store.Queued, "queued", 0, 2*gib, 0, 0, daemon.ActPause, daemon.ActRemove, daemon.ActRefresh),
		progressJob(10, "flaky-mirror.bin", store.RetryWait, "retrying", 30*mib, 90*mib, 0, 0, pr...),
		seed,
		done,
	}
}

func sampleDaemon() *fakeDaemon {
	d := newFakeDaemon(sampleJobs()...)
	d.snap.Summary = daemon.Summary{Active: 3, Waiting: 3, Attention: 1, Seeding: 1, Done: 1, Continuing: 6,
		Down: 12407 * kib, Up: 512 * kib, ETASeconds: ptr(int64(240))}
	for i, v := range []int64{0, 1, 2, 4, 6, 8, 7, 5, 9, 12} {
		d.snap.Speed = append(d.snap.Speed, daemon.SpeedSample{At: goldenNow.Add(time.Duration(i-10) * time.Second), Down: v * mib})
	}
	return d
}

type goldenCase struct {
	name    string
	w, h    int
	d       *fakeDaemon
	profile colorprofile.Profile
	set     func(o *Options)
	keys    []tea.Msg
	wait    string // text to wait for after the keys, when they fetch something
}

func key(s string) tea.Msg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	}
	r := []rune(s)[0]
	return tea.KeyPressMsg{Code: r, Text: s}
}

// runGolden shows the screen after the keys and compares the last frame,
// in the case's colour profile, with testdata/<test>/<case>.golden. The
// last frame, not the byte stream: the stream depends on render timing.
func runGolden(t *testing.T, tc goldenCase) {
	t.Helper()
	o := options(t, tc.d)
	o.Now = func() time.Time { return goldenNow }
	if tc.set != nil {
		tc.set(&o)
	}
	u := startTUI(t, o, tc.w, tc.h, tea.WithEnvironment([]string{"TERM=xterm-256color"}))
	u.waitFor(t, textEngine)
	for _, k := range tc.keys {
		u.Send(k)
	}
	if tc.wait != "" {
		u.waitFor(t, tc.wait)
	}
	m := u.quit(t)
	var b bytes.Buffer
	w := &colorprofile.Writer{Forward: &b, Profile: tc.profile}
	w.WriteString(m.View().Content)
	b.WriteString("\n")
	teatest.RequireEqualOutput(t, b.Bytes())
}

func TestGolden(t *testing.T) {
	ascii := func(o *Options) { o.Settings.UI.Glyphs = "ascii" }
	nerd := func(o *Options) { o.Settings.UI.Glyphs = "nerd" }
	for _, tc := range []goldenCase{
		{name: "empty_120x32_ansi", w: 120, h: 32, d: newFakeDaemon()},
		{name: "list_120x32_ansi", w: 120, h: 32, d: sampleDaemon()},
		{name: "list_120x32_nocolor", w: 120, h: 32, d: sampleDaemon(), profile: colorprofile.Ascii},
		{name: "waiting_120x32_ansi", w: 120, h: 32, d: sampleDaemon(), keys: []tea.Msg{key("2")}},
		{name: "finished_120x32_ansi", w: 120, h: 32, d: sampleDaemon(), keys: []tea.Msg{key("]"), key("tab")}},
		{name: "problems_120x32_ansi", w: 120, h: 32, d: sampleDaemon(), keys: []tea.Msg{key("4")}},
		{name: "list_90x30_ansi", w: 90, h: 30, d: sampleDaemon(), keys: []tea.Msg{key("j")}},
		{name: "list_90x30_details_ansi", w: 90, h: 30, d: sampleDaemon(), keys: []tea.Msg{key("enter")}},
		{name: "list_80x24_ansi", w: 80, h: 24, d: sampleDaemon()},
		{name: "list_70x20_ansi", w: 70, h: 20, d: sampleDaemon()},
		{name: "list_120x32_ascii", w: 120, h: 32, d: sampleDaemon(), set: ascii},
		{name: "list_120x32_nerd", w: 120, h: 32, d: sampleDaemon(), set: nerd},
	} {
		if tc.profile == 0 {
			tc.profile = colorprofile.ANSI
		}
		t.Run(tc.name, func(t *testing.T) { runGolden(t, tc) })
	}
}

func TestSparkline(t *testing.T) {
	g := NewTheme("unicode").G
	for _, tc := range []struct {
		name    string
		samples []int64
		width   int
		want    string
	}{
		{"none", nil, 5, "     "},
		{"no speed draws nothing", []int64{0, 0, 0}, 5, "     "},
		{"flat", []int64{5, 5, 5}, 5, "  ███"},
		{"ramp", []int64{0, 1, 2, 3, 4, 5, 6, 7}, 8, "▁▂▃▄▅▆▇█"},
		{"more than width keeps peaks", []int64{0, 0, 8, 0, 4, 4}, 3, "▁█▅"},
	} {
		if got := sparkline(g, tc.samples, tc.width); got != tc.want {
			t.Errorf("%s: sparkline(%v, %d) = %q, want %q", tc.name, tc.samples, tc.width, got, tc.want)
		}
	}
}

// Every glyph is one cell wide in every set, so columns line up.
func TestGlyphWidths(t *testing.T) {
	for _, set := range []string{"unicode", "ascii", "nerd"} {
		g := NewTheme(set).G
		all := []string{g.sel, g.ellipsis, g.full, g.empty, g.dotOK, g.dotWait, g.dotFail, g.tabOn, g.tabOff, g.down, g.up}
		all = append(all, g.eighths...)
		all = append(all, g.spark...)
		all = append(all, g.pieces[:]...)
		for _, s := range []store.State{store.Queued, store.Active, store.Paused, store.RetryWait, store.Error, store.Seeding, store.Done, store.Removed} {
			all = append(all, g.icon(s))
		}
		b := g.border
		all = append(all, b.Top, b.Left, b.TopLeft, b.TopRight, b.BottomLeft, b.BottomRight)
		for _, s := range all {
			if w := cellWidth(s); w != 1 {
				t.Errorf("%s: %q is %d cells wide", set, s, w)
			}
		}
	}
}

func TestDumbTerminalUsesASCII(t *testing.T) {
	var m tea.Model = New(options(t))
	m, _ = m.Update(tea.EnvMsg{"TERM=dumb"})
	if got := m.(Model).theme.G.sel; got != NewTheme("ascii").G.sel {
		t.Fatalf("selection mark %q on a dumb terminal, want the ASCII one", got)
	}
}

func TestFooterHintsFollowActions(t *testing.T) {
	var m tea.Model = New(options(t))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
	m, _ = m.Update(connectedMsg{d: newFakeDaemon(), snap: daemon.Snapshot{Jobs: []*store.Job{
		job(1, "a.iso", store.Done, daemon.ActRemove, daemon.ActOpen),
		job(2, "b.iso", store.Paused, daemon.ActResume, daemon.ActRemove),
	}, Summary: daemon.Summary{Done: 1, Waiting: 1}}})
	m, _ = m.Update(key("3"))
	if hs := m.(Model).hints(); hasHint(hs, actToggle) {
		t.Fatalf("a finished job offers %v", hs)
	}
	m, _ = m.Update(key("2"))
	hs := m.(Model).hints()
	if !hasHint(hs, actToggle) || hintLabel(hs, actToggle) != hintResume {
		t.Fatalf("a paused job offers %v, want resume", hs)
	}
}

func hasHint(hs []hint, action string) bool { return hintLabel(hs, action) != "" }

func hintLabel(hs []hint, action string) string {
	for _, h := range hs {
		if h.action == action {
			return h.label
		}
	}
	return ""
}

// Clicks find rows, tabs, footer keys and panes through the same layers
// the screen is drawn from.
func TestHitFindsLayers(t *testing.T) {
	var m tea.Model = New(options(t))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
	d := sampleDaemon()
	m, _ = m.Update(connectedMsg{d: d, snap: d.snap})
	c := m.(Model).compose()
	for _, tc := range []struct {
		x, y     int
		kind, id string
	}{
		{5, 5, "row", "ubuntu-26.04-desktop-amd64.iso"},
		{5, 6, "row", "ubuntu-26.04-desktop-amd64.iso"},
		{5, 7, "pane", "list"},
		{5, 8, "row", "conference-talk-1080p.mkv"},
		{100, 10, "pane", "details"},
		{20, 2, "tab", "1"},
		{117, 31, "btn", actQuit},
		{0, 0, "", ""},
	} {
		if kind, id := hit(c, tc.x, tc.y); kind != tc.kind || id != tc.id {
			t.Errorf("hit(%d, %d) = %s:%s, want %s:%s", tc.x, tc.y, kind, id, tc.kind, tc.id)
		}
	}
}
