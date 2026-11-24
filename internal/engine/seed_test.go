package engine

import (
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// startTracker is a minimal HTTP BitTorrent tracker on 127.0.0.1: it
// remembers every peer that announced and hands each the others.
func startTracker(t *testing.T) string {
	t.Helper()
	var mu sync.Mutex
	peers := map[string][]byte{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		var port uint16
		fmt.Sscan(q.Get("port"), &port)
		b := binary.BigEndian.AppendUint16(net.ParseIP(host).To4(), port)
		mu.Lock()
		peers[q.Get("peer_id")] = b
		var all []byte
		for id, p := range peers {
			if id != q.Get("peer_id") {
				all = append(all, p...)
			}
		}
		mu.Unlock()
		fmt.Fprintf(w, "d8:intervali2e5:peers%d:%se", len(all), all)
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/announce"
}

// makeTorrent is a single-file .torrent for b named name.
func makeTorrent(announce, name string, b []byte) []byte {
	const piece = 256 << 10
	var pieces []byte
	for i := 0; i < len(b); i += piece {
		h := sha1.Sum(b[i:min(i+piece, len(b))])
		pieces = append(pieces, h[:]...)
	}
	info := fmt.Sprintf("d6:lengthi%de4:name%d:%s12:piece lengthi%de6:pieces%d:%se", len(b), len(name), name, piece, len(pieces), pieces)
	return fmt.Appendf(nil, "d8:announce%d:%s4:info%se", len(announce), announce, info)
}

// A finished torrent whose files were moved out from under aria2 is added
// again with SeedFrom: aria2 seeds the files where they are, at once with
// "unverified" and after a hash check with "check", and a fresh peer gets
// every byte. Without SeedFrom aria2 refuses it (no control file).
func TestSeedFromResumesSeeding(t *testing.T) {
	seeder, dir := startEngine(t)
	tor := makeTorrent(startTracker(t), "seed.bin", data)
	if err := os.WriteFile(filepath.Join(dir, "seed.bin"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	opts := map[string]string{"seed-time": "5"}

	ref := add(t, seeder, AddRequest{Torrent: tor, Dir: dir, Options: opts})
	st := waitStatus(t, seeder, ref, 10*time.Second, func(s EngineStatus) bool { return s.State != "active" && s.State != "waiting" })
	if st.State != "error" {
		t.Fatalf("without SeedFrom: %+v; want aria2 to refuse the existing file", st)
	}
	if err := seeder.Remove(ctxT(t, 5*time.Second), ref); err != nil {
		t.Fatal(err)
	}

	for _, from := range []string{"check", "unverified"} {
		start := time.Now()
		ref = add(t, seeder, AddRequest{Torrent: tor, Dir: dir, Options: opts, SeedFrom: from})
		st = waitStatus(t, seeder, ref, 20*time.Second, func(s EngineStatus) bool { return s.Seeder || s.State == "error" })
		if !st.Seeder || st.State != "active" || st.Done != fileSize {
			t.Fatalf("SeedFrom %q: %+v; want seeding the whole file", from, st)
		}
		t.Logf("SeedFrom %q: seeding after %v", from, time.Since(start).Round(time.Millisecond))
		if from == "check" {
			if err := seeder.Remove(ctxT(t, 5*time.Second), ref); err != nil {
				t.Fatal(err)
			}
		}
	}
	checkSHA(t, filepath.Join(dir, "seed.bin"))

	leecher, dir2 := startEngine(t)
	lref := add(t, leecher, AddRequest{Torrent: tor, Dir: dir2})
	waitStatus(t, leecher, lref, 60*time.Second, func(s EngineStatus) bool { return s.State == "complete" || s.State == "error" })
	checkSHA(t, filepath.Join(dir2, "seed.bin")) // the seeder is its only peer
}
