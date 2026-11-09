package ipc

import (
	"bytes"
	"errors"
	"io"
	"net"
	"time"

	"github.com/creachadair/jrpc2/channel"
)

var ErrFrameTooLarge = errors.New("ipc message larger than 32 MiB; ask for less (e.g. a smaller --limit), or run `lazyaria doctor`")

// writeTimeout is how long a client may leave the daemon's writes unread.
const writeTimeout = 5 * time.Second

// deadlineConn drops a client that stops reading instead of letting it pin
// a pusher goroutine forever.
type deadlineConn struct{ net.Conn }

func (d deadlineConn) Write(p []byte) (int, error) {
	d.SetWriteDeadline(time.Now().Add(writeTimeout))
	n, err := d.Conn.Write(p)
	if err != nil {
		d.Conn.Close()
	}
	return n, err
}

// capReader fails once a line grows past MaxFrame, so a peer cannot make
// channel.Line buffer without bound.
type capReader struct {
	r io.Reader
	n int // bytes since the last newline
}

func (c *capReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	for b := p[:n]; ; {
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			c.n += len(b)
			break
		}
		if c.n+i > MaxFrame {
			return 0, ErrFrameTooLarge
		}
		c.n, b = 0, b[i+1:]
	}
	if c.n > MaxFrame {
		return 0, ErrFrameTooLarge
	}
	return n, err
}

func lineChannel(rwc io.ReadWriteCloser) channel.Channel {
	return channel.Line(&capReader{r: rwc}, rwc)
}
