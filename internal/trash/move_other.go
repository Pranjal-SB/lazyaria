//go:build !linux && !darwin

package trash

func renameNoReplace(string, string) error { return errNoAtomic }
