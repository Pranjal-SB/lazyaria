package ipc

import (
	"bytes"
	"errors"
	"io"

	"github.com/creachadair/jrpc2/channel"
)

var ErrFrameTooLarge = errors.New("ipc message larger than 32 MiB")

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
