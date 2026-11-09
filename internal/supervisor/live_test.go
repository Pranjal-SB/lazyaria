package supervisor

import (
	"testing"

	"github.com/Pranjal-SB/lazyaria/internal/config"
)

func TestChangedOptions(t *testing.T) {
	prev, _, _, err := config.Load("", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	cur := *prev
	cur.Downloads.MaxConcurrent = 2
	cur.Limits.GlobalDown = "1M"
	p := config.Paths{StateDir: t.TempDir(), DownloadDir: t.TempDir()}
	got := ChangedOptions(prev, &cur, p)
	want := map[string]string{"max-concurrent-downloads": "2", "max-overall-download-limit": "1048576"}
	if len(got) != len(want) {
		t.Fatalf("ChangedOptions = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}
