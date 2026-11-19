//go:build !linux && !darwin

package fsutil

func renameNoReplace(string, string) error { return errNoAtomic }
