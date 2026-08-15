package workspacediff

import (
	"context"
	"os/exec"
	"strings"
	"unicode"
)

// TargetKind is which git object a Diff view is showing.
type TargetKind int

const (
	// TargetWorkingTree is git diff HEAD + untracked. Identity is "wt".
	TargetWorkingTree TargetKind = iota
	// TargetCommit is one commit as the root view.
	TargetCommit
	// TargetRange is git diff A..B or A...B.
	TargetRange
)

// IdentityWorkingTree is TargetWorkingTree.Identity(). Never "HEAD".
const IdentityWorkingTree = "wt"

// Target is the spec a Diff tab or leaf is showing.
type Target struct {
	Kind TargetKind
	A    string // resolved left rev; empty for working-tree
	B    string // resolved right rev for ranges
	Dots string // ".." or "..." for ranges; empty otherwise
	Path string // optional file to select once the load lands
}

// WorkingTreeTarget is the Diff tab's working-tree view.
func WorkingTreeTarget() Target {
	return Target{Kind: TargetWorkingTree}
}

// TabLabel is the short header label for this target.
func (t Target) TabLabel() string {
	switch t.Kind {
	case TargetCommit:
		if t.A == "" {
			return "Commit"
		}
		if len(t.A) > 7 {
			return t.A[:7]
		}
		return t.A
	case TargetRange:
		a, b := t.A, t.B
		if len(a) > 7 {
			a = a[:7]
		}
		if len(b) > 7 {
			b = b[:7]
		}
		dots := t.Dots
		if dots != "..." {
			dots = ".."
		}
		if a == "" || b == "" {
			return "Range"
		}
		return a + dots + b
	default:
		return "Working Tree"
	}
}

// Identity is the stable tab key and the only string that crosses the request bus.
func (t Target) Identity() string {
	switch t.Kind {
	case TargetCommit:
		if t.A == "" {
			return ""
		}
		return "c:" + t.A
	case TargetRange:
		if t.A == "" || t.B == "" {
			return ""
		}
		dots := t.Dots
		if dots != "..." {
			dots = ".."
		}
		return "r:" + t.A + dots + t.B
	default:
		return IdentityWorkingTree
	}
}

// ParseSpec accepts user-facing forms and Identity forms.
func ParseSpec(raw string) (Target, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Target{}, false
	}
	if raw == IdentityWorkingTree || raw == "working-tree" {
		return Target{Kind: TargetWorkingTree}, true
	}
	if rest, ok := strings.CutPrefix(raw, "c:"); ok {
		if rest == "" {
			return Target{}, false
		}
		return Target{Kind: TargetCommit, A: rest}, true
	}
	if rest, ok := strings.CutPrefix(raw, "r:"); ok {
		return parseRange(rest)
	}
	if rest, ok := strings.CutPrefix(raw, "commit"); ok {
		rev := strings.TrimSpace(rest)
		if rev == "" {
			return Target{}, false
		}
		return Target{Kind: TargetCommit, A: rev}, true
	}
	if t, ok := parseRange(raw); ok {
		return t, true
	}
	if isRevToken(raw) {
		return Target{Kind: TargetCommit, A: raw}, true
	}
	return Target{}, false
}

func parseRange(raw string) (Target, bool) {
	for _, dots := range []string{"...", ".."} {
		i := strings.Index(raw, dots)
		if i <= 0 {
			continue
		}
		a, b := raw[:i], raw[i+len(dots):]
		if a == "" || b == "" {
			continue
		}
		if strings.Contains(b, "..") {
			continue
		}
		return Target{Kind: TargetRange, A: a, B: b, Dots: dots}, true
	}
	return Target{}, false
}

func isRevToken(raw string) bool {
	if raw == "" || strings.ContainsAny(raw, " \t\n") {
		return false
	}
	if raw == "HEAD" || strings.HasPrefix(raw, "HEAD~") || strings.HasPrefix(raw, "HEAD^") {
		return true
	}
	if looksLikeHexRev(raw) {
		return true
	}
	// Branch-shaped tokens are accepted so sidecar open --diff origin/main can
	// parse; ResolveSpec is what existence-gates them.
	for _, r := range raw {
		if r == '/' || r == '-' || r == '_' || r == '.' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			continue
		}
		return false
	}
	return true
}

func looksLikeHexRev(raw string) bool {
	if n := len(raw); n < 7 || n > 64 {
		return false
	}
	for _, r := range raw {
		if r < '0' || r > '9' && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// ResolveSpec runs git rev-parse in workdir and fills A/B with object names.
// Working-tree targets are returned unchanged.
func ResolveSpec(ctx context.Context, workdir string, t Target) (Target, error) {
	if t.Kind == TargetWorkingTree {
		return t, nil
	}
	if t.Kind == TargetCommit {
		oid, err := revParseCommit(ctx, workdir, t.A)
		if err != nil {
			return Target{}, err
		}
		t.A = oid
		return t, nil
	}
	a, err := revParseCommit(ctx, workdir, t.A)
	if err != nil {
		return Target{}, err
	}
	b, err := revParseCommit(ctx, workdir, t.B)
	if err != nil {
		return Target{}, err
	}
	t.A, t.B = a, b
	if t.Dots != "..." {
		t.Dots = ".."
	}
	return t, nil
}

func revParseCommit(ctx context.Context, workdir, rev string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	cmd.Dir = workdir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
