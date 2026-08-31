package shellstate

import (
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/agentsession"
)

// Cold-restore state: the marker that says a shell was running when its tmux
// server went away, and the writer that refuses to delete a record for that
// reason.
//
// The motivating incident is concrete. When a tmux server dies, every managed
// session disappears at once, and a liveness pass that reads "this session is
// not in the listing" as "this shell is dead" tombstones every record in the
// file. That is how a user lost the sidecar and braid shell records: not to a
// bug in the writer, but to the writer being asked a question ("is this one
// shell gone?") that had no true answer at that moment, and answering it.
//
// The fix here is not another guard in front of the deletion. It is that a
// liveness pass no longer asks for a deletion at all when the shell's server has
// been replaced: it asks for a *reclassification*, and the record survives as a
// restore candidate. Deletion remains available for the case it was designed
// for — one shell exiting inside a server that is still running — because that
// is a user closing a terminal, and keeping a tombstone for it is correct.
//
// The plan states the invariant as: never let a restore failure or a server
// death delete a shell record, tombstone, pane layout, or conversation record.
// ForgetOrPreserveAtPath is where that invariant is enforced rather than
// remembered.

// ReapOutcome is what a liveness-driven write did to one record.
type ReapOutcome string

const (
	// ReapTombstoned means the record was moved to tombstones, recoverable by
	// `sidecar shell restore` for the retention window. This is the outcome for
	// a shell that exited inside a server that is still running.
	ReapTombstoned ReapOutcome = "tombstoned"
	// ReapPreserved means the record was kept and marked as a cold-restore
	// candidate, because it did not exit — its tmux server did.
	ReapPreserved ReapOutcome = "preserved"
	// ReapAbsent means there was no such record to act on.
	ReapAbsent ReapOutcome = "absent"
)

// ObserveLiveAtPath records that these shells are running under this tmux
// server, so a later cold restore can tell a shell that died with the server
// from one nobody had open.
//
// It is the marker source open question 3 settles on, and it is deliberately
// parsimonious in two ways.
//
// It writes only on a transition. A record already marked eligible under this
// same server id is left completely alone — not even its timestamp is touched —
// so the steady state is one write per shell per tmux-server lifetime rather
// than one per refresh cycle. That is the plan's rule that shell records are
// written on meaningful transitions and not on every capture, and it is the
// reason LastSeenAliveAt means "when this shell was first confirmed alive in
// this server" rather than "when it was last polled".
//
// It takes the server as a caller-resolved "pid=N" id rather than an
// Incarnation, because the pid is the part that is safe to persist: the socket
// inode and ctime that Incarnation.String() carries are rewritten by tmux
// whenever the attached-client set changes, so a marker keyed on them would
// read as a server replacement every time a user attached a client.
//
// It returns the number of records it changed, which is zero on the overwhelming
// majority of calls.
func ObserveLiveAtPath(path, serverID string, live []Identity, now time.Time) (int, error) {
	serverID = strings.TrimSpace(serverID)
	if serverID == "" || len(live) == 0 {
		// No observed server means no evidence worth recording. Writing "eligible
		// under an unknown server" would produce a marker that can never be
		// compared against anything, which is worse than no marker.
		return 0, nil
	}
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()

	changed := 0
	err := mutateManifest(path, func(m *manifest) error {
		for _, id := range live {
			if strings.TrimSpace(id.TmuxName) == "" {
				continue
			}
			for i := range m.Shells {
				if m.Shells[i].TmuxName != id.TmuxName || !sameNamespace(m.Shells[i].Namespace, id.Namespace) {
					continue
				}
				def := m.Shells[i]
				if def.Restore != nil && def.Restore.Eligible && def.Restore.LastSeenServer == serverID {
					break // already recorded under this server; touch nothing
				}
				next := RestoreState{}
				if def.Restore != nil {
					next = *def.Restore
				}
				next.Eligible = true
				next.LastSeenServer = serverID
				next.LastSeenAliveAt = now
				def.Restore = &next
				m.Shells[i] = def
				changed++
				break
			}
		}
		if changed == 0 {
			return errRestoreUnchanged{}
		}
		return nil
	})
	if _, ok := err.(errRestoreUnchanged); ok {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return changed, nil
}

// ForgetOrPreserveAtPath is the liveness path's write, and the whole point of it
// is the case where it declines to delete.
//
// currentServer is the tmux server observed at the moment of the write, as a
// "pid=N" id, or empty when no server is running at all. Two conditions preserve
// the record instead of tombstoning it:
//
//   - No tmux server is running. A shell cannot be shown to have exited by a
//     server that is not there to be asked. This is the exact shape of the
//     incident: the listing is empty for every shell simultaneously, and the
//     honest reading is "the server is gone", not "they all died".
//   - The record was last confirmed alive under a different server. The shell
//     did not exit; it was destroyed along with the process that was hosting it,
//     which is precisely what a cold restore exists to undo.
//
// In both cases the record stays in the manifest and is marked eligible, so it
// appears in the restore plan rather than in the tombstone list. Only a shell
// that vanished inside a server that is still running is tombstoned, because
// that is a terminal someone closed.
//
// The CreatedAt fence from RemoveIfUnchangedAtPath is preserved for the
// tombstone branch: a record rewritten since it was observed is a different
// shell wearing a reused name.
func ForgetOrPreserveAtPath(path string, id Identity, observedAt time.Time, currentServer string) (ReapOutcome, error) {
	if strings.TrimSpace(id.TmuxName) == "" {
		return ReapAbsent, &Error{Kind: KindValidation, Msg: "shell session name is required"}
	}
	currentServer = strings.TrimSpace(currentServer)

	outcome := ReapAbsent
	err := mutateManifestLive(path, true, func(m *manifest) error {
		idx := -1
		for i := range m.Shells {
			if m.Shells[i].TmuxName == id.TmuxName && sameNamespace(m.Shells[i].Namespace, id.Namespace) {
				idx = i
				break
			}
		}
		if idx < 0 {
			outcome = ReapAbsent
			return errRestoreUnchanged{}
		}
		def := m.Shells[idx]

		lastSeen := ""
		if def.Restore != nil {
			lastSeen = def.Restore.LastSeenServer
		}
		diedWithServer := currentServer == "" || (lastSeen != "" && lastSeen != currentServer)
		if diedWithServer {
			outcome = ReapPreserved
			if def.Restore != nil && def.Restore.Eligible {
				return errRestoreUnchanged{} // already a candidate; no write
			}
			next := RestoreState{}
			if def.Restore != nil {
				next = *def.Restore
			}
			next.Eligible = true
			def.Restore = &next
			m.Shells[idx] = def
			return nil
		}

		if !observedAt.IsZero() && def.CreatedAt.After(observedAt) {
			return ErrShellChanged
		}
		outcome = ReapTombstoned
		m.Tombstones = appendTombstone(m.Tombstones, def, time.Now().UTC())
		m.Shells = append(m.Shells[:idx], m.Shells[idx+1:]...)
		return nil
	})
	if _, ok := err.(errRestoreUnchanged); ok {
		return outcome, nil
	}
	if err != nil {
		return ReapAbsent, err
	}
	return outcome, nil
}

// SetRestorePolicyAtPath sets one shell's per-shell restore policy.
//
// PolicyInherit clears the stored value rather than storing the word "inherit",
// so a record that follows the machine default serializes no policy at all and a
// v2-shaped record stays byte-identical.
func SetRestorePolicyAtPath(path string, id Identity, policy agentsession.Policy) (agentsession.Policy, error) {
	if strings.TrimSpace(id.TmuxName) == "" {
		return "", &Error{Kind: KindValidation, Msg: "shell session name is required"}
	}
	if policy == "" {
		policy = agentsession.PolicyInherit
	}
	effective := policy
	err := mutateManifest(path, func(m *manifest) error {
		idx := -1
		for i := range m.Shells {
			if m.Shells[i].TmuxName == id.TmuxName && sameNamespace(m.Shells[i].Namespace, id.Namespace) {
				idx = i
				break
			}
		}
		if idx < 0 {
			return &Error{Kind: KindNotFound, Msg: "no managed shell named " + id.TmuxName + " is recorded in this project"}
		}
		def := m.Shells[idx]
		current := agentsession.PolicyInherit
		if def.Restore != nil && def.Restore.Policy != "" {
			current = def.Restore.Policy
		}
		if current == policy {
			return errRestoreUnchanged{}
		}
		next := RestoreState{}
		if def.Restore != nil {
			next = *def.Restore
		}
		if policy == agentsession.PolicyInherit {
			next.Policy = ""
		} else {
			next.Policy = policy
		}
		if next == (RestoreState{}) {
			def.Restore = nil
		} else {
			def.Restore = &next
		}
		m.Shells[idx] = def
		return nil
	})
	if _, ok := err.(errRestoreUnchanged); ok {
		return effective, nil
	}
	if err != nil {
		return "", err
	}
	return effective, nil
}

// RestorePolicyOf reports a record's effective per-shell policy.
func RestorePolicyOf(def Definition) agentsession.Policy {
	if def.Restore == nil || def.Restore.Policy == "" {
		return agentsession.PolicyInherit
	}
	return def.Restore.Policy
}

// errRestoreUnchanged aborts a mutation without writing and without surfacing an
// error, the same way errSessionUnchanged does for session binding: mutateManifest
// writes whenever apply returns nil, so "nothing to do" has to be an error or
// every no-op observation would rewrite shells.json.
type errRestoreUnchanged struct{}

func (errRestoreUnchanged) Error() string { return "restore state unchanged" }
