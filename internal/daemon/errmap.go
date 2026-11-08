package daemon

import (
	"context"
	"fmt"
	"slices"

	"github.com/Pranjal-SB/lazyaria/internal/engine"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

// noURI is what aria2 says when every source failed without an HTTP reply,
// e.g. a refused connection, or a 403 seen on a resumed download.
const noURI = "No URI available."

// failure is what an aria2 error means for a job (spec §4.6, §19.3).
type failure struct {
	kind   string // error_kind shown to the user
	retry  string // retry.auto_retry entry that covers it; "" = manual only
	ignore bool
}

// classify maps an aria2 error. status is the HTTP status if one is known,
// fresh means the download never received a byte.
func classify(code, status int, msg string, fresh bool) failure {
	switch code {
	case 7:
		return failure{ignore: true} // unfinished at shutdown; Reconcile resumes it
	case 1, 22:
		switch {
		case code == 1 && status == 0 && msg == noURI && fresh:
			return failure{kind: "network", retry: "network"}
		case status == 429:
			return failure{kind: "rate-limited", retry: "429"}
		case status == 403 || status == 410:
			return failure{kind: "link-expired"}
		case status == 503:
			return failure{kind: "http-503", retry: "503"}
		case status != 0:
			return failure{kind: fmt.Sprintf("http-%d", status)}
		case code == 22 || msg == noURI:
			return failure{kind: "http-unknown"} // refresh is offered for it
		}
	case 3, 4:
		return failure{kind: "not-found"}
	case 24:
		return failure{kind: "auth"}
	case 29:
		return failure{kind: "server-busy", retry: "503"}
	case 9:
		return failure{kind: "disk-full"}
	case 11, 13:
		return failure{kind: "exists"}
	case 2:
		return failure{kind: "network", retry: "timeout"}
	case 6, 19:
		return failure{kind: "network", retry: "network"}
	}
	return failure{kind: fmt.Sprintf("aria2-%d", code)}
}

func (m *Manager) failed(ctx context.Context, j *store.Job, st engine.EngineStatus) *store.Job {
	fresh := st.Done == 0 && (j.DoneBytes == nil || *j.DoneBytes == 0)
	status := st.HTTPStatus
	if status == 0 && (st.ErrorCode == 22 || (st.ErrorCode == 1 && !fresh)) {
		// After a resume aria2 may hide the status; the details can still carry it.
		if d, err := m.e.Details(ctx, st.Ref); err == nil {
			status = d.Status.HTTPStatus
		}
	}
	f := classify(st.ErrorCode, status, st.ErrorMsg, fresh)
	if f.ignore {
		return j
	}
	if f.retry != "" && j.State != store.Paused && slices.Contains(m.s.Retry.AutoRetry, f.retry) {
		return m.retryLater(ctx, j, st, f.kind)
	}
	j = m.to(ctx, j, store.Error, f.kind, func(x *store.Job) { setErr(x, f.kind, st) })
	if f.kind == "disk-full" {
		m.diskFull(ctx, j.Dir)
	}
	return j
}

func setErr(x *store.Job, kind string, st engine.EngineStatus) {
	code, msg := st.ErrorCode, st.ErrorMsg
	x.ErrorKind, x.ErrorCode, x.ErrorMsg = &kind, &code, &msg
	setBytes(x, st)
}

// diskFull pauses every running download into dir; they would all fail
// the same way.
func (m *Manager) diskFull(ctx context.Context, dir string) {
	js, err := m.st.ListJobs(ctx, store.Filter{States: []store.State{store.Queued, store.Active}})
	if err != nil {
		m.log.Warn("list jobs after disk full", "err", err)
	}
	for _, j := range js {
		if j.Dir != dir || j.EngineRef == nil {
			continue
		}
		if err := m.e.Pause(ctx, refOf(j)); err != nil {
			m.log.Warn("pause after disk full", "job", j.ID, "err", err)
			continue
		}
		m.to(ctx, j, store.Paused, "disk-full", nil)
	}
	m.banner("disk_full", map[string]any{"dir": dir})
}
