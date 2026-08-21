# Permanent Note Deletion and Purge Policy

**Status:** Proposed **Created:** 2026-08-21 **Task:** td-90a9bd **Primary repo:** `~/code/td` owns the capability; sidecar adds a UI affordance over it.

## Goal

Soft-deleted td notes currently live forever: `deleted_at` is stamped, every query hides them, and nothing ever removes the rows. This plan adds permanent destruction of soft-deleted notes plus an opt-in retention policy that purges soft-deleted notes after 30 days.

Scoping decision (Marcus): forensic erasure is not required. We do not need zero trace in history files; we need the note gone from **every UI surface and every query path**, and not restorable through any supported operation.

## Current state (verified)

- **Storage.** Notes live in `<project>/.todos/issues.db`, migration v28 (`internal/db/schema.go:477-494`). `deleted_at` is nullable RFC3339 TEXT; `idx_notes_deleted` already exists, so age-based candidate scans are cheap.
- **Soft delete.** `DeleteNote` stamps `deleted_at`/`updated_at` and appends an `action_log` row via `insertNoteAction` (`internal/db/notes.go:282-315`, `406-417`). `RestoreNote` (`notes.go:338`) requires a prior `deleted_at`. All writers serialize through the flock in `withWriteLock` (`internal/db/db.go:170-188`) — a purge must too.
- **Every query/UI path already filters soft deletes**: `ListNotes` (`notes.go:159-162`), `GetNote`, mutation guards, TDQ's hardcoded `IncludeDeleted:false` (`internal/query/note_evaluator.go:73-77`). Only explicit opt-in views see them (`td note list --deleted`, sidecar's Deleted filter). So a real SQL `DELETE` needs no new hiding logic anywhere.
- **Undo cannot resurrect notes.** `performUndo` has no `"note"` case (`cmd/undo.go:112-131`) — it rejects note entity types outright.
- **Sync (this is the crux).** `action_log` is the push source (`internal/sync/client.go:44-48`). At push time `NormalizeActionType` maps td's plain `delete` to canonical *soft_delete* (`internal/events/taxonomy.go:155-169`) — that is how today's soft delete propagates. On the apply side, canonical `delete` runs `deleteEntity`: a real `DELETE`, no-op when the row is absent (`internal/sync/events.go:299-322`, `495-520`). Every resurrection path is already closed: updates fall back to `upsertEntityIfExists` (`events.go:386-390`), soft-delete/restore on missing rows no-op (`events.go:522-538`), and backfill excludes soft-deleted rows (`internal/sync/backfill.go:21`, `275`). Notes already allow hard `ActionDelete` combinations (`taxonomy.go:253-258`). Because normalization happens at push time, the server log stores canonical `delete`, so even older peers apply it with existing code — no cross-version risk.
- **Precedents to copy.** `td session cleanup` — preview by default, `--force` executes, `--older-than 7d` default, JSON envelope distinguishing `would_*` from executed results (`cmd/system.go:474-621`, flags `:1140-1142`); duration parser accepting `30d` (`internal/session/session.go:360-395`). Retention work riding the autosync tick — `PruneSyncHistory(tx, 500)` inside the push transaction (`cmd/autosync.go:637-639`). Ticker-based retention sweeps in the API server (`internal/api/server.go:100-188`).
- **Legacy timestamps.** `parseNoteDeletedAt` tolerates six historical layouts (`notes.go:28-49`); seeding tests for old-format rows have a precedent (`TestListNotesRecognizesHistoricalDeletedAt`, `internal/db/notes_test.go:45-88`).
- **Sidecar.** Production store shells out to `td --json note ...` with an 8s timeout (`internal/plugins/notes/store.go:114-140`) and must never open `issues.db` itself (WAL corruption, td-adbf16). The Deleted view, danger-styled delete modal, optimistic delete with rollback, and a 20-entry undo stack already exist (`plugin.go:1608-1613`, `mutations.go:309-372`, `pushUndo` `plugin.go:3336-3342`, `delete_modal.go:72-174`).

## Design decisions (settled)

1. **Purge is a real `DELETE FROM notes WHERE id = ?` under `withWriteLock`.** No migration, no tombstone table.
2. **History traces remain, deliberately.** Prior `action_log` snapshots for purged notes stay put; undo cannot touch them. No VACUUM, no secure wipe — content bytes may persist in WAL/free pages until SQLite reuses them, which satisfies the stated scoping.
3. **Sync safety via a new local action type.** Add `models.ActionPurge ActionType = "note_purge"`; register it in `NormalizeActionType`'s hard-delete mapping and in `AllActionTypes`. The purge event then propagates as canonical `delete` and peers hard-delete through existing, tested plumbing. `new_data` stays empty so no content rides the event; Phase 1 adds a sync test asserting that.
4. **Purge requires the note to be currently soft-deleted.** A live note is refused with guidance to delete it first — two-step destruction by construction, matching the UI flow (the purge affordance only exists in the Deleted view).
5. **CLI shape (td).**
   - `td note delete --permanent [-f/--force] <id>` — targeted destruction; refuses live notes; `--force` makes it non-interactive for agents/scripts; JSON emits `note_purged`.
   - `td note purge [--older-than 30d] [--force] [--json]` — bulk sweep modeled exactly on `session cleanup`: preview by default (candidate IDs + count), `--force` executes, envelope distinguishes `would_purge` from purged IDs.
6. **Auto-policy is opt-in per project.** New `models.Config` field `NotesPurgeDeletedAfterDays int` (`json:"notes_purge_deleted_after_days"`; 0/absent = disabled) in `.todos/config.json`, plus a `TD_NOTES_PURGE_DELETED_AFTER_DAYS` env override resolved in one place. When enabled, the sweep runs after each successful autosync push (outside the push transaction, under its own write lock), logging the purged count — same placement rationale as `PruneSyncHistory`. Projects without sync use manual `td note purge` (cron-friendly).
7. **Public API parity.** `pkg/notes` gains `Purge(id)` and `PurgeDeletedBefore(cutoff)` wrapping the store methods, so in-process clients (sidecar's test backend) match CLI capability.
8. **Sidecar stays presentation-layer.** The purge affordance exists only in the Deleted view and shells out through the adapter (`td --json note delete --permanent -f`). Danger-styled confirmation modal reusing the `delete_modal.go` machinery with explicit permanent-destruction copy; optimistic row removal; **no undo-stack entry** (there is nothing to restore); failure rolls the list back like `finishOptimisticDelete`.

## Phasing

Each phase is independently shippable; 1–3 are td-only.

1. **Core store + sync taxonomy.** `models.ActionPurge`; taxonomy registration; `db.PurgeNote(id)` (soft-deleted guard, locked DELETE, `note_purge` action_log row); `db.PurgeDeletedNotesBefore(cutoff)` returning purged IDs. Tests: guard refusals, legacy `deleted_at` formats, and sync-invariant tests (replica applies purge as hard-delete; subsequent update/restore events stay no-ops; pushed purge event carries no content fields).
2. **CLI.** `--permanent/-f` flags on `noteDeleteCmd` (`cmd/note.go:299-332`); new `td note purge` command following `session cleanup`'s preview/force/envelope conventions and `ParseDuration` for `30d`. Tests mirror `cmd/session_json_test.go` (preview vs force envelopes, refusal cases).
3. **Auto-cleanup policy.** Config field + load/save round-trip; env override; autosync post-push hook with slog'd counts; CHANGELOG and docs touch-up.
4. **Release seam.** `pkg/notes` wrappers + tests; tag/release td; only then bump sidecar's dependency. `GOWORK=off` build/test against the released td before sidecar work merges (house rule for cross-repo seams).
5. **Sidecar UI.** `Purge` added to the `noteStore` interface (`store.go:31-46`) + production shell-out + `NewTestStore` implementation via `pkg/notes`; Deleted-view keybinding registered in `internal/keymap/bindings.go` with a short footer command (`Purge`); confirm modal; optimistic removal without undo push; failure rollback.

## Acceptance criteria (from td-90a9bd)

- [ ] `td note delete --permanent <id>` permanently removes the note from the database (Phases 1–2).
- [ ] Notes plugin offers permanent deletion on soft-deleted notes with confirmation (Phase 5).
- [ ] Auto-cleanup safely purges deleted notes older than 30 days (Phase 3).

## Explicit non-goals

- No scrubbing/redaction of `action_log` history; no secure media erasure or VACUUM.
- No change to soft-delete behavior, restore, or undo semantics.
- No sidecar-owned storage, metadata, or notes CLI — td owns the capability; sidecar is a projection of it.
- No stronger multi-device guarantee than td's existing last-event-wins sync: a peer restoring concurrently after a purge event loses (restore no-ops on the missing row). This matches intent and is documented here, not fought.

## Risks / open questions

- **Payload leak (checked in Phase 1).** Assert via sync test that the purge event's payload contains no title/content, regardless of which `action_log` column feeds it.
- **Non-sync projects** get no background sweep until the optional opportunistic hook lands; manual/cron `td note purge` covers them. Revisit only if requested.
- **Optional follow-up (not scheduled):** once-daily sweep on monitor start guarded by a last-run timestamp, for coverage without sync. Decide after Phase 3 usage.

## Verification contract

- td: focused store tests (guards, legacy timestamps, lock discipline), CLI JSON envelope tests, sync apply/push invariants, `go test ./...` + `go build ./...` green.
- Cross-repo: two-replica sync journey in the e2e harness (`test/e2e/harness.go`) — soft-delete on A, purge on A, pull on B ends with the row gone and no resurrection after later update/restore events.
- Sidecar: isolated `./scripts/tmux-drive.sh` proof (`paths` checked first; both tmux socket and state tree isolated): create note → X to delete → Deleted view → purge → confirm modal → row vanishes; `td note list --deleted` shows nothing; undo stack has no entry; failed purge rolls the list back. Stop the driver when done; never touch the default tmux server.
