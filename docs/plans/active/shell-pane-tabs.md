# Shell pane tabs — tabbed terminals inside a shell leaf

**Status:** Proposed **Created:** 2026-08-30 **Tracking:** `td-70f193`

One sentence: **a Shell pane leaf should hold a strip of terminal tabs — each tab its own tmux session, only the active one attached — so a user can keep several shells in one pane without spending a split, in both the project workspace and the global Sessions browser.**

Today every passive pane kind (documents, issues, notes, diffs, resources) supports tabs, and shells support only splits: each additional terminal is a new `panelayout.Shell` leaf, and `LiveLeafCap = 2` (`internal/panelayout/panelayout.go:291`) means the second one is the last. This plan adds the tab axis to shell leaves without touching that cap, without adding a third tab model or renderer, and without weakening the codebase-wide invariant that a terminal is one tmux session.

## Relationship to other plans

- [terminal-splits-and-windowing.md](../implemented/terminal-splits-and-windowing.md) established Shell leaves as duplicable peers in the pane tree with the 2-live-leaf cap, and deliberately deferred background-terminal demotion. This plan is the continuation on the other axis: splits answer "where on screen", tabs answer "which terminal in this box". Its unfinished A1 residue (the `terminal_panel.go` bool-keyed state in the project workspace plugin) becomes this plan's M0 prerequisite.
- [workspace-doc-pane-tabs.md](../implemented/workspace-doc-pane-tabs.md) and [workspace-issue-pane-tabs.md](../implemented/workspace-issue-pane-tabs.md) define the tab conventions this plan inherits: the pane tree is unaware of tabs, the header row is the tab strip, hosts own the chrome, and — the rule stated in the issue-tabs plan — new tab surfaces must not become another tab model or renderer. Shell tabs reuse `internal/tabs` (`Group`, `LayoutStrip`, fit, hover) directly.
- [pane-layout-control.md](../implemented/pane-layout-control.md) decision 3 ("multiple targets in one pane are tabs") extends to shell panes in M5: a layout spec listing several sessions for one shell pane materializes them as tabs.
- [pane-repositioning.md](../implemented/pane-repositioning.md) moves leaves with their `termpanes.Deck` state intact. Tabs ride the leaf, so a repositioned shell leaf carries its whole tab set; M4 verifies this against the graft replay paths.
- [agent-shell-create-cli.md](../implemented/agent-shell-create-cli.md) shipped `sidecar create shell --split auto|right|below` and `--tab` (which means "workspace shell row", not a UI tab). M5 adds the placement value `--split tab` rather than repurposing `--tab`.
- [workspace-windowing-system.md](../deprecated/workspace-windowing-system.md) raised tab promotion ("a tab becomes its own leaf") as an open question. It stays open here; see Out of scope.

## Settled decisions

1. **A shell tab is a whole tmux session, never a tmux window.** The shell = session invariant runs through everything: `shellliveness.ProbeSession` compares session names, `tty.Target{Session, Pane}` attaches per session, `workspaceops.PaneID` (`internal/workspaceops/shell.go:198`) takes the first pane of a session, `panecodec` Live records carry a session name, `layoutapply` specs address sessions, the CLI targets sessions, and `SIDECAR_SHELL`/`SIDECAR_SHELL_NAME` are injected per session. Window-per-tab would force `(session, window)` addressing through every one of those seams for no user-visible gain. Session-per-tab means every existing per-session mechanism works on a tab unchanged.

2. **Tab state lives on the live-leaf side (`internal/termpanes`), not in `contentpanes`.** Shell leaves are foreign to the content deck by construction — `contentpanes.newViewer` panics on non-content kinds (`internal/contentpanes/viewers.go:56`), `Decode` drops shell nodes, and hosts re-splice shells from Live records (`internal/panecodec/panecodec.go:16-18`). That boundary stays. `termpanes.Leaf` gains a tab list built on `tabs.Group[*termpanes.Tab]`, and rendering uses `tabs.LayoutStrip` + `styles.RenderTab` — the same primitives every other strip uses, so this is a sixth wrapper around one model, not a second model.

3. **Only the active tab is attached; the leaf's cost model is unchanged.** One `tty.Model` control-mode subscription per live leaf, retargeted on tab switch. Background tabs hold no subscription and receive no resizes; their processes keep running because they are detached tmux sessions, which is exactly what tmux is for. `LiveLeafCap = 2` continues to count leaves — tabs never change how many terminals are painted per frame.

4. **Tabs apply to `panelayout.Shell` leaves only, in both surfaces.** The Primary leaf mirrors the selected workspace's own session (overview) or the workspace surface (project plugin); giving it tabs would conflate "which workspace am I looking at" with "which terminal did I open". Selecting a different workspace already changes the primary target; tabs belong on the shells the user opened deliberately.

5. **Split-session naming becomes an allocator.** `termpanes.SessionName(selector)` is a pure function of the owner (`internal/termpanes/session.go:24`), which is exactly why a leaf can hold only one session today. The first tab keeps the derived name `sidecar-tp-<selector>` for continuity with existing state; subsequent tabs allocate `sidecar-tp-<selector>-2`, `-3`, … skipping names that already exist. Tab count per leaf is capped at `termpanes.MaxTabs = 8` (precedent: `resourceview.MaxTabs = 16`; shells are heavier, so the bound is lower). Refusal reuses the strip's status-line pattern, never silent truncation.

6. **Close kills, hide parks, death closes one tab.** Explicitly closing a tab (`x`, close hover, modal) kills its tmux session after the same confirmation shape the shell leaf uses today; closing the last tab is closing the leaf. Hiding the leaf (ctrl+t toggle, off-surface release) parks the entire tab set and kills nothing, as today. A liveness verdict of Gone on a background or active tab closes that tab only, leaving the leaf up while other tabs survive; verdict Unknown never closes anything (`internal/shellliveness/liveness.go` rules apply per tab).

7. **Persistence rides the Live record, extended to a list.** `state.PaneLayoutJSON` gains `ShellTabs []PaneShellTabJSON{Session, Name}` and `ShellActive int` alongside the existing flat `Session`/`Name`, which remain populated with the active tab for backward and forward compatibility. `panecodec` encodes the tab list on the shell node's Live record and decodes a legacy single-session record as a one-tab list. `contentpanes` continues to drop shell nodes; the host re-splice (`internal/plugins/workspace/content_deck.go:600-618`, `internal/overview/terminal_split.go:255`) carries the tab set. Restore probes each tab's session with `shellliveness` and drops Gone tabs before attach.

8. **Parity is enforced by shared code, per the house rule.** The model and allocator live in `internal/termpanes`; the strip geometry and hit ordering live in `internal/paneframe` (the `RegionSink` grows a shell-tab path); each surface binds in its one `pane_host.go` file. Keymap gains `{`/`}` cycle, `x` close, and `t` new-tab in the shell-leaf contexts of both surfaces (`t` follows the `file-browser-tree` new-tab precedent); interactive mode is untouched — keys forward to the terminal, and tab switching from inside interactive mode retargets input to the new active tab the way `switchPreviewInteractive` already retargets across leaves.

9. **The agent surface is `--split tab` plus spec support, and the overview starts honoring placement requests.** `sidecar create shell --split tab` joins the current shell leaf as a new tab (creating the leaf if absent); `--tab` keeps its shipped meaning. `layoutapply` specs accept multiple sessions on one shell pane and materialize them as tabs, closing the pane-layout-control decision-3 gap for live kinds. The overview currently ignores `--split` requests entirely (`internal/overview/ui_requests.go:183-185`) — M5 fixes that standing parity gap for both `--split` and `--split tab` rather than extending it.

10. **The work ships behind the `shell_tabs` feature flag, default off until M6's proofs pass, then default on.** Same rollout shape as `pane_move`.

## Design

### Model

`internal/termpanes` gains:

- `Tab{Session, Name string}` — identity only; the live `tty.Model`, scrollback, and selection state stay on the leaf and are torn down/rebuilt on switch (a detached session re-fills history from tmux, which is the same restore path used when a leaf comes back on screen).
- `Leaf.Tabs tabs.Group[*Tab]` with the leaf's current single session becoming tab 0 at construction, so every existing call site that asks the leaf for "the session" reads through `ActiveTab()` and compiles against one accessor.
- `AllocSessionName(selector string, taken func(string) bool) string` — the allocator from decision 5.
- `MaxTabs = 8` and a `CapMessage`-style refusal string.

The project workspace plugin reaches this model only after M0 removes the bool-keyed pair (`terminalPane(termPanel bool)`, `primaryTermPane`/`requireShellTermPane` in `internal/plugins/workspace/terminal_panes.go`) in favor of the leaf-ID-keyed shape the overview already has (`internal/overview/terminal_panes.go:56`).

### Rendering and input

The shell leaf's header today is self-drawn chips + hints (`internal/plugins/workspace/content.go:139-142`, `internal/overview/preview_tabs.go:184-196`). It becomes: tab strip (via `tabs.LayoutStrip`, labels from tab names fitted with `tabs.FitEnd`) on the left, existing status chips collapsed into the reserved right-hand controls from `panereposition.ReserveHeader`. With one tab and the flag off, the header renders exactly as today.

Hit regions: `paneframe.RegionSink` gains the shell-tab registration alongside the existing `Tabs` path, registered in the frame's one ordering site (`internal/paneframe/compose.go:268-298`); each surface adds one case in its `pane_host.go` region sink (`internal/plugins/workspace/pane_host.go`, `internal/overview/pane_host.go`) with region IDs `shell-tab` / `global-preview-shell-tab`, close-hover included.

Keys: new `workspace-shell` context and additions to `global-workspaces-terminal` in `internal/keymap/bindings.go` — `{` prev-tab, `}` next-tab, `x` close-tab (confirm), `t` new-tab. These bind only when the shell leaf is focused and not interactive; interactive mode continues to forward everything to the terminal.

### Switching

Tab switch = detach the leaf's `tty.Model` from the old session, `Enter(newSession, firstPane)`, resize to the leaf's current box, re-arm capture. If the leaf was interactive, `interactiveState` (project) / `leaf.Interactive` (overview) retargets to the new session in the same step, with the stale-target guard the cross-leaf switch already uses. Agent-activity capture (`captureShellSessionByName`) keys per session and needs no change; background tabs simply aren't captured for the on-screen pane.

### Persistence wire format

```json
{ "kind": "shell", "session": "sidecar-tp-foo", "name": "build",
  "shellTabs": [ {"session": "sidecar-tp-foo", "name": "build"},
                 {"session": "sidecar-tp-foo-2", "name": "logs"} ],
  "shellActive": 0 }
```

Legacy records (no `shellTabs`) decode as a one-tab list. `applyLive` (`internal/panecodec/panecodec.go:389`) matches the record to the node as today and hands the host the full list.

## Work sequence

### M0 — Converge the project workspace on leaf-ID-keyed terminal state

Retire `termPanel bool` parameterization: `terminal_panel.go`'s surviving state, `terminalPane(bool)`, `terminalLeafID(bool)`, `shellContent{p: p}`'s ID-less singleton, and `rebindTerminalPaneTree`'s hard-coded `{primary, shell}` rebuild move to the overview's shape — `termpanes.Leaf` keyed by leaf ID, `shellContent{leafID}`. No behavior change; this is the terminal-splits A1 residue and the prerequisite for tabs having one home. **Proof:** existing workspace + overview test suites green; isolated `tmux-drive.sh` run showing split open/close/interactive unchanged in both surfaces.

### M1 — Tab model in termpanes

`Tab`, `Leaf.Tabs`, `ActiveTab()`, allocator, `MaxTabs`, switch mechanics (detach/enter/resize/recapture), behind `shell_tabs`. Unit tests for the allocator (collision skip, cap refusal) and group semantics reuse `internal/tabs` tests as the contract. **Proof:** `go test ./internal/termpanes/...` covering alloc, switch retarget, cap.

### M2 — Strip rendering and input, both surfaces

Header strip + chips-into-controls, `paneframe` region path, `pane_host.go` bindings, keymap contexts, close-hover, click and `{`/`}`/`x`/`t` handling, close-confirm modal, interactive retarget on switch. **Proof:** isolated `tmux-drive.sh` run in each surface — create two tabs, switch by key and by click, close by `x` and by hover ×, confirm the header renders identically to today with one tab.

### M3 — Persistence and restore

`PaneShellTabJSON`, `panecodec` encode/decode with legacy single-session fallback, host re-splice carrying the tab set, restore-time liveness filtering of Gone tabs, `layoutreport.leafInfo` listing shell tab names. **Proof:** round-trip unit tests in `panecodec` including a legacy-record fixture; `tmux-drive.sh` restart run where two tabs survive a full sidecar restart and a killed background session is dropped cleanly.

### M4 — Lifecycle hardening

Per-tab liveness reaping (Gone closes the tab, Unknown never does), explicit-close session kill per tab, hide-parks-all, off-surface release, per-tab rename via the clickable title, reposition/graft replay verified to move the whole tab set (`CaptureLeafGrafts`/`ApplyLeafGraft` extended if they carry single sessions). Audit that no orphan `sidecar-tp-*-N` sessions survive any close path. **Proof:** liveness unit tests per verdict; `tmux-drive.sh` run that kills a background tab's session out-of-band and shows the tab close without the leaf flinching; `tmux list-sessions` empty of `sidecar-tp-*` after explicit closes.

### M5 — Agent surface and parity gap

`sidecar create shell --split tab`, `layoutapply` spec accepting a session list per shell pane, overview honoring `--split` placement requests (removing the `ui_requests.go:183` early return), `docs/reference/cli.md` regenerated. **Proof:** CLI integration test creating a tab from inside a Sidecar shell in each surface; `sidecar layout get --json` showing the tab list; spec-apply test materializing a two-tab shell pane.

### M6 — Flag flip and proofs

Full isolated proof matrix (both surfaces × create/switch/close/restart/reposition), demo path via `./scripts/demo.sh`, `shell_tabs` default on, changelog entry. **Proof:** the acceptance evidence table below filled in with td/commit citations.

## Acceptance evidence

| Milestone | Status | Evidence |
| --- | --- | --- |
| M0 — leaf-ID convergence | Not started | |
| M1 — tab model | Not started | |
| M2 — strip + input | Not started | |
| M3 — persistence | Not started | |
| M4 — lifecycle | Not started | |
| M5 — agent surface | Not started | |
| M6 — flag on | Not started | |

## Open questions

- **Adopting managed shells as tabs.** A workspace shell (`sidecar-sh-*`, a `shells.json` row) could be attached into a tab instead of allocating a fresh `sidecar-tp-*` session — "pull that shell into this pane". Session-per-tab keeps this possible with zero model change, but the ownership story (who reaps it, does the sidebar row remain) is unresolved, so v1 tabs are always freshly allocated split sessions.
- **Sidebar badge.** The `⊞n` badge counts live leaves today; whether it should count tabs, or grow a second affordance, is a presentation decision deferred to M2 review.
- **Default tab names.** "Tab N" vs. seeding from `pane_current_command` the way sidebar rows surface activity; M2 starts with ordinal names and per-tab rename.

## Out of scope

- Tab promotion (tab → own leaf) and demotion (leaf → tab of a neighbor), the open question inherited from the deprecated windowing plan. The model here doesn't preclude it: promotion is "remove tab, open leaf with its session", demotion the reverse.
- Tabs on the Primary leaf (decision 4).
- Raising `LiveLeafCap` or any change to split placement.
- Duplicating passive pane kinds (pane-layout-control decision 12 still stands).
