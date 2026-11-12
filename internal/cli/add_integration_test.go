//go:build linux

package cli_test

import (
	"errors"
	"os/exec"
	"testing"

	"github.com/Pranjal-SB/lazyaria/internal/testutil"
)

func TestAddWaitExitCodes(t *testing.T) {
	testutil.RequireAria2(t)
	srv := testutil.NewFaultServer(t, map[string][]byte{"a.bin": testutil.RandBytes(128 << 10), "b.bin": testutil.RandBytes(1024)})
	e := newEnv(t, "[retry]\nmax_tries = 1\nmax_file_not_found = 1\n[daemon]\nidle_exit = \"1s\"\n")
	for _, tc := range []struct {
		name string
		mode testutil.Mode
		want int
	}{{"a.bin", testutil.ModeOK, 0}, {"b.bin", testutil.Mode404, 1}} {
		out, err := e.cmd("add", "--wait", srv.URL(tc.name, tc.mode)).CombinedOutput()
		code := 0
		if ee, ok := errors.AsType[*exec.ExitError](err); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		if code != tc.want {
			t.Errorf("add --wait %s: exit %d, want %d\n%s", tc.mode, code, tc.want, out)
		}
	}
}
