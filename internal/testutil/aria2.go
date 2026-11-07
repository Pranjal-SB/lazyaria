package testutil

import (
	"os/exec"
	"testing"
)

// RequireAria2 returns the path to aria2c, or skips the test when it is not on PATH.
func RequireAria2(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("aria2c")
	if err != nil {
		t.Skip("aria2c not found on PATH")
		return ""
	}
	return p
}
