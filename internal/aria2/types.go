package aria2

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
)

// Int64 decodes aria2's numbers, which arrive as JSON strings. An empty
// string or null is 0; a bare JSON number is accepted too.
type Int64 int64

func (n *Int64) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if string(b) == "null" {
		*n = 0
		return nil
	}
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		if s == "" {
			*n = 0
			return nil
		}
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return fmt.Errorf("aria2: bad number %q", s)
		}
		*n = Int64(v)
		return nil
	}
	var v int64
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*n = Int64(v)
	return nil
}

// Status is the subset of tellStatus keys lazyaria reads. Request it with StatusKeys.
type Status struct {
	GID             string   `json:"gid"`
	Status          string   `json:"status"`
	TotalLength     Int64    `json:"totalLength"`
	CompletedLength Int64    `json:"completedLength"`
	UploadLength    Int64    `json:"uploadLength"`
	DownloadSpeed   Int64    `json:"downloadSpeed"`
	UploadSpeed     Int64    `json:"uploadSpeed"`
	Connections     Int64    `json:"connections"`
	NumSeeders      Int64    `json:"numSeeders"`
	Seeder          string   `json:"seeder"` // "true" while a torrent seeds
	ErrorCode       string   `json:"errorCode"`
	ErrorMessage    string   `json:"errorMessage"`
	Dir             string   `json:"dir"`
	Files           []File   `json:"files"`
	FollowedBy      []string `json:"followedBy"`
	Following       string   `json:"following"`
	BelongsTo       string   `json:"belongsTo"`
	Bittorrent      *BTInfo  `json:"bittorrent"`
	Bitfield        string   `json:"bitfield"` // hex, high bit first = piece 0
	NumPieces       Int64    `json:"numPieces"`
}

// StatusKeys keeps tellStatus light: no bitfield, and files only on demand.
var StatusKeys = []string{"gid", "status", "totalLength", "completedLength", "uploadLength", "downloadSpeed", "uploadSpeed", "connections", "errorCode", "errorMessage", "dir", "followedBy", "following", "belongsTo", "numSeeders", "seeder", "bittorrent"}

type File struct {
	Index           Int64  `json:"index"`
	Path            string `json:"path"`
	Length          Int64  `json:"length"`
	CompletedLength Int64  `json:"completedLength"`
	Selected        string `json:"selected"`
	URIs            []URI  `json:"uris"`
}

type URI struct {
	URI    string `json:"uri"`
	Status string `json:"status"`
}

type BTInfo struct {
	Mode         string     `json:"mode"`
	Comment      string     `json:"comment"`
	CreationDate Int64      `json:"creationDate"`
	AnnounceList [][]string `json:"announceList"`
	Info         struct {
		Name string `json:"name"`
	} `json:"info"`
}

type Server struct {
	URI           string `json:"uri"`
	CurrentURI    string `json:"currentUri"`
	DownloadSpeed Int64  `json:"downloadSpeed"`
}

type Peer struct {
	PeerID        string `json:"peerId"`
	IP            string `json:"ip"`
	Port          Int64  `json:"port"`
	DownloadSpeed Int64  `json:"downloadSpeed"`
	UploadSpeed   Int64  `json:"uploadSpeed"`
	Seeder        string `json:"seeder"`
}

// DisplayName is the torrent name, else the first file's path.
func (s *Status) DisplayName() string {
	if s.Bittorrent != nil && s.Bittorrent.Info.Name != "" {
		return s.Bittorrent.Info.Name
	}
	if len(s.Files) > 0 {
		return s.Files[0].Path
	}
	return ""
}
