//go:build !linux

package trash

func renameNoReplace(string, string) error { return errNoAtomic }
