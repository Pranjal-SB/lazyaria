package cli

import (
	"strings"
	"testing"
)

func FuzzParseJobRef(f *testing.F) {
	for _, s := range []string{"14", "#14", "#", "-3", "01KA", "٣", "", "0", "#0", "abc", "9999999999999999999999"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		num, prefix, err := parseJobRef(s)
		if err != nil {
			return
		}
		if (num > 0) == (prefix != "") {
			t.Fatalf("%q -> num %d, prefix %q: want exactly one", s, num, prefix)
		}
		if prefix != "" && strings.Trim(s, "0123456789") == "" {
			t.Fatalf("all-digit %q became the prefix %q", s, prefix)
		}
	})
}
