package lifecycleharness

import (
	"fmt"

	"github.com/marcus/sidecar/internal/agentactivity"
	"github.com/marcus/sidecar/internal/agentlifecycle"
)

// Recorder is an in-memory [Sink] that applies the ordering and identity rules
// the persistent store will own.
//
// It is deliberately not a stand-in for that store: it has no locking, no file
// format, no compaction, and no crash behavior. What it does have is the exact
// acceptance semantics the harness scenarios need to be meaningful before the
// store exists, so Phase B can swap in the real implementation and expect every
// scenario to keep passing.
type Recorder struct {
	latest   map[agentlifecycle.Key]agentlifecycle.Report
	all      []agentlifecycle.Report
	rejected []error
}

func NewRecorder() *Recorder {
	return &Recorder{latest: map[agentlifecycle.Key]agentlifecycle.Report{}}
}

// Append records a report if it is valid and advances its sequence key.
func (r *Recorder) Append(rep agentlifecycle.Report) (agentlifecycle.Acceptance, error) {
	if err := validate(rep); err != nil {
		r.rejected = append(r.rejected, err)
		return "", err
	}
	key := rep.Key()
	if prev, ok := r.latest[key]; ok {
		if rep.Sequence == prev.Sequence && rep.ID == prev.ID {
			// Replay is idempotent. This is the case a provider with at-least-once
			// delivery produces constantly, and treating it as an error would make
			// a healthy integration look broken.
			return agentlifecycle.AcceptedDuplicate, nil
		}
		if rep.Sequence <= prev.Sequence {
			err := fmt.Errorf("%s: sequence %d does not advance past %d",
				agentlifecycle.ErrStaleSequence, rep.Sequence, prev.Sequence)
			r.rejected = append(r.rejected, err)
			return "", err
		}
	}
	r.latest[key] = rep
	r.all = append(r.all, rep)
	return agentlifecycle.AcceptedAuthoritative, nil
}

// Latest returns the current record for a key.
func (r *Recorder) Latest(key agentlifecycle.Key) (agentlifecycle.Report, bool) {
	rep, ok := r.latest[key]
	return rep, ok
}

// All returns every accepted report in arrival order.
func (r *Recorder) All() []agentlifecycle.Report { return r.all }

// Rejected returns the errors from reports that were not accepted.
func (r *Recorder) Rejected() []error { return r.rejected }

// validate enforces the bounds and enum rules a hook surface must apply before
// anything is persisted. Provider input is untrusted local data.
func validate(rep agentlifecycle.Report) error {
	if rep.SchemaVersion != agentlifecycle.SchemaVersion {
		return fmt.Errorf("%s: schema version %d", agentlifecycle.ErrInvalidReport, rep.SchemaVersion)
	}
	if rep.Source == "" || rep.Identity.PaneID == "" || rep.Identity.RunID == "" {
		return fmt.Errorf("%s: incomplete identity", agentlifecycle.ErrInvalidContext)
	}
	if !validKind(rep.Kind) {
		return fmt.Errorf("%s: kind %q", agentlifecycle.ErrInvalidReport, rep.Kind)
	}
	switch rep.Kind {
	case agentlifecycle.KindState:
		if !validReportState(rep.State) {
			return fmt.Errorf("%s: state %q", agentlifecycle.ErrInvalidReport, rep.State)
		}
		if rep.Outcome != "" {
			return fmt.Errorf("%s: state report carries an outcome", agentlifecycle.ErrInvalidReport)
		}
	case agentlifecycle.KindEnd:
		if !validOutcome(rep.Outcome) {
			return fmt.Errorf("%s: outcome %q", agentlifecycle.ErrInvalidReport, rep.Outcome)
		}
		if rep.State != "" {
			return fmt.Errorf("%s: end report carries a lane", agentlifecycle.ErrInvalidReport)
		}
	default:
		if rep.State != "" || rep.Outcome != "" {
			return fmt.Errorf("%s: %s report carries a lane or outcome", agentlifecycle.ErrInvalidReport, rep.Kind)
		}
	}
	if rep.Reason != "" && !validReason(rep.Reason) {
		return fmt.Errorf("%s: reason %q is not in the allowlist", agentlifecycle.ErrInvalidReport, rep.Reason)
	}
	// The only free-form field is bounded, so a provider cannot smuggle a
	// payload through it.
	if len(rep.Detail) > 200 {
		return fmt.Errorf("%s: detail exceeds 200 bytes", agentlifecycle.ErrInvalidReport)
	}
	return nil
}

func validKind(k agentlifecycle.Kind) bool {
	for _, v := range agentlifecycle.Kinds() {
		if v == k {
			return true
		}
	}
	return false
}

func validOutcome(o agentlifecycle.Outcome) bool {
	for _, v := range agentlifecycle.Outcomes() {
		if v == o {
			return true
		}
	}
	return false
}

func validReason(r agentlifecycle.ReasonCode) bool {
	for _, v := range agentlifecycle.Reasons() {
		if v == r {
			return true
		}
	}
	return false
}

func validReportState(s agentactivity.State) bool {
	for _, v := range agentlifecycle.ReportStates() {
		if v == s {
			return true
		}
	}
	return false
}
