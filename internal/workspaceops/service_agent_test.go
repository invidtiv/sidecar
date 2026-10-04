package workspaceops

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentcontrol"
)

func TestAgentLauncherReadinessAndTargetPolicy(t *testing.T) {
	request := agentcontrol.StartRequest{
		Target: agentcontrol.Target{Host: "local", Project: "sidecar", Session: "reviewer", Name: "Review"},
		Kind:   "codex", Argv: []string{"codex", "--profile", "review"}, Timeout: 30 * time.Second,
	}
	pinned := request.Target
	pinned.PaneID, pinned.PanePID, pinned.ServerPID, pinned.Namespace = "%7", 42, 99, "/private/socket"
	for _, tt := range []struct {
		name           string
		wait, useReady bool
		wantTarget     agentcontrol.Target
	}{
		{"CLI pins observed pane", true, true, pinned},
		{"TUI keeps requested identity", true, false, request.Target},
		{"reconnect skips readiness", false, false, request.Target},
		{"reconnect has no ready identity", false, true, request.Target},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			var phases []string
			want := agentcontrol.Agent{Target: pinned}
			launcher := AgentLauncher{
				Wait: func(gotCtx context.Context, target agentcontrol.Target, timeout time.Duration) (agentcontrol.Snapshot, error) {
					phases = append(phases, "wait")
					if gotCtx != ctx || target != request.Target || timeout != request.Timeout {
						t.Fatalf("readiness changed context, target, or timeout: target=%+v timeout=%v", target, timeout)
					}
					return agentcontrol.Snapshot{Target: pinned}, nil
				},
				StartAgent: func(gotCtx context.Context, got agentcontrol.StartRequest) (agentcontrol.Agent, error) {
					phases = append(phases, "start")
					if gotCtx != ctx || got.Target != tt.wantTarget || got.Kind != request.Kind || got.Timeout != request.Timeout || !reflect.DeepEqual(got.Argv, request.Argv) {
						t.Fatalf("start request changed: %+v", got)
					}
					return want, nil
				},
			}
			got, stage, err := launcher.Start(ctx, request, tt.wait, tt.useReady)
			wantPhases := []string{"start"}
			if tt.wait {
				wantPhases = []string{"wait", "start"}
			}
			if !reflect.DeepEqual(got, want) || stage != "" || err != nil || !reflect.DeepEqual(phases, wantPhases) {
				t.Fatalf("agent=%+v stage=%q err=%v phases=%v; want %+v phases=%v", got, stage, err, phases, want, wantPhases)
			}
			if request.Target.PaneID != "" {
				t.Fatal("readiness mutated the caller's request")
			}
		})
	}
}

func TestAgentLauncherFailureDoesNotRetryAndKeepsRawError(t *testing.T) {
	for _, tt := range []struct {
		name      string
		stage     AgentStartStage
		wantCalls int
	}{
		{"readiness", AgentWaitReady, 0},
		{"provider", AgentStartProvider, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			boom := &agentcontrol.Error{Code: agentcontrol.ErrNotReady, Message: "busy", Err: errors.New("underlying failure")}
			partial := agentcontrol.Agent{Target: agentcontrol.Target{Session: "reviewer", PaneID: "%7"}}
			starts := 0
			launcher := AgentLauncher{
				Wait: func(context.Context, agentcontrol.Target, time.Duration) (agentcontrol.Snapshot, error) {
					if tt.stage == AgentWaitReady {
						return agentcontrol.Snapshot{}, boom
					}
					return agentcontrol.Snapshot{Target: partial.Target}, nil
				},
				StartAgent: func(context.Context, agentcontrol.StartRequest) (agentcontrol.Agent, error) {
					starts++
					return partial, boom
				},
			}
			got, stage, err := launcher.Start(context.Background(), agentcontrol.StartRequest{Target: partial.Target}, true, true)
			want := partial
			if tt.stage == AgentWaitReady {
				want = agentcontrol.Agent{}
			}
			if err != boom || stage != tt.stage || starts != tt.wantCalls || !reflect.DeepEqual(got, want) {
				t.Fatalf("agent=%+v stage=%q err=%v starts=%d; want agent=%+v stage=%q starts=%d", got, stage, err, starts, want, tt.stage, tt.wantCalls)
			}
		})
	}
}
