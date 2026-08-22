# Plan: Cross-project td issue links

## Goal

A `td-*` reference clicked in one project should open even when the issue lives in another project's td store. Sidecar searches its other configured projects, opens the card fetched from the owning store **in context** (same issue card, correct live refresh), and marks it with a **project badge** so the user can see it came from elsewhere.

Local behavior is untouched: if the issue resolves in the current project, nothing changes. The cross-project search runs only when the local lookup misses — lazy and user-initiated (a click), never on startup.

Out of scope: session/project switching actions on cross-project cards, kanban and issue-list surfaces, td CLI changes, any durable index or registry file.

---

## Current behavior (baseline)

| Concern | Where |
|---|---|
| Detection of `td-*` refs | `internal/contentlink/scan.go` (regex + scanner), wrapped for terminals by `internal/terminallink/scan.go` |
| Span → activation target | `internal/uirequest/target.go`, executed by `internal/targetactivation/activate.go` |
| Fetch | `issueview.Fetch(workDir, issueID)` runs `td show <id> -f json` with `cmd.Dir = workDir` (`internal/issueview/fetch.go:72-109`); read-only env `TD_SYNC_AUTO_START=0`/`TD_ANALYTICS=false` (`fetch.go:132-137`) |
| Tree sections | `attachTree` runs `td tree` follow-ups, failures are non-fatal (`fetch.go:139-159`) |
| Miss handling | generic `issue %q not found` (`fetch.go:102`), rendered as in-card error rows (`model.go:772-778`) or the app modal's danger pane (`issue_preview_modal.go:301-313`). No fallback exists. |
| Card model | `Model.Load` stores the fetch directory in `m.workDir` (`model.go:153-180`, field at `:80`); live `Refresh` re-fetches from `m.workDir` (`live.go:87`); hosts watch the store directory via `StoreTargets` → `tdroot.ResolveDBPath` (`live.go:34-40`) |
| Hosts | workspace issue panes (`internal/plugins/workspace/issue_panes.go:137,:405,:416`), app preview modal (`internal/app/update.go:2854-2862`), global Sessions overview (`internal/overview/content_deck.go:82` → `internal/contentpanes/viewers.go:186`) |
| Store location | `tdroot.ResolveTDRoot` collapses worktrees onto a shared root via the centralized `td-root` file, legacy `.td-root`, or git main-worktree `.todos` (`internal/tdroot/tdroot.go:48-106`) |
| Candidate source | configured projects: `config.Projects.List` (`internal/config/config.go:49-63`), available to plugins via `plugin.Context.Config` (`internal/plugin/context.go:22`) and to the app as `m.cfg` |

td itself has no cross-project concept: one SQLite DB per project root (`<root>/.todos/issues.db`), context supplied solely by cwd/`-w`/`TD_WORK_DIR`. That existing addressing surface is all this plan needs.

---

## Product rules (settled decisions)

1. **No new td surface.** `TD_WORK_DIR=<root> td show <id>` already addresses any project's store. Sidecar automates "try these directories" — presentation-layer resolution over an owned-by-td capability. No `td locate` command, no index.
2. **No durable artifact.** td databases sync across machines; a locally maintained index would be inconsistent with them. Every lookup reads authoritative synced state.
3. **Local-first.** The current project is tried exactly as today. The search fires only on local miss, inside the click's `tea.Cmd`.
4. **Candidates come from sidecar's own registry**: `config.Projects.List`, resolved through `tdroot.ResolveTDRoot` so worktrees sharing a td database collapse to one candidate, excluding the current project's resolved root, and stat-pruned (skip any candidate whose `.todos/issues.db` does not exist — free, no spawn).
5. **Concurrent fan-out, first success wins.** Bounded workers (`min(6, N)`), overall timeout (~4s), losers cancelled via context. Cross-project duplicate IDs are accepted as extremely rare; first completion wins is declared policy, not an accident.
6. **In-context card + badge.** The card renders from the owning store exactly as a local card would (including subtasks/parent sections and live refresh), with a badge naming the owning project. No jump-to-project action in this plan.
7. **State-free core.** Candidate building and fan-out are pure functions testable without Bubble Tea, adoptable headlessly if another surface ever needs them.
8. **No direct SQLite reads.** Coupling sidecar to td's private schema breaks under pinned-release/synced-DB version skew. The `td` process boundary is the adapter seam.
9. **No feature flag.** The path only activates on an explicit interaction after a local miss.
10. **Never on the startup path.** `BuildCandidates` resolves roots (may invoke `git rev-parse`) and stats files — allowed only inside commands spawned by user action, consistent with the AGENTS.md latency rules.

---

## Design

### A. Pure core: `internal/issueview/crossproject.go`

Kept inside `issueview` to reuse `showIssue` (the td JSON contract stays defined once). Two small types, two functions:

```go
// ProjectRef is one searchable project: display name plus any directory in it.
type ProjectRef struct{ Name, Root string }

// SearchCandidate is a deduped, verified place to look for an issue.
type SearchCandidate struct{ Name, Root string }

// BuildCandidates resolves each ref through tdroot.ResolveTDRoot, skips the
// current project's resolved root, dedupes shared roots (first name wins),
// and drops candidates whose .todos/issues.db is absent. Filesystem reads only.
func BuildCandidates(currentRoot string, refs []ProjectRef) []SearchCandidate

type hit struct {
    Cand SearchCandidate
    Data *Data
}

// findAcrossProjects fans out showIssue(root, id) concurrently, bounded by a
// semaphore (min(6, len)) and a ~4s context timeout; first success wins and
// cancels the rest.
func findAcrossProjects(ctx context.Context, issueID string, cands []SearchCandidate) (*hit, error)
```

Concurrency follows the established semaphore + WaitGroup pattern (`internal/plugins/workspace/worktree.go:69-99`).

### B. Fetch plumbing

- `FetchedMsg` gains `FoundIn *Owner` with `type Owner struct{ Name, Root string }`; nil means local.
- `loadIssue(workDir, issueID string, fallbacks []SearchCandidate)`: local `showIssue` + `attachTree` exactly as today; on miss with candidates present, run `findAcrossProjects`; on hit, `attachTree(hit.Cand.Root, data)` and return the owner alongside the data. Real td errors (corrupt store etc.) surfaced by `extractTdError` still short-circuit — only a genuine "not found" falls through to the search.
- `Model.Load(modelID, workDir, issueID, epoch)` gains a fallbacks parameter; all three call sites updated (`issue_panes.go:416`, `update.go:2860`, `viewers.go:186` and its re-load at `:216`).

### C. Model: ownership + badge

- On a successful cross-project result, `SetResult` adopts the owner: `m.workDir = FoundIn.Root` (so `Refresh` at `live.go:87` and any retry address the owning store directly) and stores `m.foundIn = FoundIn.Name`.
- Badge rendering: `statusHeader` (`render.go:187-194`, placed at `render.go:113`) shows the project name beside the issue ID on the header row when `m.foundIn != ""`; `Heading` (`render.go:30-39`) prefixes `[name] ` so tab strips (`issue_panes.go:554-557`), overview headers (`preview_issue.go:184`), and modal titles (`issue_preview_modal.go:293/:302/:322`) carry the context too. Use existing dimmed/accent style keys; no new theme concepts.
- Total-miss copy becomes `issue %q not found in N project(s)` (current project + searched count), so the failure states what was actually tried.

### D. Live refresh retargeting

A cross-project card whose watcher still points at the requester's store would quietly stop being true. Per host:

- **Workspace** (`live_panes.go:183-258`): watch targets union the pane-level store dirs with the store dir of each open card's effective `view.WorkDir()` (new accessor), deduplicated per directory; the existing cache/refresh loop is otherwise unchanged.
- **App modal** (`issue_preview_live.go:34-70`): when a claimed result carries `FoundIn`, restart the watcher against the owner root (`stopIssuePreviewWatch` + `startIssuePreviewWatch(owner.Root)`), batched with the result application.
- **Overview** (`live_preview.go:104-223`): `syncPreviewDeckProjection` already derives watch roots from `issue.root` (`content_deck.go:180`); `applyPreviewIssueLoaded` (`preview_issue.go:99-111`) sets `issue.root = FoundIn.Root` when present, and the existing resolve/cache path follows automatically.

### E. Persistence across restart

Workspace issue leaves serialize their root today (`decodeIssueLeaf`, `issue_panes.go:532`); add owner name/root fields so a restored cross-project card reopens without re-running the search and keeps its badge. The app modal and overview previews are transient — nothing to persist.

### F. Host wiring

Each host maps `config.Projects.List` to `[]issueview.ProjectRef{{Name, Root: Path}}` at click time and passes it through its load path:

- workspace plugin: from `p.ctx.Config` (`plugin.Context.Config`)
- app preview modal: from `m.cfg` (existing precedent, e.g. `update.go:1313`)
- overview deck: from the app-level config where `Open` is initiated

---

## Work sequence

1. **Pure core** — `crossproject.go`: types, `BuildCandidates`, unit tests over temp dirs covering exclusion, worktree dedupe, and stat pruning.
2. **Fan-out + fetch wiring** — `findAcrossProjects`, `loadIssue`/`Fetch`/`Load` signature updates, `FetchedMsg.Owner`, model adoption of owner root/name; unit tests using a fake `td` shim on PATH (fast and delayed variants to prove first-win and cancellation).
3. **Badge rendering** — `statusHeader` chip, `Heading` prefix, total-miss copy; tests asserting badge appears only when `FoundIn` is set.
4. **Host wiring** ×3 — pass candidates from config; handle `Owner` in each host's result handler.
5. **Live-refresh retargeting** ×3 — per Section D.
6. **Leaf persistence** — encode/decode owner name/root for workspace issue panes.
7. **Verification** — full suite + proof run (below).

---

## Testing & acceptance evidence

- `go build ./...` and `go test ./...` clean.
- Unit tests from steps 1–3 green, including: candidates exclude the current root and collapse worktrees; fan-out returns the first success and cancels losers (delayed shim); model refresh addresses the owning store after adoption; badge/heading render only for cross-project cards.
- Headless proof with `scripts/tmux-drive.sh` (isolated tmux socket **and** isolated state tree — confirm with `paths` first):
  1. Two demo projects A and B, each with a td store; create `td-xxxxx` in B.
  2. In an A shell, print text referencing `td-xxxxx`; click it.
  3. Expected: the card loads B's issue, header/tab shows `[B]`, live edits to B's issue refresh the card.
  4. Negative: remove B's store, repeat — error card reads `not found in 2 projects`.
- `SIDECAR_STARTUP_TRACE=stderr` before/after shows no new work before the first frame.

---

## Rejected alternatives

- **Write-through global ID index in td** (`~/.local/state/td/issues.index.jsonl`): cheap lookups, but td databases sync between machines while the index would be machine-local — inconsistent by construction. Any durable artifact loses for the same reason.
- **New `td locate` command**: unnecessary; `-w`/cwd addressing already covers it, and sidecar owes no new td surface for a browsing concern.
- **Sidecar reading `issues.db` directly**: avoids spawns but couples sidecar to td's private schema; a newer synced DB read by an older pinned release is a real failure mode. Subprocess boundary stays the seam.
