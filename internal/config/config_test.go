package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func find(t *testing.T, es []Entry, key string) Entry {
	t.Helper()
	for _, e := range es {
		if e.Key == key {
			return e
		}
	}
	t.Fatalf("entry %q not found", key)
	return Entry{}
}

func writeCfg(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

type ov map[string]string

func (o ov) All() (map[string]string, error) { return o, nil }

func TestDefaults(t *testing.T) {
	es := Schema()
	for _, c := range []struct {
		k string
		v any
	}{
		{"downloads.max_concurrent", 5},
		{"bt.seed_minutes", 0},
		{"ui.units", "iec"},
		{"daemon.mode", "on-demand"},
		{"retry.backoff_factor", 2.0},
	} {
		if got := find(t, es, c.k).Value; got != c.v {
			t.Errorf("%s = %#v, want %#v", c.k, got, c.v)
		}
	}
	for _, e := range es {
		if e.Help == "" {
			t.Errorf("%s has no help", e.Key)
		}
		if e.Source != SourceDefault {
			t.Errorf("%s source = %v", e.Key, e.Source)
		}
	}
}

func TestPrecedence(t *testing.T) {
	p := writeCfg(t, "[downloads]\nmax_concurrent = 3\n")
	env := []string{"LAZYARIA_DOWNLOADS_MAX_CONCURRENT=4", "HOME=/x"}
	o := ov{"downloads.max_concurrent": "6"}
	f := map[string]string{"downloads.max_concurrent": "7"}
	cases := []struct {
		o   Overrides
		f   map[string]string
		v   int
		src string
	}{
		{o, f, 7, "flag"},
		{o, nil, 6, "override"},
		{nil, nil, 4, "env"},
	}
	for _, c := range cases {
		s, es, _, err := Load(p, env, c.o, c.f)
		if err != nil {
			t.Fatal(err)
		}
		e := find(t, es, "downloads.max_concurrent")
		if s.Downloads.MaxConcurrent != c.v || e.Source.String() != c.src {
			t.Errorf("got %d/%v want %d/%v", s.Downloads.MaxConcurrent, e.Source, c.v, c.src)
		}
	}
	s, es, _, _ := Load(p, nil, nil, nil)
	if s.Downloads.MaxConcurrent != 3 || find(t, es, "downloads.max_concurrent").Source.String() != "file" {
		t.Error("file layer wrong")
	}
}

func TestUnknownKeyWarns(t *testing.T) {
	_, _, w, err := Load(writeCfg(t, "[ui]\nfoo = 1\n"), nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(w) != 1 || w[0].Key != "ui.foo" || w[0].Line != 2 {
		t.Fatalf("warnings = %+v", w)
	}
}

func TestBadValueErrors(t *testing.T) {
	_, _, _, err := Load(writeCfg(t, "[ui]\nmouse = \"sometimes\"\n"), nil, nil, nil)
	if err == nil {
		t.Fatal("want error")
	}
	for _, sub := range []string{"ui.mouse", "line 2", "all|cell|off", `default "all"`} {
		if !strings.Contains(err.Error(), sub) {
			t.Errorf("error %q lacks %q", err, sub)
		}
	}
}

func TestRejectRPCExtraArgs(t *testing.T) {
	_, _, _, err := Load(writeCfg(t, "[engine]\naria2_extra_args = [\"--rpc-listen-all=true\"]\n"), nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "rpc-* options are not allowed") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseSize(t *testing.T) {
	for in, want := range map[string]int64{"0": 0, "512K": 524288, "4M": 4194304, "1.5G": 1610612736} {
		if got, err := ParseSize(in); err != nil || got != want {
			t.Errorf("%s = %d, %v", in, got, err)
		}
	}
	if _, err := ParseSize("4X"); err == nil {
		t.Error("4X should fail")
	}
}

func TestLiveFlags(t *testing.T) {
	es := Schema()
	if !find(t, es, "ui.mouse").Live || find(t, es, "engine.aria2c_path").Live {
		t.Error("live flags wrong")
	}
}

func TestKeysAndTheme(t *testing.T) {
	p := writeCfg(t, "[keys]\npreset = \"vim\"\npause_resume = [\"space\"]\nquit = [\"space\"]\n[theme]\naccent = \"#ff00aa\"\n")
	s, _, w, err := Load(p, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.Keys["preset"][0] != "vim" || s.Keys["pause_resume"][0] != "space" || s.Theme["accent"] != "#ff00aa" {
		t.Errorf("keys/theme = %v %v", s.Keys, s.Theme)
	}
	if len(w) != 1 || !strings.Contains(w[0].Msg, "space") {
		t.Errorf("want conflict warning, got %+v", w)
	}
	if _, _, _, err := Load(writeCfg(t, "[keys]\npreset = \"emacs\"\n"), nil, nil, nil); err == nil {
		t.Error("bad preset accepted")
	}
	if _, _, _, err := Load(writeCfg(t, "[theme]\naccent = 99\n"), nil, nil, nil); err == nil {
		t.Error("bad theme accepted")
	}
}

func TestValidFixture(t *testing.T) {
	s, _, w, err := Load("../../testdata/config/valid.toml", nil, nil, nil)
	if err != nil || len(w) != 0 {
		t.Fatalf("err=%v warnings=%v", err, w)
	}
	if s.Downloads.MaxConcurrent != 8 || s.Retry.BackoffFactor != 3 || s.Theme["accent"] != "4" || len(s.Keys["pause_resume"]) != 2 {
		t.Errorf("unexpected settings: %+v", s)
	}
}

func TestSetAndMissingFile(t *testing.T) {
	s, _, _, err := Load(filepath.Join(t.TempDir(), "none.toml"), nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := Set(s, "downloads.min_split_size", "8M"); err != nil || s.Downloads.MinSplitSize != "8M" {
		t.Errorf("set: %v", err)
	}
	if Set(s, "downloads.min_split_size", "8X") == nil || Set(s, "nope.x", "1") == nil ||
		Set(s, "downloads.connections_per_server", "17") == nil {
		t.Error("invalid set accepted")
	}
	if err := Set(s, "retry.auto_retry", "429,503"); err != nil || len(s.Retry.AutoRetry) != 2 {
		t.Errorf("list set: %v", err)
	}
}
