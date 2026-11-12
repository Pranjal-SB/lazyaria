package ipc

import (
	"errors"
	"os"
)

var errWindows = errors.New("the daemon is not supported on windows; run lazyaria on linux, freebsd or macos")

func dirIsPrivate(os.FileInfo) bool { return true }

func takeLock(string) (*Lock, error) { return nil, errWindows }
