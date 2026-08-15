package workspaceops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/marcus/sidecar/internal/projectdir"
)

// PendingCreationJournal v2 persists only transport-neutral operation and Git
// identity. Version 1 is accepted: field-based JSON decoding ignores the
// plugin-owned Agent pointer and presentation state stored by older builds.
type PendingCreationJournal struct {
	Version     int            `json:"version"`
	RepoKey     string         `json:"repoKey"`
	OperationID string         `json:"operationId"`
	Plan        WorktreePlan   `json:"plan"`
	Worktree    WorktreeRecord `json:"worktree"`
}

func PendingCreationPath(ctx context.Context, plan *WorktreePlan) (string, error) {
	if plan == nil {
		return "", fmt.Errorf("missing creation plan")
	}
	dir, err := projectdir.WorktreeDirContext(ctx, plan.MainWorktree, plan.Path)
	if err != nil {
		return "", err
	}
	key := StablePathKey(plan.OperationID)
	return filepath.Join(dir, "pending-creation-"+key[:12]+".json"), nil
}

func PersistPendingCreation(ctx context.Context, plan *WorktreePlan, record *WorktreeRecord) error {
	if record == nil {
		return fmt.Errorf("missing created worktree identity")
	}
	path, err := PendingCreationPath(ctx, plan)
	if err != nil {
		return fmt.Errorf("resolve pending creation journal: %w", err)
	}
	journal := PendingCreationJournal{Version: 2, RepoKey: plan.RepoKey, OperationID: plan.OperationID, Plan: *plan, Worktree: *record}
	data, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return fmt.Errorf("encode pending creation journal: %w", err)
	}
	if err := WriteDurableFile(path, append(data, '\n'), 0644); err != nil {
		return fmt.Errorf("write pending creation journal: %w", err)
	}
	return nil
}

func LoadPendingCreation(ctx context.Context, projectRoot string, candidates []WorktreeRecord, repoKey string) (*PendingCreationJournal, error) {
	for _, candidate := range candidates {
		dir, err := projectdir.WorktreeDirContext(ctx, projectRoot, candidate.Path)
		if err != nil {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasPrefix(entry.Name(), "pending-creation-") || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			data, readErr := os.ReadFile(filepath.Join(dir, entry.Name()))
			if readErr != nil {
				continue
			}
			var journal PendingCreationJournal
			if json.Unmarshal(data, &journal) != nil || (journal.Version != 1 && journal.Version != 2) || journal.RepoKey != repoKey {
				continue
			}
			if journal.Worktree.Key != candidate.Key || filepath.Clean(journal.Worktree.Path) != filepath.Clean(candidate.Path) || journal.Worktree.HEADOID != candidate.HEADOID {
				continue
			}
			return &journal, nil
		}
	}
	return nil, nil
}

func RemovePendingCreation(plan *WorktreePlan) error {
	return RemovePendingCreationWithOps(plan, os.Remove, func(dir string) error {
		file, err := os.Open(dir)
		if err != nil {
			return err
		}
		defer file.Close()
		return file.Sync()
	})
}

func RemovePendingCreationWithOps(plan *WorktreePlan, remove func(string) error, syncDir func(string) error) error {
	path, err := PendingCreationPath(context.Background(), plan)
	if err != nil {
		return err
	}
	if err := remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := syncDir(filepath.Dir(path)); err != nil {
		return fmt.Errorf("sync pending creation journal directory: %w", err)
	}
	return nil
}
