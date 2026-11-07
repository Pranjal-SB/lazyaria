package aria2

import (
	"encoding/json"
	"testing"
)

func FuzzDecodeStatus(f *testing.F) {
	f.Add([]byte(`{"gid":"g","totalLength":"1","files":[{"length":"2"}],"bittorrent":{"info":{"name":"n"}}}`))
	f.Add([]byte(`{"totalLength":"x"}`))
	f.Fuzz(func(t *testing.T, b []byte) {
		var s Status
		_ = json.Unmarshal(b, &s)
	})
}
