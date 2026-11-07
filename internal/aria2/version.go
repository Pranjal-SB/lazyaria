package aria2

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"regexp"
	"strconv"
	"time"
)

var (
	ErrNotFound      = errors.New("aria2c not found")
	ErrTooOld        = errors.New("aria2c is too old")
	ErrNotExecutable = errors.New("aria2c is not executable")
)

var versionTimeout = 5 * time.Second

const (
	minVersion = "1.35.0"
	fixHint    = "install aria2 (e.g. `sudo pacman -S aria2`) or set engine.aria2c_path; run `lazyaria doctor`"
)

type tooOldError struct{ have string }

func (e *tooOldError) Error() string {
	return fmt.Sprintf("aria2c %s is too old; need ≥ %s: upgrade aria2 or set engine.aria2c_path; run `lazyaria doctor`", e.have, minVersion)
}
func (e *tooOldError) Is(t error) bool { return t == ErrTooOld }

var versionRe = regexp.MustCompile(`(?m)^aria2 version (\d+)\.(\d+)\.(\d+)`)

// CheckVersion runs `<path> --version` and returns the version string.
func CheckVersion(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), versionTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	cmd.WaitDelay = time.Second // do not hang on a grandchild holding the pipe
	out, err := cmd.Output()
	if err != nil {
		switch {
		case ctx.Err() != nil:
			return "", fmt.Errorf("aria2: %q --version timed out after %s: %s", path, versionTimeout, fixHint)
		case errors.Is(err, fs.ErrNotExist):
			return "", fmt.Errorf("%w at %q: %s", ErrNotFound, path, fixHint)
		case errors.Is(err, fs.ErrPermission):
			return "", fmt.Errorf("%w at %q: check file permissions or set engine.aria2c_path; run `lazyaria doctor`", ErrNotExecutable, path)
		}
		return "", fmt.Errorf("aria2: %q --version failed: %v: %s", path, err, fixHint)
	}
	m := versionRe.FindStringSubmatch(string(out))
	if m == nil {
		return "", fmt.Errorf("aria2: no `aria2 version X.Y.Z` line in output of %q: %s", path, fixHint)
	}
	v := [3]int{}
	for i := range v {
		v[i], _ = strconv.Atoi(m[i+1])
	}
	have := fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2])
	if v[0] < 1 || (v[0] == 1 && v[1] < 35) {
		return "", &tooOldError{have: have}
	}
	return have, nil
}
