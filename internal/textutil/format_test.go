package textutil

import (
	"testing"
	"time"
)

func TestBytes(t *testing.T) {
	for _, tc := range []struct {
		n    int64
		si   bool
		want string
	}{
		{0, false, "0 B"},
		{1023, false, "1023 B"},
		{1024, false, "1.0 KiB"},
		{1536, false, "1.5 KiB"},
		{1<<20 - 1, false, "1.0 MiB"},
		{1 << 20, false, "1.0 MiB"},
		{6 << 30, false, "6.0 GiB"},
		{999, true, "999 B"},
		{1000, true, "1.0 kB"},
		{999_999, true, "1.0 MB"},
		{1_500_000, true, "1.5 MB"},
		{6_000_000_000, true, "6.0 GB"},
	} {
		if got := Bytes(tc.n, tc.si); got != tc.want {
			t.Errorf("Bytes(%d, %v) = %q, want %q", tc.n, tc.si, got, tc.want)
		}
	}
}

func TestDuration(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{0, "0 s"},
		{45 * time.Second, "45 s"},
		{59*time.Second + 600*time.Millisecond, "1 min"},
		{90 * time.Second, "2 min"},
		{59 * time.Minute, "59 min"},
		{time.Hour, "1 h"},
		{72 * time.Minute, "1 h 12 min"},
		{26 * time.Hour, "1 d 2 h"},
		{48 * time.Hour, "2 d"},
	} {
		if got := Duration(tc.d); got != tc.want {
			t.Errorf("Duration(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}
