package xattr

import "testing"

// Userinfo, query and fragment can carry passwords and signed tokens; a
// magnet keeps only its info-hash (its trackers may hold a passkey).
func TestCleanURL(t *testing.T) {
	cases := map[string]string{
		"https://user:pw@example.com/a?b=1":  "https://example.com/a",
		"https://tok@example.com/f":          "https://example.com/f",
		"https://example.com/f":              "https://example.com/f",
		"https://example.com/f?X-Sig=abc#fr": "https://example.com/f",
		"magnet:?xt=urn:btih:0123456789ABCDEF0123456789ABCDEF01234567&tr=https://t/announce?passkey=s": "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567",
	}
	for in, want := range cases {
		got, err := cleanURL(in)
		if err != nil || got != want {
			t.Errorf("%s: got %q err %v want %q", in, got, err, want)
		}
	}
}
