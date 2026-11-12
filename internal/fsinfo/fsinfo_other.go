//go:build !linux && !freebsd && !darwin

// Package fsinfo answers small questions about the filesystem behind a directory.
package fsinfo

import "errors"

var errUnsupported = errors.New("filesystem info is not supported on this platform")

func FreeBytes(string) (uint64, error) { return 0, errUnsupported }

func Dev(string) (uint64, error) { return 0, errUnsupported }

func Allocation(string) string { return "none" }

func IsFAT(string) bool { return false }

func TypeName(string) string { return "unknown" }
