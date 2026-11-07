package aria2

import "testing"

func TestHTTPStatus(t *testing.T) {
	cases := map[string]int{
		"The response status is not successful. status=429": 429,
		"The response status is not successful. status=403": 403,
		"timeout": 0,
		"":        0,
	}
	for in, want := range cases {
		if got := HTTPStatus(in); got != want {
			t.Errorf("HTTPStatus(%q)=%d want %d", in, got, want)
		}
	}
}

func TestErrCodeValues(t *testing.T) {
	if ErrUnknown != 1 || ErrDiskFull != 9 || ErrFileExists != 13 || ErrHTTPProtocol != 22 || ErrChecksum != 32 {
		t.Fatal("code table drifted")
	}
	if ErrDiskFull.String() == "" {
		t.Fatal("empty description")
	}
}
