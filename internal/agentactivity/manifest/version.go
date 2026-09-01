package manifest

import (
	"fmt"
	"strconv"
	"strings"
)

// ValidateVersion checks a manifest version string the way Herdr's
// ManifestVersion::parse does: non-empty, dotted, with every segment a
// non-empty run of ASCII digits that fits in a u64. It returns the trimmed
// version on success.
func ValidateVersion(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("version must not be empty")
	}
	for _, segment := range strings.Split(trimmed, ".") {
		if segment == "" {
			return "", fmt.Errorf("version %q contains an empty segment", trimmed)
		}
		for i := 0; i < len(segment); i++ {
			if segment[i] < '0' || segment[i] > '9' {
				return "", fmt.Errorf("version %q must be dotted numeric", trimmed)
			}
		}
		if _, err := strconv.ParseUint(segment, 10, 64); err != nil {
			return "", fmt.Errorf("version %q contains an oversized segment", trimmed)
		}
	}
	return trimmed, nil
}

// CompareVersions orders two dotted-numeric manifest versions segment by
// segment, returning -1, 0, or 1. It ports Herdr's Ord for ManifestVersion:
// segments compare as u64, a missing segment counts as zero, and a segment
// that does not parse counts as zero rather than failing. "1.2" and "1.2.0"
// are therefore equal, and "1.10" is greater than "1.9".
func CompareVersions(left, right string) int {
	leftParts := strings.Split(strings.TrimSpace(left), ".")
	rightParts := strings.Split(strings.TrimSpace(right), ".")
	width := max(len(leftParts), len(rightParts))
	for i := 0; i < width; i++ {
		l := versionSegment(leftParts, i)
		r := versionSegment(rightParts, i)
		switch {
		case l < r:
			return -1
		case l > r:
			return 1
		}
	}
	return 0
}

func versionSegment(parts []string, i int) uint64 {
	if i >= len(parts) {
		return 0
	}
	value, err := strconv.ParseUint(parts[i], 10, 64)
	if err != nil {
		return 0
	}
	return value
}
