package supervisor

import (
	"context"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/config"
)

// altValues are changed values for live keys whose type alone does not
// say what aria2 would accept.
var altValues = map[string]string{
	"downloads.user_agent": "lazyaria-test",
	"network.all_proxy":    "http://127.0.0.1:1/", // aria2 adds the slash
	"network.no_proxy":     "example.com",
}

// alternative returns a valid value for e that differs from its default.
func alternative(e config.Entry) []string {
	if v, ok := altValues[e.Key]; ok {
		return []string{v}
	}
	switch v := e.Value.(type) {
	case bool:
		return []string{strconv.FormatBool(!v)}
	case int:
		return []string{strconv.Itoa(v + 1), strconv.Itoa(v - 1)}
	case float64:
		return []string{strconv.FormatFloat(v+1, 'f', -1, 64)}
	case string:
		if len(e.Enum) > 0 {
			return slices.DeleteFunc(slices.Clone(e.Enum), func(x string) bool { return x == v })
		}
		return []string{"2M", "7s", "x"}
	}
	return nil
}

// Every live key that ends up as an aria2 option must really change in a
// running aria2: aria2.changeGlobalOption ignores some options silently,
// and such a key must not be marked live.
func TestLiveKeysReachAria2(t *testing.T) {
	_, c := startSupervisor(t)
	base, p := testSettings(t)
	for _, e := range config.Schema() {
		if !e.Live {
			continue
		}
		var opts map[string]string
		var mod *config.Settings
		for _, alt := range alternative(e) {
			mod = config.WithLive(base, base)
			if config.Set(mod, e.Key, alt) != nil {
				continue
			}
			opts = ChangedOptions(base, mod, p)
			break
		}
		if len(opts) == 0 {
			continue // not an aria2 option
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		if err := c.Call(ctx, "aria2.changeGlobalOption", []any{opts}, nil); err != nil {
			t.Errorf("%s: changeGlobalOption(%v): %v", e.Key, opts, err)
		}
		var got map[string]string
		if err := c.Call(ctx, "aria2.getGlobalOption", nil, &got); err != nil {
			t.Fatal(err)
		}
		cancel()
		for k, v := range opts {
			if got[k] != v {
				t.Errorf("%s is live but aria2 kept %s=%q after changeGlobalOption(%s=%q)", e.Key, k, got[k], k, v)
			}
		}
		// Put it back so the next key starts from the defaults.
		ctx, cancel = context.WithTimeout(context.Background(), 3*time.Second)
		c.Call(ctx, "aria2.changeGlobalOption", []any{ChangedOptions(mod, base, p)}, nil)
		cancel()
	}
}
