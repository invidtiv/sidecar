# Overnight: work the remaining global-workspaces tickets

You are the **orchestrator**. Stay in this session. Do not implement the stories yourself unless a change is a few lines you have already diagnosed. Delegate implementation. **Always delegate review.** Something other than the author must review every story before it closes.

This is an unattended all-nighter. Work until the tickets below are closed or you are genuinely blocked. Re-read `td show` / `td tree` before each wave — the user may have added stories.

Repo: this checkout. Branch: `global-workspace`.

---

## Bootstrap

```bash
sidecar shell rename "overnight gw tickets"
td usage --new-session -q
```

Read:

- `.claude/skills/orchestrate/SKILL.md` (the one rule: independent review before close)
- `.claude/skills/pragmatic-engineering/SKILL.md`
- `Agents.md` (td close flags, no default-tmux murder, plugin height, no plugin footers, isolated proof)
- This file
- `td show td-08a423` and `td show td-0b95b1` (and children)

Do **not** `td ws tag` the whole set. That auto-starts every issue as *you* and makes you implementer-of-record. Implementers `td start` under their own `TD_CONTEXT_ID`.

Give every sub-agent:

- Role (implementer / reviewer)
- Ticket id(s)
- `export TD_CONTEXT_ID=impl-<id>` or `review-<id>`
- `td start` / `td log` / `td review` (implementer) or `td approve` (reviewer, own context)
- “If compacted: `td context <id>` and continue.”
- Focused test command they own. They do **not** run `go test ./...`.
- Commit format: `feat|fix|chore: <summary> (td-XXXXXX)`

---

## Hard rules

- **Never** stop, restart, kill, or replace the machine’s default tmux server. Proof and tests use a private socket. `tmux-drive.sh paths` before any proof run — nothing may resolve under `~/.local/state/sidecar` or `~/.config/sidecar`. Prefer `SIDECAR_ISOLATED_STATE=1`.
- Do not send keys into the user’s live Sidecar sessions.
- Do not reopen closed epic **td-92f525**. That contract is shipped. Do not regress it (see Locked product, below).
- Do not implement **td-de1ab2** (shared terminal layer leftover).
- Do not open the td issue-preview **modal** as a feature. Sidecar is leaving modals for splits.
- Do not invent create/merge/push/stage in global Workspaces.
- Do not fold unrelated tickets into one PR just to look busy. Batch only when files and design are the same.
- One review + one fix cycle, then land P0s and file the rest. Do not loop.
- A finding without a reproduction is not a finding.
- Prefer a reviewer with its own `TD_CONTEXT_ID` so `td approve` is mechanically independent.

---

## Locked product (do not regress)

From td-92f525, already on this branch:

- Global Workspaces has **two keyboard states**: list (browse) and interactive (type). No watched-preview mode. `l`/`→` do not focus a watched preview. `\` is layout only (list keys stay).
- Enter / click-in-pane / `E` start typing. Dead row stays put. Double-click still opens the owning project.
- `i` is find-TD-task (not interactive). `q` is quit modal. `K` / logo / `esc` on the list leave global.
- `ctrl+\` / `esc esc` land on the **list**. `ctrl+]` stays project-only.
- Idle / no-session rows hidden by default; fly-out can show them.
- Last global tab persists. Capture-age text is gone.
- Topic worktrees have Output / Diff / Task. Kind glyphs on the Agents board are `⑂` worktree / `❯` shell — the list work must reuse those, not invent a third pair.

---

## Sequence

Two tracks. They barely share files. Prefer Track A first if you are one orchestrator; start Track B after Wave A1 (or in parallel with a worktree once A1 is merged).

**td-4819be is last on Track A.** It is the largest rewrite. Do not start it until the list row exists.

### Track A — td-08a423 Polish global Workspaces identity and list

#### Wave A1 — small, unblocks daily use (one implementer or three tiny ones; one review)

| # | ID | What |
|---|---|---|
| 1 | **td-4e73d6** | Enter on a View fly-out sort applies **and closes**. No tab-to-Done. |
| 2 | **td-60f563** | Worktree Output is Ambiguous when `sidecar-tp-*` / `sidecar-edit-*` share the cwd. Prefer `sidecar-ws-*`, ignore chrome. Do not guess among leftover rivals. **Do not touch the user’s tmux sessions.** |
| 3 | **td-0842de** | Make global vs project obvious in the header. Steel thread: loud `Overview` breadcrumb pill (clickable, same toggle as the logo). Mode must survive after the clock is dropped. A clock-adjacent chip is optional and must not be the only signal. Header tint in global is welcome. |

Review A1 as one batch if they land together.

#### Wave A2 — list scannability (serialize — `internal/workspacelist`)

| # | ID | What |
|---|---|---|
| 4 | **td-47d267** | Two-line global row: `{project} {name}` … `{age}`; line 2 `{kind} {agent}`. Kind = **⑂** worktree, **❯** shell (`kindGlyph`). Drop status text. Marker stays in the gutter. Do **not** force project-name-on-line-1 onto the project plugin. |
| 5 | **td-a8644b** | Project sort: per-project headings. Recent sort: time buckets (New / Today / This week / Older; omit empty). Activity unchanged. Name sort may stay flat. |
| 6 | **td-90ebad** | Pin shells/worktrees to the top (`p` on the list). Persist. Not duplicated below. Pins sit **above** the sort sections. While typing, `p` goes to the pane. |

One implementer for 4→5→6 if possible (same package). Review the three together.

#### Wave A3 — actions

| # | ID | What |
|---|---|---|
| 7 | **td-bd99b9** | `R` renames a selected **shell** in global, same modal/`shellstate` as project, owning project’s manifest. Worktrees ignore `R`. |
| 8 | **td-e489e7** | Global **shells** get Output + Diff (not Task). Diff is `workspacediff` on `ProjectRoot`. |
| 9 | **td-9e4d71** | Chip + **`O`** (`open-in-git`) jumps to project Git plugin. Worktree → its path; shell → `ProjectRoot`. Sequence, not Batch. `O` only when list-focused. While typing, `O` is a letter. Can start on worktrees in parallel with 8. |

#### Wave A4 — last

| ID | What |
|---|---|
| **td-4819be** | Nest child shells under parent worktrees in the **project** sidebar. Attach by session name + WorkDir, not `panesForPath`. Inherit td-47d267 row chrome. |

### Track B — td-0b95b1 Terminal links (foundation for splits)

| # | ID | What |
|---|---|---|
| 1 | **td-01f331** | Extract scanner (`url` / `file` / `issue`) out of `package workspace`. Fold **td-77dd35** into this PR: emit `issue` spans for `td-<hex>`, **no activation**. |
| 2 | **td-6699fc** | Any regular file on this machine is previewable (absolute, `~/`, `.go`, bare path). Safety: exists, regular file, no control chars, no network. Open in the **doc pane**, do not `SwitchWorktree`. Project can ship this immediately. |
| 3 | **td-69b8c1** | Global file clicks. The markdown **doc pane already exists in project Workspaces**. This is wiring that same `docview` preview to global, for shells and worktrees. Not a second viewer. Not td-epic preview. Do this **after** the scanner *and* loosened resolve so global does not ship the old in-project-md-only rules. |

### Side — anytime, isolated

| ID | What |
|---|---|
| **td-af01fa** | Doc pane: **`m`** toggles rendered ↔ raw. Header chip shows current mode **and** clicks to toggle. Drop `r` as the render key if you can. File browser already uses `m`. Parent epic is td-98028c. |

---

## Review pattern

After each wave (not after every file save):

1. Implementer: `td review <ids>`, focused tests in the log, commit on the branch.
2. You: spawn a reviewer with the **contract** (`td show` acceptance), the diff range, and recorded test commands. Tell it to **falsify**.
3. Reviewer (own `TD_CONTEXT_ID`): Clean → `td approve <id> --reason "..."`. Otherwise file a finding with a reproduction; do not approve.
4. Resume the **same** implementer for the fix. Re-review. Then move on.

Batch reviews for a wave. Name every story and the commit range in the reviewer prompt.

---

## Tests the orchestrator owns

After each wave is merged to this checkout, run the packages that wave touched (not the world). Before you stop for the night / call the epics done:

```bash
go test ./internal/overview ./internal/app ./internal/keymap ./internal/state ./internal/workspacelist ./internal/plugins/workspace ./internal/workspacediff
# plus any new package (e.g. terminallink)
```

`gofmt` the files you (they) touched. Do not “fix” unrelated unformatted files.

---

## If you run out of night

Stop at a wave boundary. `td handoff` the current epic(s):

```bash
td handoff td-08a423 --done "..." --remaining "..." --decision "..." --uncertain "..."
td handoff td-0b95b1 --done "..." --remaining "..." --decision "..." --uncertain "..."
```

Honest `--done`. Leave the tree compiling. Do not leave half-applied sort/header/pin changes.

---

## Done means

- Every ticket listed above is **closed** with an independent review, or explicitly handed off with a reason.
- Locked product from td-92f525 still holds (spot-check Enter / `q` / `i` / `\` / two-state tests).
- No new pile of “follow-up” tickets unless something cannot ship in this run. Prefer fix-in-place.

Start at Wave A1. Go.
