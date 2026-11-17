package cli

import (
	"slices"
	"strings"
	"testing"
)

// remove prints the command that undoes it, and undo runs it.
func TestRemoveThenUndo(t *testing.T) {
	f := startFake(t)
	f.jobs = sampleJobs()
	code, out, errOut := run(t, Remove, "", "01JC0000000000000000000001")
	if code != 0 || !strings.Contains(out, "undo: lazyaria undo tok\n") {
		t.Fatalf("exit %d, stdout %q: %s", code, out, errOut)
	}
	if code, out, errOut := run(t, Undo, "", "tok"); code != 0 || !slices.Equal(f.undone, []string{"tok"}) || out == "" {
		t.Fatalf("undo: exit %d, undone %v, stdout %q: %s", code, f.undone, out, errOut)
	}
	for _, args := range [][]string{nil, {"a", "b"}} {
		if code, _, _ := run(t, Undo, "", args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
}
