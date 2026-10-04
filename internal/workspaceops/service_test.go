package workspaceops

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestCreationJournalsPartialIdentityAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	plan := &WorktreePlan{RepoKey: "repo", Path: "/created"}
	record := &WorktreeRecord{Path: plan.Path, HEADOID: "created-oid"}
	journaled := false
	svc := Service{
		Execute: func(context.Context, string, *WorktreePlan) (*WorktreeRecord, error) {
			cancel()
			return record, context.Canceled
		},
		Journal: func(ctx context.Context, got *WorktreePlan, identity *WorktreeRecord) error {
			if ctx.Err() != nil || got != plan || identity != record {
				t.Fatal("partial identity lost or journal inherited cancellation")
			}
			journaled = true
			return nil
		},
		Identity: func(context.Context, *WorktreePlan) []SetupOutcome {
			t.Fatal("setup ran on an interrupted add")
			return nil
		},
		Finalize: func(*WorktreePlan) error { t.Fatal("interrupted journal removed"); return nil },
	}
	result := svc.CreateWorktree(ctx, plan, false)
	if !journaled || result.Record != record || !errors.Is(CreationError(result), context.Canceled) {
		t.Fatalf("result=%+v journaled=%v", result, journaled)
	}
}

func TestCreationFailureRetainsJournalAndRetryUsesSameSetup(t *testing.T) {
	for _, failure := range []string{"journal", "identity", "setup", "optional", "finalize", "none"} {
		t.Run(failure, func(t *testing.T) {
			var stages []string
			boom := errors.New("failure")
			plan := &WorktreePlan{RepoKey: "repo"}
			outcome := func(kind string) []SetupOutcome {
				stages = append(stages, kind)
				if kind == failure || (kind == "setup" && failure == "optional") {
					return []SetupOutcome{{Kind: kind, Action: kind, Required: failure != "optional", Err: boom}}
				}
				return nil
			}
			svc := Service{
				Execute: func(_ context.Context, key string, got *WorktreePlan) (*WorktreeRecord, error) {
					if key != plan.RepoKey || got != plan {
						t.Fatal("confirmed plan changed")
					}
					stages = append(stages, "execute")
					return &WorktreeRecord{HEADOID: "created"}, nil
				},
				Journal: func(context.Context, *WorktreePlan, *WorktreeRecord) error {
					stages = append(stages, "journal")
					if failure == "journal" {
						return boom
					}
					return nil
				},
				Identity: func(context.Context, *WorktreePlan) []SetupOutcome { return outcome("identity") },
				Setup:    func(context.Context, *WorktreePlan) []SetupOutcome { return outcome("setup") },
				Finalize: func(*WorktreePlan) error {
					stages = append(stages, "finalize")
					if failure == "finalize" {
						return boom
					}
					return nil
				},
			}
			result := svc.CreateWorktree(context.Background(), plan, false)
			want := []string{"execute", "journal", "identity", "setup"}
			if failure == "none" || failure == "optional" || failure == "finalize" {
				want = append(want, "finalize")
			}
			if !reflect.DeepEqual(stages, want) {
				t.Fatalf("stages=%v want=%v", stages, want)
			}
			if requiredFailure := failure != "none" && failure != "optional"; errors.Is(CreationError(result), boom) != requiredFailure {
				t.Fatalf("required failure lost: %+v", result)
			}
			stages = nil
			svc.SetupWorktree(context.Background(), plan, false)
			if !reflect.DeepEqual(stages, []string{"identity", "setup"}) {
				t.Fatalf("retry recreated worktree: %v", stages)
			}
		})
	}
}

func TestCreationBeforeMutationFailureDoesNotJournal(t *testing.T) {
	boom := errors.New("branch exists")
	svc := Service{
		Execute: func(context.Context, string, *WorktreePlan) (*WorktreeRecord, error) { return nil, boom },
		Journal: func(context.Context, *WorktreePlan, *WorktreeRecord) error {
			t.Fatal("journal written without an identity")
			return nil
		},
	}
	if result := svc.CreateWorktree(context.Background(), &WorktreePlan{}, false); result.Record != nil || !errors.Is(result.Err, boom) {
		t.Fatalf("result=%+v", result)
	}
}
