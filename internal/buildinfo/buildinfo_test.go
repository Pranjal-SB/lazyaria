package buildinfo

import (
	"runtime/debug"
	"testing"
)

func TestModuleVersion(t *testing.T) {
	vcs := []debug.BuildSetting{{Key: "vcs.revision", Value: "abc"}}
	for _, c := range []struct {
		name, ldflags, module string
		settings              []debug.BuildSetting
		want                  string
	}{
		{"ldflags win", "0.2.0", "v0.1.0", nil, "0.2.0"},
		{"go install tag", "dev", "v0.1.0", nil, "0.1.0"},
		{"go install pseudo-version", "dev", "v0.0.0-20261127000000-abcdefabcdef", nil, "0.0.0-20261127000000-abcdefabcdef"},
		{"local checkout", "dev", "v0.1.1-0.20261127000000-abcdefabcdef+dirty", vcs, "dev"},
		{"no module version", "dev", "(devel)", nil, "dev"},
		{"empty", "dev", "", nil, "dev"},
	} {
		info := &debug.BuildInfo{Main: debug.Module{Version: c.module}, Settings: c.settings}
		if got := moduleVersion(c.ldflags, info); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
