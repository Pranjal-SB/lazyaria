package ipc

import "net"

func Handoff(net.Listener, *Lock) (listenFD, lockFD int, err error) { return 0, 0, errWindows }

func KeepOnExec(...int) error { return errWindows }

func Adopt(int, int) (net.Listener, *Lock, error) { return nil, nil, errWindows }
