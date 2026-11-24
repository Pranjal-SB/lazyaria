package cli

import (
	"errors"
	"slices"
	"testing"
)

func TestParseJobRef(t *testing.T) {
	for _, tc := range []struct {
		in     string
		num    int64
		prefix string
	}{
		{"#14", 14, ""},
		{"14", 14, ""},
		{"01KA", 0, "01KA"},
		{"01ka", 0, "01KA"},
	} {
		num, prefix, err := parseJobRef(tc.in)
		if err != nil || num != tc.num || prefix != tc.prefix {
			t.Errorf("parseJobRef(%q) = %d, %q, %v; want %d, %q", tc.in, num, prefix, err, tc.num, tc.prefix)
		}
	}
	for _, in := range []string{"#", "#x", "-3", "01K", "#99999999999999999999"} {
		if _, _, err := parseJobRef(in); !errors.As(err, new(usageError)) {
			t.Errorf("parseJobRef(%q) error %v, want a usage error", in, err)
		}
	}
}

func TestResolveByNumber(t *testing.T) {
	f := startFake(t)
	f.jobs = sampleJobs()
	code, out, errOut := run(t, Pause, "", "1")
	if code != 0 || out != "paused #1\n" || !slices.Equal(f.paused, []string{f.jobs[0].ID}) {
		t.Fatalf("exit %d, stdout %q, stderr %q, paused %v; want job #1 paused", code, out, errOut, f.paused)
	}
	if code, out, _ := run(t, Pause, "", "#2"); code != 0 || out != "#2 has failed; nothing to pause\n" {
		t.Fatalf("#2: exit %d, stdout %q", code, out)
	}
}

func TestResolveNumberUnknown(t *testing.T) {
	f := startFake(t)
	f.jobs = sampleJobs()
	code, _, errOut := run(t, Pause, "", "99")
	if want := "lazyaria pause: no job #99; see `lazyaria list --all`\n"; code != 2 || errOut != want {
		t.Fatalf("exit %d, stderr %q; want 2 and %q", code, errOut, want)
	}
}
