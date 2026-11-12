package cli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/Pranjal-SB/lazyaria/internal/config"
	"github.com/Pranjal-SB/lazyaria/internal/ipc"
)

const newConfig = "# lazyaria settings. `lazyaria config list` shows every key and its default.\nversion = 1\n"

// configEdit opens a copy of config.toml in $VISUAL/$EDITOR and only puts
// it in place once it loads cleanly, offering another round on errors.
func configEdit(ctx context.Context, out, errOut io.Writer) error {
	_, p, err := resolveLocal(os.Environ(), nil)
	if err != nil {
		return err
	}
	path := p.ConfigFile
	if target, err := filepath.EvalSymlinks(path); err == nil {
		path = target // keep a dotfiles symlink pointing where it did
	}
	orig, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		orig = []byte(newConfig)
	} else if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.toml")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(orig)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}

	answers := bufio.NewReader(stdin)
	for {
		if err := runEditor(tmp.Name()); err != nil {
			return err
		}
		_, _, warns, lerr := config.Load(tmp.Name(), nil, nil, nil)
		if lerr == nil {
			for _, w := range warns {
				fmt.Fprintf(errOut, "warning: %s (line %d): %s\n", w.Key, w.Line, w.Msg)
			}
			break
		}
		fmt.Fprintf(errOut, "error: %v\nedit again? [Y/n] ", lerr)
		line, _ := answers.ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a == "n" || a == "no" {
			return errors.New("not saved; the config file is unchanged")
		}
	}

	edited, err := os.ReadFile(tmp.Name())
	if err != nil {
		return err
	}
	if bytes.Equal(edited, orig) {
		fmt.Fprintln(out, "no changes")
		return nil
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	fmt.Fprintf(out, "saved %s\n", path)
	if reloadDaemon(ctx, p) {
		fmt.Fprintln(out, "the daemon applied the live settings; the others apply after it restarts")
	}
	return nil
}

// runEditor opens path in the user's editor. The editor may carry
// arguments ("code -w"), so the shell splits it.
func runEditor(path string) error {
	ed := os.Getenv("VISUAL")
	if ed == "" {
		ed = os.Getenv("EDITOR")
	}
	if ed == "" {
		ed = "vi"
	}
	cmd := exec.Command("sh", "-c", ed+` "$1"`, "sh", path)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("editor %q: %w; set $EDITOR", ed, err)
	}
	return nil
}

// reloadDaemon sends SIGHUP to a running daemon so it rereads the file. It
// never starts one.
func reloadDaemon(ctx context.Context, p config.Paths) bool {
	c, err := ipc.Dial(ctx, p)
	if err != nil {
		return false
	}
	defer c.Close()
	st, err := c.Status(ctx)
	return err == nil && st.PID > 0 && syscall.Kill(st.PID, syscall.SIGHUP) == nil
}
