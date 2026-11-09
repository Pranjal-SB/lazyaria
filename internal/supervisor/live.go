package supervisor

import (
	"strings"

	"github.com/Pranjal-SB/lazyaria/internal/config"
)

// ChangedOptions returns the aria2 options whose conf value differs between
// prev and cur, ready for aria2.changeGlobalOption. An option cur no longer
// sets at all keeps its old value until aria2 restarts.
func ChangedOptions(prev, cur *config.Settings, p config.Paths) map[string]string {
	a, b := confMap(prev, p), confMap(cur, p)
	out := map[string]string{}
	for k, v := range b {
		if a[k] != v {
			out[k] = v
		}
	}
	return out
}

func confMap(s *config.Settings, p config.Paths) map[string]string {
	b := &confBuilder{}
	b.base(s, p, 0, "")
	b.extra(s.Engine.Aria2ExtraArgs)
	m := map[string]string{}
	for _, l := range strings.Split(b.out.String(), "\n") {
		if k, v, ok := strings.Cut(l, "="); ok {
			m[k] = v
		}
	}
	return m
}
