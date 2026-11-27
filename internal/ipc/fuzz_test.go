package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"slices"
	"strings"
	"testing"
	"time"
)

const sentinel = `{"jsonrpc":"2.0","id":"sentinel","method":"hello","params":{"client":"t","version":"dev"}}`

// exchange feeds line to a new server connection, followed by a hello,
// and returns once the hello is answered or the server hangs up. Every
// reply must be valid JSON.
func exchange(t *testing.T, s *server, line string) {
	t.Helper()
	cli, srv := net.Pipe()
	done := make(chan struct{})
	go func() { s.serveConn(context.Background(), srv); close(done) }()
	defer func() { cli.Close(); <-done }()
	cli.SetDeadline(time.Now().Add(10 * time.Second))
	go fmt.Fprint(cli, line+"\n"+sentinel+"\n") // net.Pipe blocks until the server reads
	r := bufio.NewReaderSize(cli, 1<<20)
	for {
		reply, err := r.ReadBytes('\n')
		if len(reply) > 0 && !json.Valid(reply) {
			t.Fatalf("reply to %.200q is not JSON: %.200q", line, reply)
		}
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				t.Fatalf("no answer to the hello after %.200q", line)
			}
			return // hung up
		}
		if strings.Contains(string(reply), `"sentinel"`) {
			return
		}
	}
}

func FuzzServeLine(f *testing.F) {
	s := &server{h: newFake()}
	probe := (&conn{s: s}).methods()
	var names []string
	for name := range probe {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		f.Add(fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":%q,"params":{}}`, name))
	}
	for _, line := range []string{
		`[{"jsonrpc":"2.0","id":1,"method":"daemon.status"},{"jsonrpc":"2.0","id":2,"method":"nope"}]`,
		`{"jsonrpc":"2.0","id":1,"method":"jobs.pause","params":null}`,
		`{"jsonrpc":"2.0","id":1,"method":"jobs.pause","params":{"ids":5}}`,
		`{"jsonrpc":"2.0","id":123456789012345678901234567890,"method":"daemon.status"}`,
		`{"jsonrpc":"2.0","id":1,"method":"no.such.method"}`,
		`"just a string"`, `42`, `null`, `[]`, `{`, ``,
		`{"jsonrpc":"2.0","id":1,"method":"jobs.add","params":{"uris":["` + strings.Repeat("a", 1<<20) + `"]}}`,
	} {
		f.Add(line)
	}
	f.Fuzz(func(t *testing.T, line string) {
		exchange(t, s, line)
		exchange(t, s, "") // a fresh connection still works
	})
}
