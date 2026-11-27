package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/BurntSushi/toml"
)

func FuzzLoad(f *testing.F) {
	for _, s := range []string{
		"",
		"[downloads]\ndir = \"/tmp/x\"\n",
		"[keys]\nquit = [\"q\"]\n",
		"[theme]\naccent = \"#ff0000\"\n",
		"[downloads]\ndir = 5\n",
		"[keys]\nquit = [\"q\"]\n[keys]\nquit = [\"x\"]\n",
		"a = {b = {c = {d = {e = {f = {g = 1}}}}}}\n",
		"[keys]\npreset = \"default\"\nx.y = [\"z\"]\n",
	} {
		f.Add(s)
	}
	path := filepath.Join(f.TempDir(), "config.toml")
	f.Fuzz(func(t *testing.T, data string) {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		_, ents, _, err := Load(path, nil, nil, nil)
		if err != nil {
			return
		}
		for _, e := range ents {
			if lookup(e.Key) == nil {
				t.Fatalf("entry %q has no setting", e.Key)
			}
		}
	})
}

// format writes v the way a user would type it back into Set.
func format(v any) string {
	if l, ok := v.([]string); ok {
		var b strings.Builder
		toml.NewEncoder(&b).Encode(map[string][]string{"v": l})
		return strings.TrimSpace(strings.TrimPrefix(b.String(), "v = "))
	}
	return fmt.Sprint(v)
}

func FuzzSet(f *testing.F) {
	for _, e := range Schema() {
		f.Add(e.Key, format(e.Value))
		f.Add(e.Key, "\x00?bad")
	}
	f.Add("keys.quit", "q,ctrl+c")
	f.Add("theme.accent", "#ff0000")
	f.Fuzz(func(t *testing.T, key, raw string) {
		if !utf8.ValidString(raw) {
			return // TOML cannot hold it, so a list value could not be written back
		}
		s := defaults()
		if err := Set(s, key, raw); err != nil {
			return
		}
		got := lookup(key).get(s)
		again := defaults()
		if err := Set(again, key, format(got)); err != nil {
			t.Fatalf("Set(%q, %q) = %v, but its own value %q is refused: %v", key, raw, got, format(got), err)
		}
		if back := lookup(key).get(again); !reflect.DeepEqual(got, back) {
			t.Fatalf("Set(%q, %q): %#v, then %#v after a round trip", key, raw, got, back)
		}
	})
}

func FuzzParseSize(f *testing.F) {
	for _, s := range []string{"0", "4M", "1.5G", "1e300K", "NaN", "-1", "9223372036854775807", "8E", "1e18T", "", "K"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if n, err := ParseSize(s); err == nil && n < 0 {
			t.Fatalf("ParseSize(%q) = %d", s, n)
		}
	})
}

func FuzzParseDuration(f *testing.F) {
	for _, s := range []string{"30s", "-1s", "1e9h", "0", "", "9223372036854775807h", "1h30m"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if d, err := ParseDuration(s); err == nil && d < 0 {
			t.Fatalf("ParseDuration(%q) = %v", s, d)
		}
	})
}
