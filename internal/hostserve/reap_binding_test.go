package hostserve

import (
	"sync"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/shellliveness"
	"github.com/marcus/sidecar/internal/tmuxserver"
	"github.com/marcus/sidecar/internal/workspaceops"
)

// The headless serve loop's binding to the shell writer.
//
// This loop reaps a remote machine's dead shells for a viewer that is not
// sitting at it, and it defaulted its server identity to tmuxserver.Socket — a
// socket stat, Present with pid 0. The writer could not identify that server and
// read every call as "the server died", so this loop never tombstoned anything
// either: a shell the operator closed was preserved, marked as a cold-restore
// candidate, and recreated after the next tmux restart.
//
// The fix takes the server pid off the pane listing the loop already runs, which
// is the same #{pid} ride-along the interactive surfaces use. That listing is
// the evidence the reap decision is built from, so it is also the right
// authority for "which server is this" — a separately-timed observation could
// name a different server than the one the panes came from.

// serverRecorder captures the tmux identity that reaches the writer.
type serverRecorder struct {
	mu      sync.Mutex
	servers []tmuxserver.Incarnation
	verdict shellliveness.Verdict
}

func (r *serverRecorder) probe(string) shellliveness.Verdict { return r.verdict }

func (r *serverRecorder) forget(_, _, _ string, _ time.Time, server tmuxserver.Incarnation) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.servers = append(r.servers, server)
	return nil
}

func (r *serverRecorder) seen() []tmuxserver.Incarnation {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]tmuxserver.Incarnation(nil), r.servers...)
}

// paneLineWithServer is a listing line carrying both the pane pid and, in the
// eighth field, the tmux server pid — the shape `tmux list-panes -a` now returns.
const paneLineWithServer = "%9\tunrelated-session\t/tmp\tbash\t\t0\t4243\t7777\n"

// TestServeReapWriterReceivesAnIdentifiedServer is the regression test for the
// blocker on this surface: whatever server identity this loop resolves must
// arrive at the writer intact, all the way through the probe and the fence.
func TestServeReapWriterReceivesAnIdentifiedServer(t *testing.T) {
	recorder := &serverRecorder{verdict: shellliveness.Gone}
	reapRunWith(t, nil, func(f *fakeRunner) { f.panes = paneLineWithServer }, func(o *Options) {
		o.ProbeShell = recorder.probe
		o.ForgetShell = recorder.forget
		o.ServerIncarnation = func() tmuxserver.Incarnation { return tmuxserver.Present(11, 22, 7777) }
	})

	seen := recorder.seen()
	if len(seen) == 0 {
		t.Fatal("the reap path never reached the writer")
	}
	state := workspaceops.ServerStateOf(seen[0])
	if !state.Known() {
		t.Fatal("the writer was handed an unidentifiable server; it cannot tell a closed shell from a dead server")
	}
	if !state.Running() {
		t.Fatalf("a live tmux server reached the writer as not running: %+v", seen[0])
	}
	if want := "pid=7777"; state.ID() != want {
		t.Fatalf("server id %q, want %q", state.ID(), want)
	}
}

// TestServeDefaultServerObservationIsAnswerable is the regression guard for the
// default itself, and it is the half that matters: the plumbing above carries
// whatever it is given, so the bug lived entirely in what the default gave it.
//
// tmuxserver.Socket cannot produce a Known state — it is Present-with-pid-0 when
// the socket file exists and Unknown when it does not, and neither identifies a
// server. The default must answer the question one way or the other.
func TestServeDefaultServerObservationIsAnswerable(t *testing.T) {
	opts := Options{}.withDefaults()
	if opts.ServerIncarnation == nil {
		t.Fatal("no default server observation")
	}
	if state := workspaceops.ServerStateOf(opts.ServerIncarnation()); !state.Known() {
		t.Fatalf("the default server observation is unanswerable (%+v); the writer cannot tell a "+
			"closed shell from a dead server, so it will never tombstone anything", opts.ServerIncarnation())
	}
}
