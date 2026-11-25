package textutil

import (
	"fmt"
	"time"
)

// Bytes formats n with IEC units (1024 based, KiB) or, with si, SI units
// (1000 based, kB).
func Bytes(n int64, si bool) string {
	base, units, suffix := 1024.0, "KMGTPE", "iB"
	if si {
		base, units, suffix = 1000, "kMGTPE", "B"
	}
	if float64(n) < base {
		return fmt.Sprintf("%d B", n)
	}
	f, i := float64(n), -1
	// base-0.05 would print as "1024.0 KiB"; step up so it reads "1.0 MiB"
	for f >= base-0.05 && i < len(units)-1 {
		f /= base
		i++
	}
	return fmt.Sprintf("%.1f %c%s", f, units[i], suffix)
}

// Duration formats d in plain words with two units at most: "45 s",
// "2 min", "1 h 12 min", "1 d 2 h". Minutes and up are rounded.
func Duration(d time.Duration) string {
	s := int64(d.Round(time.Second) / time.Second)
	if s < 60 {
		return fmt.Sprintf("%d s", s)
	}
	m := (s + 30) / 60
	two := func(big int64, bigUnit string, small int64, smallUnit string) string {
		if small == 0 {
			return fmt.Sprintf("%d %s", big, bigUnit)
		}
		return fmt.Sprintf("%d %s %d %s", big, bigUnit, small, smallUnit)
	}
	switch {
	case m < 60:
		return fmt.Sprintf("%d min", m)
	case m < 24*60:
		return two(m/60, "h", m%60, "min")
	}
	h := (m + 30) / 60
	return two(h/24, "d", h%24, "h")
}
