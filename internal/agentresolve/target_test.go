package agentresolve_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/marcus/sidecar/internal/agentcontrol"
	"github.com/marcus/sidecar/internal/agentresolve"
)

func TestResolveTargetScopesExplicitSearchAndPreservesIdentity(t *testing.T) {
	want := agentcontrol.Target{Host: "aerie", Project: "sidecar", Session: "reviewer", Name: "Review", Namespace: "/private/socket", PaneID: "%7", PanePID: 42, ServerPID: 99, ServerIncarnation: "observed"}
	for _, tt := range []struct {
		name   string
		query  agentresolve.TargetQuery
		global bool
	}{
		{"explicit", agentresolve.TargetQuery{Target: "Review", Explicit: true}, true},
		{"own shell", agentresolve.TargetQuery{Target: "reviewer"}, false},
		{"project", agentresolve.TargetQuery{Target: "Review", Project: "sidecar", Explicit: true}, false},
		{"shell", agentresolve.TargetQuery{Target: "Review", Shell: "caller", Explicit: true}, false},
		{"shell and project", agentresolve.TargetQuery{Target: "Review", Shell: "caller", Project: "sidecar", Explicit: true}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			got, err := agentresolve.ResolveTarget(tt.query, func(target, shell, project string, global bool) (agentcontrol.Target, error) {
				calls++
				if target != tt.query.Target || shell != tt.query.Shell || project != tt.query.Project || global != tt.global {
					t.Fatalf("lookup = %q, %q, %q, global=%v; query=%+v global=%v", target, shell, project, global, tt.query, tt.global)
				}
				return want, nil
			})
			if err != nil || got != want || calls != 1 {
				t.Fatalf("resolved = %+v, err=%v, calls=%d; want %+v", got, err, calls, want)
			}
		})
	}
}

func TestResolveTargetRefusesMissingTargetBeforeLookup(t *testing.T) {
	_, err := agentresolve.ResolveTarget(agentresolve.TargetQuery{}, func(string, string, string, bool) (agentcontrol.Target, error) {
		t.Fatal("a missing target must not scan or consult UI focus")
		return agentcontrol.Target{}, nil
	})
	var typed *agentcontrol.Error
	if !agentcontrol.AsError(err, &typed) || typed.Code != agentcontrol.ErrNotFound || typed.Message != "target is required outside a managed shell" {
		t.Fatalf("missing target = %v", err)
	}
}

func TestResolveTargetKeepsRefusalAndTransportCauses(t *testing.T) {
	failure := errors.New("manifest unreadable")
	refusal := &agentcontrol.Error{Code: agentcontrol.ErrNotFound, Message: "unknown project"}
	for _, cause := range []error{failure, refusal, fmt.Errorf("lookup: %w", refusal)} {
		got, err := agentresolve.ResolveTarget(agentresolve.TargetQuery{Target: "reviewer"}, func(string, string, string, bool) (agentcontrol.Target, error) {
			return agentcontrol.Target{Session: "unsafe-partial-target"}, cause
		})
		var typed *agentcontrol.Error
		if got != (agentcontrol.Target{}) || !agentcontrol.AsError(err, &typed) || !errors.Is(err, cause) {
			t.Fatalf("failure target=%+v error=%v; cause=%v", got, err, cause)
		}
		if cause == failure {
			if typed.Code != agentcontrol.ErrTransport || typed.Message != failure.Error() {
				t.Fatalf("transport error = %+v", typed)
			}
		} else if typed != refusal {
			t.Fatalf("semantic refusal changed: %+v", typed)
		}
	}
}

func TestResolveTargetWithoutLookupReturnsTransportFailure(t *testing.T) {
	_, err := agentresolve.ResolveTarget(agentresolve.TargetQuery{Target: "reviewer"}, nil)
	var typed *agentcontrol.Error
	if !agentcontrol.AsError(err, &typed) || typed.Code != agentcontrol.ErrTransport {
		t.Fatalf("nil lookup = %v", err)
	}
}
