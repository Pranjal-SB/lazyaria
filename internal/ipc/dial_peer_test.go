package ipc

import (
	"context"
	"errors"
	"net"
	"testing"
)

func TestDialReportsPeerCheckFailure(t *testing.T) {
	p := testPaths(shortDir(t))
	l, lock, err := Listen(p)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	defer l.Close()
	go func() {
		if c, err := l.Accept(); err == nil {
			c.Close()
		}
	}()
	boom := errors.New("no credentials")
	old := clientPeerUID
	clientPeerUID = func(net.Conn) (int, error) { return -1, boom }
	defer func() { clientPeerUID = old }()
	_, err = Dial(context.Background(), p)
	var fd *ErrForeignDaemon
	if !errors.Is(err, boom) || errors.As(err, &fd) {
		t.Fatalf("want the peerUID error, got %v", err)
	}
}
