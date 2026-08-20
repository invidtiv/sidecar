package targetactivation

import (
	"errors"
	"testing"

	"github.com/marcus/sidecar/internal/uirequest"
)

func TestResolveFileTargets(t *testing.T) {
	t.Parallel()
	plan, err := Resolve(uirequest.Target{Kind: uirequest.TargetKindFile, Value: "internal/app/model.go", Line: 42})
	if err != nil {
		t.Fatalf("resolve file: %v", err)
	}
	if plan.Kind != PlanOpenFile || plan.PluginID != FileBrowserPluginID {
		t.Fatalf("unexpected plan %+v", plan)
	}
	if plan.Path != "internal/app/model.go" || plan.Line != 42 {
		t.Fatalf("unexpected path/line %+v", plan)
	}

	cleaned, err := Resolve(uirequest.Target{Kind: uirequest.TargetKindFile, Value: "./docs/../docs/plan.md"})
	if err != nil {
		t.Fatalf("resolve cleanable file: %v", err)
	}
	if cleaned.Path != "docs/plan.md" {
		t.Fatalf("path not normalized: %q", cleaned.Path)
	}
}

func TestResolveFileRefusals(t *testing.T) {
	t.Parallel()
	for name, value := range map[string]string{
		"empty":    "  ",
		"absolute": "/etc/passwd",
		"home":     "~/.ssh/id_rsa",
		"escaping": "../../secrets.txt",
		"control":  "notes\x07.md",
	} {
		if _, err := Resolve(uirequest.Target{Kind: uirequest.TargetKindFile, Value: value}); err == nil {
			t.Fatalf("%s: expected refusal for %q", name, value)
		}
	}
	if _, err := Resolve(uirequest.Target{Kind: uirequest.TargetKindFile, Value: "a.go", Line: -3}); err == nil {
		t.Fatal("expected refusal for negative line")
	}
}

func TestResolveURLSafety(t *testing.T) {
	t.Parallel()
	plan, err := Resolve(uirequest.Target{Kind: uirequest.TargetKindURL, Value: "https://example.com/x."})
	if err != nil {
		t.Fatalf("resolve url: %v", err)
	}
	if plan.Kind != PlanOpenURL || plan.URL != "https://example.com/x" {
		t.Fatalf("unexpected plan %+v", plan)
	}
	for _, unsafe := range []string{"file:///etc/passwd", "javascript:alert(1)", "ftp://example.com", "https://", "not a url"} {
		if _, err := Resolve(uirequest.Target{Kind: uirequest.TargetKindURL, Value: unsafe}); err == nil {
			t.Fatalf("expected refusal for %q", unsafe)
		}
	}
}

func TestResolveUnsupportedKinds(t *testing.T) {
	t.Parallel()
	for _, kind := range []uirequest.TargetKind{uirequest.TargetKindIssue, uirequest.TargetKindDiff, uirequest.TargetKindResource} {
		_, err := Resolve(uirequest.Target{Kind: kind, Value: "td-123456"})
		if !errors.Is(err, ErrUnsupportedKind) {
			t.Fatalf("%s: want ErrUnsupportedKind, got %v", kind, err)
		}
	}
	if _, err := Resolve(uirequest.Target{}); err == nil {
		t.Fatal("expected refusal for kindless target")
	}
}
