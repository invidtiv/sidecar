# tmux crash recovery — 2026-08-22

The default tmux server died and restarted at **13:29 PDT**. Every Sidecar-managed shell was destroyed. This document records what was in flight, what survived, and exactly where each interrupted thread resumes.

## Bottom line

No committed work was lost. Three threads were interrupted mid-task; all three are recoverable from disk, and two of the three open questions are now answered (see below). One uncommitted refactor in `tasks-modals` does **not** compile and is the only genuinely fragile item.

## What the crash actually destroyed

Sidecar reaps shell records whose tmux session has vanished, so `shells.json` was rewritten to an empty list for `recall`, `clara-home`, `vibes`, `clara`, and `td` at 13:31 — two minutes after the server restart. That erased each shell's display name, working directory, and agent configuration. Only `sidecar/shells.json` retains a pre-crash entry (`sidecar-global-workspace`, "workspace row convergence"), and only because that shell predates the reap window.

There is no backup mechanism: the sole `.bak` in the state tree is `shells.json.overwritten-phase2-20260808T112359.bak` from Aug 8. The five emptied manifests are not recoverable — the data is gone. The structural fix is [shell-record-durability.md](../implemented/shell-record-durability.md) (epic td-e4578b): a tmux server going away must degrade shells to offline rows, never to an empty `shells.json`.

## Where the agent sessions live

Work was split across four harnesses. Their transcripts all survived the crash:

| Harness | Session store | Keyed by |
|---|---|---|
| Claude Code | `~/.claude/projects/<slug>/<uuid>.jsonl` | cwd slug |
| pi | `~/.pi/agent/sessions/--<slug>--/<ts>_<uuid>.jsonl` | cwd slug |
| opencode | `~/.local/share/opencode/opencode.db` (SQLite, `session`/`message`/`part`) | `directory` column |
| grok | `~/.grok/sessions/<url-encoded-cwd>/<uuid>/` | URL-encoded cwd |

Codex sessions (`~/.codex/sessions/2026/08/22/`) were all `braid` pipeline automation and unaffected.

## Thread 1 — Go 1.27 upgrade sweep (td → tasks → recall → sidecar)

A coordinated toolchain bump across four repos, driven from pi.

- **td** — complete on the unmerged `go-upgrade` worktree branch (`~/code/td-go-upgrade`, `6b748b0`). Working tree clean. `td` main is still at `009ad5e` ("docs: plan Go 1.27 toolchain upgrade"). **Resumes at: merge `go-upgrade` into main.**
- **tasks** — `~/code/tasks-go-upgrade` on branch `go-upgrade`, clean.
- **recall** — uncommitted and verified below.
- **sidecar** — plan written (`docs/plans/active/go-1-27-upgrade.md`, untracked, 09:47) but the bump itself never started. `go.mod`/`go.work` are still at `1.26.0`.

### recall: the open question is resolved

The pi session died at 20:19:20Z mid-verification, having just seen two test failures and never learning whether they were caused by the upgrade. They are **not**.

The upgrade itself is clean: `go mod tidy` produced no drift, `go build ./...` and `make lint` (0 issues) both pass under `go 1.27.0`.

The two failures reproduce identically at the old `go 1.26.4` directive and under `GOEXPERIMENT=nojsonv2`, so they are pre-existing and unrelated to Go 1.27 or to json/v2:

- `internal/adapters/tasks` — `TestLiveBinary`: `metadata[project] = "Inbox" for an Inbox task; the rollup excludes Inbox`, plus a wall-clock assertion (`7 wall for one search across 5 invocations`). Shells out to the live `tasks` CLI, so it depends on ambient task data.
- `internal/cli` — `TestServeWaitsForActiveRequestDrainBeforeReturning`: timing-sensitive drain assertion.

**Resumes at: commit the recall bump.** The uncommitted diff (`go.mod` → 1.27.0, `setup-go` switched to `go-version-file: go.mod`, golangci-lint pinned to v2.13.1 in both workflows, plus a Makefile guard that fails loudly when the local linter drifts from the CI pin) is finished work. The two failures should be filed separately as pre-existing.

Also untracked in recall, unrelated to this thread: `docs/plans/active/okf-trust-tier.md` (09:07 today) and `plans/kuoder-brain.md` (Aug 12).

## Thread 2 — sidecar truecolor / COLORTERM (uncommitted, `internal/tty`)

A pi session (`~/.pi/agent/sessions/--Users-marcus-code-sidecar--/2026-08-22T15-42-10-839Z_*.jsonl`, 373 messages) was debugging `TestPrepareServerAdvertisesTruecolor` when the crash hit. `session_test.go` was last written at 13:18 — one minute before.

The change adds `advertiseTruecolor()` to `PrepareServer`: set global `COLORTERM=truecolor` when absent, and append `,*:Tc` to `terminal-overrides` unless a `Tc`/`RGB` entry already exists. Motivation was that pi quantized its theme to the 256-colour cube inside sidecar panes because the tmux server's environment carried no `COLORTERM`.

### The failure is in the test, not the implementation

`session_test.go:280-288` asserts:

```go
// New sessions inherit the server's global environment: that inheritance
// is the whole point, since pane applications read COLORTERM from it.
paneOut, err := exec.Command("tmux", "show-environment", "-t", "probe", "COLORTERM").Output()
```

That premise is false. A *session's* environment is a distinct table from the server's *global* environment; tmux merges the global environment into new pane **processes**, not into per-session environment. `show-environment -t <session>` therefore reports `unknown variable` even when the global set succeeded.

Probed on an isolated server, with `COLORTERM` stripped from the server's own process environment:

```
show-environment -g COLORTERM   →  COLORTERM=truecolor   exit 0   ✅
show-environment -t after       →  unknown variable      exit 1   ← what the test checks
```

The preceding assertion at lines 275-278 (`show-environment -g`) already passes. **Resumes at: replace the lines 280-288 probe.** Either drop it as redundant with the global check, or assert the thing that actually matters by reading `$COLORTERM` from inside a spawned pane process rather than from the session environment table.

Note when probing this by hand: an interactive shell usually exports `COLORTERM=truecolor` already, and the tmux **server** inherits it from whichever client started it. A probe that only does `env -u COLORTERM` on the `new-session` call will read that inherited value back and appear to pass. Strip it from the `start-server` invocation too.

## Thread 3 — tasks-modals (uncommitted, does not compile) ⚠️

`~/code/tasks-modals` on branch `modals`, from the opencode session "Redesigning tasks modals to match Sidecar" (last active 11:49). This is the largest at-risk item and the one the crash left in the worst state.

Uncommitted: 792 insertions / 236 deletions across 5 tracked files, plus 6 untracked new files (`button.go`, `chrome.go`, `keychips.go`, `scrollregion.go`, `statusline.go`, and the plan `docs/plans/active/sidecar-style-modals.md`).

`go build ./...` fails — `fieldmodalrender.go` calls symbols that were never written:

- `PaintBorderSlot` — referenced at lines 248, 251-253, 419, 423, 426, 430; mentioned in `chrome.go` but defined nowhere.
- `f.variant` — `FieldModal` has no such field (`variantForPaint()` at line 144 returns it).
- `labelWindow` declared and not used at line 371.

The extraction was interrupted partway: `fieldmodalrender.go` had already been rewritten against the new chrome API while `chrome.go` was still being written (11:20-11:28). **Resumes at: finish `chrome.go` — define `PaintBorderSlot` and add the `variant` field to `FieldModal`.** Everything else in `internal/tui/...` still passes.

## Threads that completed cleanly (no action)

- **intersections** — polyline road geometry in `networkBuilder.ts` committed as `781688c`, `.todos` state as `c111e8a`, both pushed to the new private repo `github.com/marcus/intersections`. Typecheck and 40 tests passed before commit. Worktrees `intersections-models` and `intersections-world` are clean apart from `.todos` churn; `intersections-world` has one untracked `tests/unit/world/probe.test.ts`.
- **vibes** — Sparkle release tooling landed and pushed as `83b9591`: preflight now refuses a build number that does not exceed the last published one, `make mac-bump` collapses the four-place version edit into one step, and 25 historical releases were backfilled as tags (`v0.2.0`…`v0.11.0`).
- **clara-home** — auto-commit through `bcc55ac`; only `data/stats/usage-2026-08.jsonl` is dirty, which is generated. A grok session ("Sidecar launch video logos and Remotion intro", grok-4.6) was last active 13:17 with `last_turn_summary`: "Remotion 4.0.514 intro: 9-agent logo flash ready".
- **sidecar-scroll**, **sidecar-cross-project-td-links**, **sidecar-files-position**, **vibes-website**, **clara**, **braid**, **sidecar-jira**, **sidecar-public-site** — all clean.

## Suggested resume order

1. `tasks-modals` — finish `chrome.go` and get it compiling; it is the only thread that cannot survive another accident intact.
2. `recall` — commit the Go 1.27 bump; file the two pre-existing failures separately.
3. `td` — merge `go-upgrade` to main, then `tasks`, then start sidecar's own bump per its plan.
4. `sidecar` — fix the `session_test.go` probe and commit the truecolor change.
