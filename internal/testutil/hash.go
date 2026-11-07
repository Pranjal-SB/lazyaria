package testutil

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"testing"
)

// SHA256File returns the lowercase hex SHA-256 of the file at path.
func SHA256File(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatalf("hash %s: %v", path, err)
	}
	return hex.EncodeToString(h.Sum(nil))
}
