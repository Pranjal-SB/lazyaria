package daemon

import (
	"context"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/store"
	"github.com/Pranjal-SB/lazyaria/internal/textutil"
)

// Details is one job's full view (jobs.details).
type Details struct {
	Job      *store.Job    `json:"job"`
	Files    []DetailFile  `json:"files"`
	Servers  []string      `json:"servers"` // current URIs, credentials stripped
	Peers    int           `json:"peers"`
	Segments []int         `json:"segments"` // percent done per cell; null when unknown
	Pieces   int           `json:"pieces"`
	Events   []DetailEvent `json:"events"` // the newest 20, oldest first
}

type DetailFile struct {
	Path     string `json:"path"`
	Length   int64  `json:"length"`
	Done     int64  `json:"done"`
	Selected bool   `json:"selected"`
}

type DetailEvent struct {
	At     time.Time `json:"at"`
	Kind   string    `json:"kind"`
	Detail string    `json:"detail,omitempty"`
}

const maxDetailEvents = 20

// Segments folds a piece bitfield (hex, high bit first = piece 0) into
// cells, each the percent of its pieces that are done. With more cells
// than pieces a piece spans several cells. Bits past pieces are ignored,
// pieces past the bitfield count as not done. No pieces gives nil.
func Segments(bitfield string, pieces, cells int) []int {
	if pieces <= 0 || cells <= 0 {
		return nil
	}
	done := func(p int) bool {
		if p/4 >= len(bitfield) {
			return false
		}
		v, ok := hexVal(bitfield[p/4])
		return ok && v&(8>>(p%4)) != 0
	}
	out := make([]int, cells)
	for i := range out {
		lo, hi := i*pieces/cells, (i+1)*pieces/cells
		hi = max(hi, lo+1) // more cells than pieces: the cell shows its piece
		n := 0
		for p := lo; p < min(hi, len(bitfield)*4); p++ {
			if done(p) {
				n++
			}
		}
		out[i] = n * 100 / (hi - lo)
	}
	return out
}

func hexVal(c byte) (byte, bool) {
	switch {
	case '0' <= c && c <= '9':
		return c - '0', true
	case 'a' <= c && c <= 'f':
		return c - 'a' + 10, true
	case 'A' <= c && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// Details returns job id with its files, sources, piece map in cells
// (clamped to 1..512) and recent events. A finished job's map is full
// without asking the engine; a job the engine does not know, or one with
// no pieces yet, has none.
func (m *Manager) Details(ctx context.Context, id string, cells int) (Details, error) {
	cells = min(max(cells, 1), 512)
	j, err := m.st.GetJob(ctx, id)
	if err != nil {
		return Details{}, err
	}
	j.Torrent = nil
	d := Details{Job: m.decorate(j)}
	evs, err := m.st.Events(ctx, id)
	if err != nil {
		m.log.Warn("details: events", "job", id, "err", err)
	}
	for _, e := range evs[max(len(evs)-maxDetailEvents, 0):] {
		d.Events = append(d.Events, DetailEvent{At: e.At, Kind: e.Kind, Detail: e.Detail})
	}
	switch {
	case j.State == store.Done:
		d.Segments = make([]int, cells)
		for i := range d.Segments {
			d.Segments[i] = 100
		}
		return d, nil
	case j.EngineRef == nil:
		return d, nil
	}
	ed, err := m.e.Details(ctx, refOf(j))
	if err != nil {
		m.log.Debug("details: engine", "job", id, "err", err)
		return d, nil
	}
	for _, f := range ed.Files {
		d.Files = append(d.Files, DetailFile{Path: f.Path, Length: f.Length, Done: f.Done, Selected: f.Selected})
	}
	for _, s := range ed.Servers {
		clean, _, _ := textutil.SplitUserinfo(s.URI)
		d.Servers = append(d.Servers, clean)
	}
	d.Peers, d.Pieces = len(ed.Peers), ed.Pieces
	d.Segments = Segments(ed.Bitfield, ed.Pieces, cells)
	return d, nil
}
