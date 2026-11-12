package fsinfo

import "testing"

func TestFreeBytesPositive(t *testing.T) {
	n, err := FreeBytes(t.TempDir())
	if err != nil || n == 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestDev(t *testing.T) {
	if _, err := Dev(t.TempDir()); err != nil {
		t.Fatal(err)
	}
}

func TestAllocationTmpfs(t *testing.T) {
	got := Allocation(t.TempDir())
	if got == "none" {
		t.Skip("filesystem not in the falloc list")
	}
	if got != "falloc" {
		t.Fatalf("got %q", got)
	}
}

func TestIsFATFalse(t *testing.T) {
	if IsFAT(t.TempDir()) {
		t.Skip("temp dir is on FAT")
	}
}

func TestTypeName(t *testing.T) {
	if got := TypeName(t.TempDir()); got == "" || got == "unknown" {
		t.Fatalf("TypeName = %q", got)
	}
}
