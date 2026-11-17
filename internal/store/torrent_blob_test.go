package store

import (
	"bytes"
	"testing"
)

// The .torrent bytes are read only by GetJob and written only by InsertJob.
func TestTorrentBlobOnlyInGetJob(t *testing.T) {
	s := open(t)
	j := job("t", Queued)
	j.Kind, j.Torrent = "torrent", []byte("d4:infod4:name1:xee")
	if err := s.InsertJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	js, err := s.ListJobs(ctx, Filter{})
	if err != nil || len(js) != 1 {
		t.Fatalf("ListJobs = %v, %v", js, err)
	}
	if js[0].Torrent != nil {
		t.Fatal("ListJobs read the torrent blob")
	}
	if js, _ := s.Search(ctx, "t", 0, 0); len(js) != 1 || js[0].Torrent != nil {
		t.Fatalf("Search = %v; want one job without the blob", js)
	}
	got, err := s.UpdateJob(ctx, j.ID, func(x *Job) error {
		if x.Torrent != nil {
			t.Error("UpdateJob read the torrent blob")
		}
		x.Torrent = nil
		x.RetryCount = 1
		return nil
	})
	if err != nil || got.RetryCount != 1 {
		t.Fatalf("UpdateJob = %+v, %v", got, err)
	}
	full, err := s.GetJob(ctx, j.ID)
	if err != nil || !bytes.Equal(full.Torrent, j.Torrent) {
		t.Fatalf("GetJob torrent = %q, %v; want the stored blob", full.Torrent, err)
	}
}
