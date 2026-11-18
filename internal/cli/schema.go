package cli

import (
	"strings"

	"github.com/Pranjal-SB/lazyaria/internal/config"
	"github.com/Pranjal-SB/lazyaria/internal/ipc"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

// The documents below are what --json prints. They are a public interface
// (spec §10, G28): change them only together with the golden files in
// testdata/cli. Field names are snake_case like the IPC wire format.

// listDoc is `lazyaria list --json`. Jobs use the IPC job shape: no
// torrent blob, auth options masked by the daemon, link userinfo masked
// by the CLI.
type listDoc struct {
	Jobs []*store.Job `json:"jobs"`
}

// statusDoc is `lazyaria status --json`.
type statusDoc struct {
	DaemonVersion string `json:"daemon_version"`
	Protocol      int    `json:"protocol"`
	ipc.Status
}

// settingDoc is one entry of `lazyaria config list --json`, the
// machine-readable settings schema.
type settingDoc struct {
	Key     string   `json:"key"`
	Type    string   `json:"type"` // string|bool|int|float|list
	Value   any      `json:"value"`
	Default any      `json:"default"`
	Source  string   `json:"source"` // default|file|env|override|flag
	Live    bool     `json:"live"`
	Enum    []string `json:"enum"`
	Help    string   `json:"help"`
	Unused  bool     `json:"not_used_yet"` // nothing reads it yet
}

// checkDoc is one row of `lazyaria doctor --json`.
type checkDoc struct {
	Check  string `json:"check"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
	Fix    string `json:"fix"`
}

// schemaByKey is the built-in schema, for the type and default of a key.
func schemaByKey() map[string]config.Entry {
	m := map[string]config.Entry{}
	for _, e := range config.Schema() {
		m[e.Key] = e
	}
	return m
}

func settingDocs(entries []ipc.ConfigEntry) []settingDoc {
	schema := schemaByKey()
	out := make([]settingDoc, len(entries))
	for i, e := range entries {
		def, ok := schema[e.Key]
		typ := "string"
		if ok {
			typ = typeName(def.Value)
		} else if strings.HasPrefix(e.Key, "keys.") {
			typ = "list"
		}
		enum := e.Enum
		if enum == nil {
			enum = []string{}
		}
		out[i] = settingDoc{Key: e.Key, Type: typ, Value: maskSetting(e.Key, e.Value), Default: maskSetting(e.Key, def.Value), Source: e.Source, Live: e.Live, Enum: enum, Help: e.Help, Unused: config.Unused(e.Key)}
	}
	return out
}

func typeName(v any) string {
	switch v.(type) {
	case bool:
		return "bool"
	case int:
		return "int"
	case float64:
		return "float"
	case []string:
		return "list"
	}
	return "string"
}

// knownKey reports whether key is a setting, a built-in or a keys.* /
// theme.* binding.
func knownKey(key string) bool {
	if _, ok := schemaByKey()[key]; ok {
		return true
	}
	name, rest, ok := strings.Cut(key, ".")
	return ok && rest != "" && (name == "keys" || name == "theme")
}
