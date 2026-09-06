package agentsession

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/adapter"
	"github.com/marcus/sidecar/internal/adapter/antigravity"
	"github.com/marcus/sidecar/internal/adapter/claudecode"
	"github.com/marcus/sidecar/internal/adapter/grok"
	"github.com/marcus/sidecar/internal/adapter/opencode"
)

// CandidateConfidence describes how strongly provider-store evidence identifies
// one conversation. It is deliberately separate from Ref.Reported: discovery
// can suggest text for a human to submit, but can never authorize auto-resume.
type CandidateConfidence string

const (
	CandidateLikely    CandidateConfidence = "likely"
	CandidateAmbiguous CandidateConfidence = "ambiguous"
)

// Candidate is a conversation recovered from a provider's own store.
// Picker is true when equally near sessions remain and the provider's picker
// should be typed instead of a session-specific command. Ref.Value is empty in
// that case; Reason names every conversation still in contention.
type Candidate struct {
	// AgentKind records the provider used to discover this candidate when the
	// shell itself has no authoritative provider binding (legacy layouts).
	AgentKind   string              `json:"agentKind,omitempty"`
	Ref         Ref                 `json:"ref"`
	Title       string              `json:"title,omitempty"`
	LastWriteAt time.Time           `json:"lastWriteAt,omitzero"`
	Confidence  CandidateConfidence `json:"confidence"`
	Reason      string              `json:"reason"`
	Picker      bool                `json:"picker,omitempty"`
}

// CandidateQuery is the shell lifetime and provider scope used for discovery.
// Claimed references let a coordinator disambiguate several shells in one
// directory without assigning the same native conversation twice.
type CandidateQuery struct {
	WorkDir      string
	AgentKind    string
	CreatedAt    time.Time
	ServerLostAt time.Time
	Claimed      []Ref
}

// SessionReader is the small adapter seam candidate discovery needs.
type SessionReader interface {
	Sessions(projectRoot string) ([]adapter.Session, error)
}

type recoverySessionReader interface {
	RecoverySessions(workDir string) ([]adapter.Session, error)
}

// CandidateFinder reads existing provider adapters; it has no tmux or UI
// dependency and does not mutate provider stores.
type CandidateFinder struct {
	readers map[string]SessionReader
}

// NewCandidateFinder constructs the production finder for providers whose
// current adapters expose reliable-enough directory and time evidence.
func NewCandidateFinder() *CandidateFinder {
	return NewCandidateFinderWithReaders(map[string]SessionReader{
		"claude":      claudecode.New(),
		"grok":        grok.New(),
		"antigravity": antigravity.New(),
		"opencode":    opencode.New(),
	})
}

// NewCandidateFinderWithReaders constructs a finder around adapter-compatible
// readers. It is exported so restore coordination and focused fixtures can use
// the same ranking rules without provider-specific filesystem setup.
func NewCandidateFinderWithReaders(readers map[string]SessionReader) *CandidateFinder {
	copyReaders := make(map[string]SessionReader, len(readers))
	for kind, reader := range readers {
		copyReaders[canonicalCandidateKind(kind)] = reader
	}
	return &CandidateFinder{readers: copyReaders}
}

// Find returns the nearest unclaimed session inside the shell's alive window.
// Several distinct write times remain useful evidence: the nearest one wins and
// the reason names the alternatives. Only a tie at the nearest instant falls
// back to a provider picker.
func (f *CandidateFinder) Find(query CandidateQuery) (Candidate, bool, error) {
	if f == nil {
		return Candidate{}, false, nil
	}
	kind := canonicalCandidateKind(query.AgentKind)
	reader := f.readers[kind]
	if reader == nil || strings.TrimSpace(query.WorkDir) == "" {
		return Candidate{}, false, nil
	}
	if query.CreatedAt.IsZero() || query.ServerLostAt.IsZero() || query.ServerLostAt.Before(query.CreatedAt) {
		return Candidate{}, false, nil
	}
	var sessions []adapter.Session
	var err error
	if recoveryReader, ok := reader.(recoverySessionReader); ok {
		sessions, err = recoveryReader.RecoverySessions(query.WorkDir)
	} else {
		sessions, err = reader.Sessions(query.WorkDir)
	}
	if err != nil {
		return Candidate{}, false, fmt.Errorf("read %s conversation candidates: %w", kind, err)
	}
	claimed := make(map[string]bool, len(query.Claimed))
	for _, ref := range query.Claimed {
		if ref.Value != "" {
			claimed[string(ref.Kind)+"\x00"+ref.Value] = true
		}
	}
	eligible := make([]adapter.Session, 0, len(sessions))
	for _, session := range sessions {
		if session.ID == "" || session.IsSubAgent || session.UpdatedAt.IsZero() || session.UpdatedAt.Before(query.CreatedAt) || session.UpdatedAt.After(query.ServerLostAt) {
			continue
		}
		if claimed[string(RefID)+"\x00"+session.ID] {
			continue
		}
		eligible = append(eligible, session)
	}
	if len(eligible) == 0 {
		return Candidate{}, false, nil
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		di := query.ServerLostAt.Sub(eligible[i].UpdatedAt)
		dj := query.ServerLostAt.Sub(eligible[j].UpdatedAt)
		if di == dj {
			return eligible[i].ID < eligible[j].ID
		}
		return di < dj
	})

	nearestAt := eligible[0].UpdatedAt
	tied := 1
	for tied < len(eligible) && eligible[tied].UpdatedAt.Equal(nearestAt) {
		tied++
	}
	if tied > 1 {
		reason := fmt.Sprintf("equally near conversations are in contention: %s", describeCandidates(eligible[:tied]))
		if tied < len(eligible) {
			reason += "; alternatives considered: " + describeCandidates(eligible[tied:])
		}
		return Candidate{
			Ref:   Ref{Kind: RefID, Source: "sidecar.candidate." + kind},
			Title: strings.Join(candidateTitles(eligible[:tied]), " / "), LastWriteAt: nearestAt,
			Confidence: CandidateAmbiguous, Picker: true,
			Reason: reason,
		}, true, nil
	}

	selected := eligible[0]
	reason := fmt.Sprintf("nearest conversation to server loss is %s", describeCandidate(selected))
	if len(eligible) > 1 {
		reason += "; alternatives considered: " + describeCandidates(eligible[1:])
	}
	if kind == "opencode" {
		reason += "; OpenCode time_updated is last-message time and may predate the server loss"
	}
	return Candidate{
		Ref:   Ref{Kind: RefID, Value: selected.ID, Source: "sidecar.candidate." + kind, Reported: false},
		Title: selected.Name, LastWriteAt: selected.UpdatedAt,
		Confidence: CandidateLikely, Reason: reason,
	}, true, nil
}

func canonicalCandidateKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "claude-code":
		return "claude"
	default:
		return strings.ToLower(strings.TrimSpace(kind))
	}
}

func candidateTitles(sessions []adapter.Session) []string {
	titles := make([]string, 0, len(sessions))
	for _, session := range sessions {
		if session.Name != "" {
			titles = append(titles, session.Name)
		} else {
			titles = append(titles, session.ID)
		}
	}
	return titles
}

func describeCandidate(session adapter.Session) string {
	if session.Name == "" || session.Name == session.ID {
		return fmt.Sprintf("%s at %s", session.ID, session.UpdatedAt.Format(time.RFC3339Nano))
	}
	return fmt.Sprintf("%s (%q) at %s", session.ID, session.Name, session.UpdatedAt.Format(time.RFC3339Nano))
}

func describeCandidates(sessions []adapter.Session) string {
	parts := make([]string, 0, len(sessions))
	for _, session := range sessions {
		parts = append(parts, describeCandidate(session))
	}
	return strings.Join(parts, ", ")
}
