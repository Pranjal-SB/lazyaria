package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/Pranjal-SB/lazyaria/internal/daemon"
	"github.com/Pranjal-SB/lazyaria/internal/engine"
)

func TestRefresh(t *testing.T) {
	f := startFake(t)
	f.jobs = sampleJobs()
	code, out, errOut := run(t, Refresh, "", "01jc0000000000000000000001", "https://example.com/new.iso")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if want := []string{f.jobs[0].ID, "https://example.com/new.iso"}; !slices.Equal(f.refreshed, want) {
		t.Fatalf("refreshed %v, want %v", f.refreshed, want)
	}
	if !strings.Contains(out, "refreshed "+f.jobs[0].ID) {
		t.Errorf("stdout %q", out)
	}
	for _, args := range [][]string{{"01JC0000000000000000000001"}, {"01JC0000000000000000000001", "https://a/b", "https://a/c"}, {"01J", "https://a/b"}} {
		if code, _, errOut := run(t, Refresh, "", args...); code != 2 {
			t.Errorf("%v: exit %d, want 2: %s", args, code, errOut)
		}
	}
}

// A refresh the daemon refuses as wrong for the job, such as a torrent job
// given an http link, is a usage error like a bad link refused locally.
func TestRefreshMisfitExitsUsage(t *testing.T) {
	f := startFake(t)
	f.jobs = sampleJobs()
	f.refreshErr = &daemon.BadRequestError{Err: engine.ErrNotSupported}
	code, _, errOut := run(t, Refresh, "", "01JC0000000000000000000001", "https://example.com/new.iso")
	if code != 2 || !strings.Contains(errOut, engine.ErrNotSupported.Error()) || strings.Contains(errOut, "[-") {
		t.Fatalf("exit %d, stderr %q; want 2 and the reason alone", code, errOut)
	}
}

// A link the daemon would refuse is a usage error, with the reason.
func TestRefreshBadLink(t *testing.T) {
	f := startFake(t)
	f.jobs = sampleJobs()
	for link, want := range map[string]string{
		"ftp:/x":                  "not a valid URL",
		"notalink":                "not a valid URL",
		"magnet:?xt=urn:btih:abc": "magnets cannot be refreshed",
		"file:///etc/passwd":      "http(s), ftp or sftp",
	} {
		code, _, errOut := run(t, Refresh, "", "01JC0000000000000000000001", link)
		if code != 2 || !strings.Contains(errOut, want) {
			t.Errorf("%s: exit %d, stderr %q; want 2 and %q", link, code, errOut, want)
		}
	}
	if len(f.refreshed) != 0 {
		t.Fatalf("bad links reached the daemon: %v", f.refreshed)
	}
}
