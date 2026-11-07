package config

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// Overrides supplies runtime overrides (the store's settings table).
type Overrides interface {
	All() (map[string]string, error)
}

// Warning is a non-fatal problem found while loading.
type Warning struct {
	Key  string
	Line int
	Msg  string
}

var (
	sectionRe = regexp.MustCompile(`^\s*\[([A-Za-z0-9_.-]+)\]\s*(#.*)?$`)
	keyRe     = regexp.MustCompile(`^\s*([A-Za-z0-9_-]+)\s*=`)
)

// lineIndex maps "section.key" to its 1-based line in the file.
func lineIndex(src []byte) map[string]int {
	idx := map[string]int{}
	sec := ""
	for i, l := range strings.Split(string(src), "\n") {
		if m := sectionRe.FindStringSubmatch(l); m != nil {
			sec = m[1] + "."
		} else if m := keyRe.FindStringSubmatch(l); m != nil {
			if _, dup := idx[sec+m[1]]; !dup {
				idx[sec+m[1]] = i + 1
			}
		}
	}
	return idx
}

// Load layers default < file < env < overrides < flags. A missing file is not
// an error. It returns the settings, one Entry per key (with its source), and
// warnings for unknown keys and key conflicts.
func Load(path string, env []string, ov Overrides, flags map[string]string) (*Settings, []Entry, []Warning, error) {
	s := defaults()
	src := map[string]Source{}
	var warns []Warning

	if path != "" {
		w, err := loadFile(s, src, path)
		if err != nil {
			return nil, nil, nil, err
		}
		warns = w
	}
	if err := applyEnv(s, src, env); err != nil {
		return nil, nil, nil, err
	}
	if ov != nil {
		m, err := ov.All()
		if err != nil {
			return nil, nil, nil, fmt.Errorf("reading runtime overrides: %w", err)
		}
		if err := applyMap(s, src, m, SourceOverride, "override", &warns); err != nil {
			return nil, nil, nil, err
		}
	}
	if err := applyMap(s, src, flags, SourceFlag, "flag", &warns); err != nil {
		return nil, nil, nil, err
	}
	warns = append(warns, keyConflicts(s)...)
	return s, withDynamic(s, src), warns, nil
}

func loadFile(s *Settings, src map[string]Source, path string) ([]Warning, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}
	raw := map[string]any{}
	if _, err := toml.Decode(string(data), &raw); err != nil {
		return nil, fmt.Errorf("config %s: %w (run `lazyaria config path` to find the file)", path, err)
	}
	lines := lineIndex(data)
	var warns []Warning
	for _, k := range sortedKeys(raw) {
		if sec, ok := raw[k].(map[string]any); ok {
			for _, sk := range sortedKeys(sec) {
				if err := fileValue(s, src, k+"."+sk, sec[sk], lines, &warns); err != nil {
					return nil, err
				}
			}
		} else if err := fileValue(s, src, k, raw[k], lines, &warns); err != nil {
			return nil, err
		}
	}
	sort.SliceStable(warns, func(i, j int) bool { return warns[i].Line < warns[j].Line })
	return warns, nil
}

func fileValue(s *Settings, src map[string]Source, key string, v any, lines map[string]int, warns *[]Warning) error {
	f := lookup(key)
	if f == nil {
		*warns = append(*warns, Warning{Key: key, Line: lines[key], Msg: "unknown setting"})
		return nil
	}
	val, err := f.fromTOML(v)
	if err == nil {
		err = f.check(val)
	}
	if err != nil {
		return f.fail("", lines[key], err)
	}
	f.set(s, val)
	src[key] = SourceFile
	return nil
}

func applyEnv(s *Settings, src map[string]Source, env []string) error {
	byName := map[string]*field{}
	for _, f := range fields() {
		byName[EnvName(f.key)] = f
	}
	for _, kv := range env {
		name, raw, _ := strings.Cut(kv, "=")
		if f := byName[name]; f != nil {
			if err := assign(s, src, f, raw, SourceEnv, name); err != nil {
				return err
			}
		}
	}
	return nil
}

// EnvName is the environment variable for a key: downloads.dir -> LAZYARIA_DOWNLOADS_DIR.
func EnvName(key string) string {
	return "LAZYARIA_" + strings.ToUpper(strings.ReplaceAll(key, ".", "_"))
}

func applyMap(s *Settings, src map[string]Source, m map[string]string, kind Source, where string, warns *[]Warning) error {
	for _, k := range sortedKeys(m) {
		f := lookup(k)
		if f == nil {
			*warns = append(*warns, Warning{Key: k, Msg: "unknown setting in " + where})
			continue
		}
		if err := assign(s, src, f, m[k], kind, where); err != nil {
			return err
		}
	}
	return nil
}

func assign(s *Settings, src map[string]Source, f *field, raw string, kind Source, where string) error {
	v, err := f.fromString(raw)
	if err == nil {
		err = f.check(v)
	}
	if err != nil {
		return f.fail(where, 0, err)
	}
	f.set(s, v)
	src[f.key] = kind
	return nil
}

// Set validates raw and stores it in s. It is used by `config set` and the TUI.
func Set(s *Settings, key, raw string) error {
	f := lookup(key)
	if f == nil {
		return fmt.Errorf("unknown setting %q (run `lazyaria config list` to see all keys)", key)
	}
	return assign(s, map[string]Source{}, f, raw, SourceOverride, "")
}

// withDynamic returns the schema entries plus one entry per custom keys.* and
// theme.* binding.
func withDynamic(s *Settings, src map[string]Source) []Entry {
	out := entries(s, src)
	for _, a := range sortedKeys(s.Keys) {
		if a != "preset" {
			out = append(out, lookup("keys."+a).entry(s, src["keys."+a]))
		}
	}
	for _, r := range sortedKeys(s.Theme) {
		out = append(out, lookup("theme."+r).entry(s, src["theme."+r]))
	}
	return out
}

// keyConflicts reports one key bound to several actions.
func keyConflicts(s *Settings) []Warning {
	owner := map[string]string{}
	var w []Warning
	for _, a := range sortedKeys(s.Keys) {
		if a == "preset" {
			continue
		}
		for _, k := range s.Keys[a] {
			if o, ok := owner[k]; ok {
				w = append(w, Warning{Key: "keys." + a, Msg: fmt.Sprintf("key %q is bound to both %s and %s", k, o, a)})
			}
			owner[k] = a
		}
	}
	return w
}

func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
