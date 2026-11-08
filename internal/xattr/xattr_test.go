package xattr

import "testing"

func TestStripUserinfo(t *testing.T) {
	cases := map[string]string{
		"https://user:pw@example.com/a?b=1": "https://example.com/a?b=1",
		"https://tok@example.com/f":         "https://example.com/f",
		"https://example.com/f":             "https://example.com/f",
	}
	for in, want := range cases {
		got, err := stripUserinfo(in)
		if err != nil || got != want {
			t.Errorf("%s: got %q err %v want %q", in, got, err, want)
		}
	}
}
