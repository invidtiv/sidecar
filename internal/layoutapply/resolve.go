package layoutapply

import (
	"fmt"

	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/resourceview"
	"github.com/marcus/sidecar/internal/terminallink"
	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/workspacediff"
)

// ResolveTargets turns a descriptor's target strings into resolved uirequest
// targets through ResolveTarget — the exact classification the CLI's `open`
// argument goes through, here on the host where the workspace root is known.
func ResolveTargets(kind panelayout.Kind, spec uirequest.LayoutPane, root string, matchers []terminallink.ResourceMatcher) ([]uirequest.Target, string) {
	if len(spec.Targets) == 0 {
		if kind == panelayout.Diff {
			return []uirequest.Target{{Kind: uirequest.TargetKindDiff, Value: workspacediff.IdentityWorkingTree}}, ""
		}
		return nil, "a " + kind.Name() + " pane needs at least one target"
	}
	want, ok := WireKind(kind)
	if !ok {
		return nil, "unsupported pane kind " + kind.Name()
	}
	targets := make([]uirequest.Target, 0, len(spec.Targets))
	for _, raw := range spec.Targets {
		var (
			tgt uirequest.Target
			err error
		)
		if kind == panelayout.Resource {
			tgt, err = uirequest.ResolveResourceTarget(spec.Provider, raw)
		} else {
			tgt, err = uirequest.ResolveTarget(root, raw, 0, uirequest.ResolveOptions{Diff: kind == panelayout.Diff})
		}
		if err != nil {
			return nil, fmt.Sprintf("target %q: %v", raw, err)
		}
		if tgt.Kind != want {
			return nil, fmt.Sprintf("target %q resolves to a %s pane, want %s", raw, wireNameForTarget(tgt.Kind), kind.Name())
		}
		targets = append(targets, tgt)
	}
	if kind == panelayout.Resource {
		ref, refusal := resourceview.ReferenceForLocator(matchers, spec.Provider, targets[0].Value)
		if refusal != "" {
			return nil, refusal
		}
		targets[0].Matcher = ref.Matcher
	}
	return targets, ""
}

func WireKind(kind panelayout.Kind) (uirequest.TargetKind, bool) {
	switch kind {
	case panelayout.Document:
		return uirequest.TargetKindFile, true
	case panelayout.Issue:
		return uirequest.TargetKindIssue, true
	case panelayout.Diff:
		return uirequest.TargetKindDiff, true
	case panelayout.Note:
		return uirequest.TargetKindNote, true
	case panelayout.Resource:
		return uirequest.TargetKindResource, true
	default:
		return "", false
	}
}

func wireNameForTarget(kind uirequest.TargetKind) string {
	if mapped, ok := panelayout.KindByName(string(kind)); ok {
		return mapped.Name()
	}
	return string(kind)
}
