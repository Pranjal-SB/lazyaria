package config

import "os"

// Windows has no uid or unix modes; per-user profile ACLs stand in, so no check.
func ownerUID(os.FileInfo) (int, bool) { return os.Getuid(), true }

func modeOK(os.FileInfo) bool { return true }
