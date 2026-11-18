package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

const (
	tailLines  = 50
	followPoll = 250 * time.Millisecond
)

// Logs runs `lazyaria logs [-f]`: the end of the daemon log, in human
// format, and with -f everything written after.
func Logs(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := newFlags("logs", errOut)
	follow := fs.Bool("f", false, "keep printing new lines")
	if err := parseNoArgs(fs, args); err != nil {
		return flagExit(err)
	}
	_, p, err := resolveLocal(os.Environ(), nil)
	if err != nil {
		return fail(errOut, "logs", err)
	}
	f, err := os.Open(p.Log)
	if errors.Is(err, os.ErrNotExist) {
		return fail(errOut, "logs", fmt.Errorf("no log at %s yet. Under systemd the daemon logs to the journal: journalctl --user -u lazyaria", p.Log))
	}
	if err != nil {
		return fail(errOut, "logs", err)
	}
	defer func() { f.Close() }()

	var tail []string
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			tail = append(tail, humanLog(line))
			if len(tail) > tailLines {
				tail = tail[1:]
			}
		}
		if err != nil {
			break
		}
	}
	for _, l := range tail {
		fmt.Fprintln(out, l)
	}
	if !*follow {
		return exitOK
	}
	offset, _ := f.Seek(0, io.SeekCurrent)
	var partial []byte
	tick := time.NewTicker(followPoll)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return exitOK
		case <-tick.C:
		}
		// Rotation renames the file away; start over on the new one.
		if fi, err := os.Stat(p.Log); err == nil && (fi.Size() < offset || !sameFile(f, fi)) {
			if nf, err := os.Open(p.Log); err == nil {
				f.Close()
				f, offset, partial = nf, 0, nil
			}
		}
		b, _ := io.ReadAll(f)
		offset += int64(len(b))
		partial = append(partial, b...)
		for {
			i := bytes.IndexByte(partial, '\n')
			if i < 0 {
				break
			}
			fmt.Fprintln(out, humanLog(partial[:i]))
			partial = partial[i+1:]
		}
	}
}

func sameFile(f *os.File, fi os.FileInfo) bool {
	cur, err := f.Stat()
	return err == nil && os.SameFile(cur, fi)
}

// humanLog turns one slog JSON line into "time LEVEL msg key=value...",
// keeping the attribute order. Anything else comes back as it is.
func humanLog(line []byte) string {
	line = bytes.TrimRight(line, "\r\n")
	dec := json.NewDecoder(bytes.NewReader(line))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return string(line)
	}
	var when, level, msg string
	var attrs []string
	for dec.More() {
		k, err := dec.Token()
		var v json.RawMessage
		if err != nil || dec.Decode(&v) != nil {
			return string(line)
		}
		var s string
		isStr := json.Unmarshal(v, &s) == nil
		switch key := k.(string); {
		case key == "time" && isStr:
			if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
				s = t.Local().Format("2006-01-02 15:04:05")
			}
			when = s
		case key == "level" && isStr:
			level = s
		case key == "msg" && isStr:
			msg = s
		case isStr && s != "" && !strings.ContainsAny(s, " \t\"="):
			attrs = append(attrs, key+"="+s)
		default:
			attrs = append(attrs, key+"="+string(v))
		}
	}
	return strings.TrimRight(fmt.Sprintf("%s %-5s %s %s", when, level, msg, strings.Join(attrs, " ")), " ")
}
