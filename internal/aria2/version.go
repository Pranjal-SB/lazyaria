package aria2

import (
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

var (
	ErrNotFound = errors.New("aria2c not found")
	ErrTooOld   = errors.New("aria2c is too old")
)

const minVersion = "1.35.0"

type tooOldError struct{ have string }

func (e *tooOldError) Error() string {
	return fmt.Sprintf("aria2c %s is too old; need ≥ %s (upgrade aria2, see lazyaria doctor)", e.have, minVersion)
}
func (e *tooOldError) Is(t error) bool { return t == ErrTooOld }

var versionRe = regexp.MustCompile(`^aria2 version (\d+)\.(\d+)\.(\d+)`)

// CheckVersion runs `<path> --version` and returns the version string.
func CheckVersion(path string) (string, error) {
	out, err := exec.Command(path, "--version").Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return "", fmt.Errorf("aria2: %s --version failed: %w", path, err)
		}
		return "", fmt.Errorf("%w at %q: install aria2 or set the aria2 path (see lazyaria doctor)", ErrNotFound, path)
	}
	line, _, _ := strings.Cut(string(out), "\n")
	m := versionRe.FindStringSubmatch(line)
	if m == nil {
		return "", fmt.Errorf("aria2: cannot parse version from %q (see lazyaria doctor)", line)
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
