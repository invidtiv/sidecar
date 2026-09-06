package shellstate

import (
	"strings"
	"time"
)

// RecordServerLoss is the shared transition used by both manifest serializers.
// observedAt is loss-observation time, never the record's CreatedAt identity.
func RecordServerLoss(def Definition, observedAt time.Time, evidence string) (Definition, bool) {
	if def.Restore != nil && !def.Restore.ServerLostAt.IsZero() {
		return def, false
	}
	next := RestoreState{}
	if def.Restore != nil {
		next = *def.Restore
	}
	if observedAt.IsZero() {
		observedAt = time.Now()
	}
	next.Eligible = true
	next.ServerLostAt = observedAt.UTC()
	next.PrefillClaimedAt = time.Time{}
	next.PrefilledAt = time.Time{}
	next.LastAgentActivity = strings.TrimSpace(evidence)

	def.Restore = &next
	return def, true
}

// DescribeLastAgentActivity formats observed activity without inventing timing.
func DescribeLastAgentActivity(kind, status string, changedAt time.Time) string {
	kind = strings.TrimSpace(kind)
	if kind == "" || kind == "shell" {
		return ""
	}
	result := "was running " + kind
	if status = strings.TrimSpace(status); status != "" && status != "unknown" {
		result += ", " + status
		if !changedAt.IsZero() {
			result += " since " + changedAt.Local().Format("15:04")
		}
	}
	return result
}
