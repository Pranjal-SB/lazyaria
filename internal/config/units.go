package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParseSize reads "0", "512K", "4M", "1.5G". Units are IEC (1K = 1024).
func ParseSize(s string) (int64, error) {
	t := strings.ToUpper(strings.TrimSpace(s))
	mult := float64(1)
	if t != "" {
		if i := strings.IndexByte("KMGT", t[len(t)-1]); i >= 0 {
			mult = float64(int64(1) << (10 * (i + 1)))
			t = t[:len(t)-1]
		}
	}
	n, err := strconv.ParseFloat(t, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid size %q (use a number with optional K, M, G or T, e.g. 4M)", s)
	}
	return int64(n * mult), nil
}

// ParseDuration reads Go duration strings such as "30s" or "5m".
func ParseDuration(s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("invalid duration %q (use e.g. 30s, 5m)", s)
	}
	return d, nil
}
