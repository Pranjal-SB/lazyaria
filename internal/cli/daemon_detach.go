package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"syscall"
)

// detachDaemon starts the daemon in its own session with stdio on
// /dev/null, so a closed terminal or CLI pipe can never kill it, and
// returns once it reports that it is serving. A failed start is printed
// here, on the caller's stderr, with exit code 1.
func detachDaemon(mode string, errOut io.Writer) int {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(errOut, "lazyaria daemon: cannot find my own executable:", err)
		return 1
	}
	r, w, err := os.Pipe()
	if err != nil {
		fmt.Fprintln(errOut, "lazyaria daemon:", err)
		return 1
	}
	defer r.Close()
	args := []string{"daemon"}
	if mode != "" {
		args = append(args, "--mode", mode)
	}
	cmd := exec.Command(exe, args...)
	cmd.Dir = "/" // do not keep the caller's directory busy
	// nil stdio is /dev/null; the pipe becomes fd 3.
	cmd.ExtraFiles = []*os.File{w}
	// It is not a systemd service even when we are, so it logs to its file.
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, "INVOCATION_ID=") })
	cmd.Env = append(env, statusFDEnv+"=3")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	err = cmd.Start()
	w.Close()
	if err != nil {
		fmt.Fprintln(errOut, "lazyaria daemon: start:", err)
		return 1
	}
	go cmd.Wait() // reap it if it dies before we return

	msg, _ := io.ReadAll(r) // ends when the daemon reports or exits
	switch s := strings.TrimSpace(string(msg)); s {
	case "ready":
		return 0
	case "":
		fmt.Fprintln(errOut, "lazyaria daemon: the daemon exited during start-up; see `lazyaria logs` or run `lazyaria doctor`")
	default:
		fmt.Fprintln(errOut, s)
	}
	return 1
}

func setCloseOnExec(fd int) { syscall.CloseOnExec(fd) }
