package aria2

import (
	"encoding/json"
	"testing"
)

func TestInt64Decode(t *testing.T) {
	var s struct{ A, B, C Int64 }
	if err := json.Unmarshal([]byte(`{"A":"123","B":"","C":7}`), &s); err != nil {
		t.Fatal(err)
	}
	if s.A != 123 || s.B != 0 || s.C != 7 {
		t.Fatalf("%+v", s)
	}
	if err := json.Unmarshal([]byte(`{"A":"x"}`), &s); err == nil {
		t.Fatal(`want error for "x"`)
	}
}

func TestStatusDecode(t *testing.T) {
	var st Status
	in := `{"gid":"g","status":"active","totalLength":"10","downloadSpeed":"5","errorCode":"0","files":[{"index":"1","path":"/a","length":"10","completedLength":"2","selected":"true"}]}`
	if err := json.Unmarshal([]byte(in), &st); err != nil {
		t.Fatal(err)
	}
	if st.TotalLength != 10 || st.DownloadSpeed != 5 || st.Files[0].CompletedLength != 2 || st.ErrorCode != "0" {
		t.Fatalf("%+v", st)
	}
}
