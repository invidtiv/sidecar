package agentsession

import (
	"fmt"
	"sort"
	"strings"
)

// Decision is what a report did to a shell's stored session reference.
type Decision string

const (
	// DecisionRecorded stored a reference where there was none.
	DecisionRecorded Decision = "recorded"
	// DecisionRotated replaced a different reference from the same live pane,
	// which is what a provider does when a user starts a new conversation.
	DecisionRotated Decision = "rotated"
	// DecisionCleared removed the stored reference.
	DecisionCleared Decision = "cleared"
	// DecisionUnchanged means the report said what was already stored. Replays
	// are idempotent rather than errors: a hook that runs twice is normal.
	DecisionUnchanged Decision = "unchanged"
	// DecisionIgnored means the report lost the generation fence and nothing
	// was written.
	DecisionIgnored Decision = "ignored"
)

// Decisions lists the vocabulary in a stable order.
func Decisions() []Decision {
	return []Decision{DecisionRecorded, DecisionRotated, DecisionCleared, DecisionUnchanged, DecisionIgnored}
}

// Fence decides whether an incoming report may replace the stored reference.
//
// The rule the plan states is that a report replaces the prior reference only
// when it comes from the current pinned provider/process generation, and that
// late output from an exited or replaced process is ignored. Both halves matter
// and they are different comparisons:
//
//   - `incoming.Generation` is derived from the reporting hook's own process
//     ancestry — the provider process that is talking.
//   - `live` is derived independently from what currently occupies the pane.
//
// When those disagree, the process that is talking is not the process that is
// running, so the report is late and loses. Comparing the incoming report only
// against the *stored* generation would not catch this at all: a late report
// from a dead process still matches the record it wrote itself.
//
// A prior reference from an older generation is not an obstacle. A new provider
// process in the same pane legitimately takes the binding over, which is the
// difference between "late" and "new".
func Fence(prior *Ref, incoming Ref, live string) (Decision, error) {
	if strings.TrimSpace(live) == "" {
		return DecisionIgnored, fmt.Errorf("%w: no live provider generation was resolved for the pane", ErrStaleGeneration)
	}
	if strings.TrimSpace(incoming.Generation) == "" {
		return DecisionIgnored, fmt.Errorf("%w: the report carries no provider generation", ErrStaleGeneration)
	}
	if incoming.Generation != live {
		return DecisionIgnored, fmt.Errorf("%w: the report is from generation %s but %s occupies the pane",
			ErrStaleGeneration, incoming.Generation, live)
	}
	if prior == nil || prior.Empty() {
		return DecisionRecorded, nil
	}
	if prior.Kind == incoming.Kind && prior.Value == incoming.Value {
		return DecisionUnchanged, nil
	}
	return DecisionRotated, nil
}

// FenceClear decides whether an explicit clear may remove the stored reference.
//
// A clear is fenced exactly like a report. A provider process that has already
// been replaced must not be able to unbind the conversation its successor just
// bound, which is the same late-writer problem in the other direction.
func FenceClear(prior *Ref, generation, live string) (Decision, error) {
	if strings.TrimSpace(live) == "" {
		return DecisionIgnored, fmt.Errorf("%w: no live provider generation was resolved for the pane", ErrStaleGeneration)
	}
	if strings.TrimSpace(generation) == "" {
		return DecisionIgnored, fmt.Errorf("%w: the clear carries no provider generation", ErrStaleGeneration)
	}
	if generation != live {
		return DecisionIgnored, fmt.Errorf("%w: the clear is from generation %s but %s occupies the pane",
			ErrStaleGeneration, generation, live)
	}
	if prior == nil || prior.Empty() {
		return DecisionUnchanged, nil
	}
	return DecisionCleared, nil
}

// DedupKey is the global per-host identity of one native conversation.
//
// The provider kind is part of the key so two providers that happen to mint the
// same identifier are not reported as a conflict with each other. The separator
// cannot occur in a validated reference, so the key cannot be forged by a value
// that contains it.
func DedupKey(kind string, ref Ref) string {
	if ref.Empty() {
		return ""
	}
	return strings.Join([]string{strings.TrimSpace(kind), string(ref.Kind), ref.Value}, "\x00")
}

// Holder is one managed shell that claims a session reference.
type Holder struct {
	Project string
	Session string // tmux session name, the shell's durable identity
	Name    string // display name, for human output
	Kind    string
	Ref     Ref
}

// DedupResult is the verdict for one conversation.
type DedupResult struct {
	// Key is the dedup key these holders share.
	Key string
	// Winner is the single shell allowed to resume the conversation.
	Winner Holder
	// Conflicts are the other shells claiming it. They restore as plain shells
	// and are told which target won.
	Conflicts []Holder
}

// Dedup enforces that one exact native session reference resumes into at most
// one managed shell on this host.
//
// The most recently reported claim wins, because it is the one a provider most
// recently confirmed is live; ties break on the tmux session name so the result
// is stable rather than dependent on manifest order. Losers are not deleted or
// rewritten — deduplication decides who may *resume*, and a plan that silently
// edited shell records to enforce it would be destroying evidence.
func Dedup(holders []Holder) []DedupResult {
	groups := map[string][]Holder{}
	for _, h := range holders {
		key := DedupKey(h.Kind, h.Ref)
		if key == "" {
			continue
		}
		groups[key] = append(groups[key], h)
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := make([]DedupResult, 0, len(keys))
	for _, key := range keys {
		group := groups[key]
		sort.SliceStable(group, func(i, j int) bool {
			if !group[i].Ref.ReportedAt.Equal(group[j].Ref.ReportedAt) {
				return group[i].Ref.ReportedAt.After(group[j].Ref.ReportedAt)
			}
			return group[i].Session < group[j].Session
		})
		out = append(out, DedupResult{Key: key, Winner: group[0], Conflicts: append([]Holder(nil), group[1:]...)})
	}
	return out
}

// Conflicts returns only the results where more than one shell claims the same
// conversation.
func Conflicts(holders []Holder) []DedupResult {
	var out []DedupResult
	for _, r := range Dedup(holders) {
		if len(r.Conflicts) > 0 {
			out = append(out, r)
		}
	}
	return out
}
