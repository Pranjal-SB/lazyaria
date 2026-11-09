package config

import "maps"

// WithLive returns a copy of cur with every live key taken from next.
// Keys that need a daemon restart keep cur's value. Key bindings and
// theme colours are for clients only, so they are always taken from next.
func WithLive(cur, next *Settings) *Settings {
	out := *cur
	out.Keys, out.Theme = maps.Clone(next.Keys), maps.Clone(next.Theme)
	for _, f := range fields() {
		if f.live && f.idx != nil {
			f.set(&out, f.get(next))
		}
	}
	return &out
}
