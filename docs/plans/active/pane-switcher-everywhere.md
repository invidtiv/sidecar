# The Pane Switcher Everywhere — `ctrl+n` in Notes, Files, Git and the rest

**Status:** Implemented (M0–M3). `ctrl+n` opens the pane switcher from every deck-eligible plugin — File Browser, Git, Notes, Tasks and td — bound once in `internal/app/pane_switcher.go`, offering pane kinds only, routed through the app deck's own `openAppContentOutcome`. A focused passive leaf inside a plugin deck answers the `n` its context already binds on both Workspaces surfaces. **Tracking:** `td-cced5d`.

One sentence: **the pane switcher should open from wherever you are standing in Sidecar, not only from the Workspaces surfaces.**

Opening context beside your work is not a Workspaces feature — it is how you read anything in Sidecar. You are in the Notes plugin writing up a bug and want the failing file beside the note. You are in Git reading a commit and want the td issue it references. Today both mean leaving the plugin, switching to Workspaces, and opening the pane there, which is exactly the "focus the right pane first" tax the Workspaces half of this just removed.

## What already shipped

The [pane-layout-control](../implemented/pane-layout-control.md) plan delivered the switcher and, in a follow-up, made it reachable from every **content pane** on both Workspaces surfaces:

- The create modal grew into a pane switcher: a vertical kind list (Shell, Worktree, Terminal split, File, Git diff, td issue, Note, one row per configured resource provider), a target picker for the kinds that need one, and a placement row on every pane kind.
- `n` opens it whenever a content pane has focus — `workspace-doc|issue|note|diff|resource` and `global-workspaces-doc|issue|note|diff|resource`. The Diff pane's `n`/`N` next-change moved to `>`/`<` so one key means one thing in every pane.
- `o` opens it from either surface's terminal preview, where `n` belongs to the list's create.
- `internal/keymap`'s parity tests hold both surfaces to the same key in the same contexts.

This plan is the third surface: the ordinary plugins.

## Current state this plan builds on

Verified in the tree at the time of writing. The important thing is how much already exists — this is a **binding and routing** job, not a new pane system.

- **The plugins already have a pane deck.** `internal/app/content_deck.go` gives any eligible plugin an `appContentDeck` — the same `internal/contentpanes` deck, the same `internal/paneframe` chrome, the same `internal/livepanes` refresh the Workspaces surfaces use. `contentDeckEligible` requires a plugin to implement `plugin.ContentLinkProvider` and `plugin.PaneFocusProvider`, and it is gated on the `PluginContentPanes` feature flag.
- **Five plugins are eligible today**: `filebrowser`, `gitstatus`, `notes`, `tasks`, `tdmonitor` (each declares the two interfaces in its `capabilities.go` / `focus.go`). `workspace` is explicitly excluded — it owns its own tree.
- **The app deck already answers `sidecar open`**, including `--at`, through `handleAppContentUIRequest`. So an agent can already put a pane beside a plugin; only the human cannot.
- **The switcher is data-driven and host-agnostic.** `workspacecreate.Open(OpenOpts{...})` takes `Kind`, `FocusKind`, `UseLastKind`, `AllowTerminalSplit`, `ShowNotes`, `Providers`, `ShowProject`. The two Workspaces hosts differ only in those options. `workspacecreate.ResolvePickerTarget` turns a picker answer into the same `uirequest.Target` the CLI produces, which is what keeps the modal an entry point rather than a second implementation.
- **`n` is spoken for in every plugin that has a create.** `notes-list` spends it on `new-note`; `file-browser-search`, `file-browser-content-search`, `git-status-commits`, `git-history-search` and `notes-note-search` all spend it on `next-match`. This is why the entry key here is `ctrl+n` rather than `n`.

## Settled decisions

1. **`ctrl+n` is the entry key in plugin contexts.** Each of these plugins already answers `n` with its own "new" or "next-match", and displacing those would be the drift the Workspaces half deliberately avoided. `ctrl+n` reads as the same intent one modifier out, and it is the key the Workspaces list already uses for its own second create (`new-shell`), so the shape is not novel.

2. **`ctrl+n` is globally `cursor-down`, and that is the real design problem here.** `{Key: "ctrl+n", Command: "cursor-down", Context: "global"}` — plus context-local `cursor-down` bindings in `file-browser-quick-open`, `file-browser-project-search`, `notes-search`, `notes-editor`, `global-workspaces-filter` and `project-switcher`. A context binding shadows the global one, so claiming `ctrl+n` in a plugin's **browse** context is safe, but claiming it in a context where `ctrl+n` already walks a list or moves a cursor is not. The rule this plan adopts: **`ctrl+n` opens the switcher in a plugin's browse and preview contexts, and is left alone in every list-navigation, filter, search and editor context.** Those are precisely the contexts where a live input surface owns the keyboard anyway, which is the same rule the Workspaces content panes follow for `n`.

   Contexts that must keep `ctrl+n` as-is: `notes-editor`, `notes-search`, `file-browser-quick-open`, `file-browser-project-search`, and `notes-preview` — the note preview answers `ctrl+n`/`ctrl+p` as its own cursor motion inside the plugin (`notes.handleEditorPreviewKey`), with nothing in `bindings.go` to show for it, so the rule stands aside there too. Contexts that should gain it: `notes-list`, `file-browser-tree`, `file-browser-preview`, `git-status`, `git-status-commits`, `git-status-diff`, `git-diff`, `git-commit-preview`, and — enumerated in M2 — `tasks-list`, `tasks-detail`, `tasks-response`, `tasks-response-detail`, `td-monitor`, `td-board`, `td-kanban`. Those last two plugins name their own contexts upstream, so the M2 entry records which of them are reachable at all and which are not. The Git list is what `gitstatus.FocusContext()` actually reports — the cursor's row and the focused pane are part of its context, and the `git-history` context that exists in `bindings.go` is never reported at all.

   On a **focused passive content leaf** inside a plugin's deck the key is `n`, not `ctrl+n`: the leaf reports the same `workspace-doc|issue|note|diff|resource` context the two Workspaces surfaces report for the same pane, and it must answer the same key there. The host therefore reads the entry key out of the keymap for the focused context rather than comparing against a constant, which is also what makes a rebound `open-pane` move the key and its footer hint together.

3. **The switcher offers pane kinds only, outside Workspaces.** Shell and Worktree create *workspace rows*, not panes, and a Notes plugin has no workspace to put them in. Terminal split is excluded for a different reason now that `internal/termpanes` exists: the plugin decks are passive `contentpanes` decks with no live-leaf host — no `termpanes` binding, no tty routing, no live-leaf cap enforcement. That adoption is [terminal-splits-and-windowing.md](terminal-splits-and-windowing.md)'s B3, and when it lands the row arrives here by flipping `AllowTerminalSplit`, exactly as it did on the global Sessions browser. In a plugin host the kind list shows File, Git diff, td issue, Note and the configured resource providers. Mechanically this is `AllowTerminalSplit: false` plus a new `PaneKindsOnly` option that drops the Shell and Worktree rows — the same data-driven catalog, one more flag, no second modal. The same option titles the modal **"Open Pane"** rather than "Create Workspace": a heading naming the one act the host cannot perform is the only thing on screen describing what the keystroke did.

4. **Placement stays `auto|right|below`.** The app deck plans through the same `panelayout` policy, including the fourth-pane 2×2 rule, so the placement row means exactly what it means in Workspaces. Explicit cells remain the CLI's precision tool (`sidecar open --at`), unchanged.

5. **The result routes through the app deck's existing open path.** The picker resolves to a `uirequest.Target`; the host maps it to a `contentlink.Ref` and calls `openAppContentOutcome`, which is the same function `handleAppContentUIRequest` already uses. Nothing new decides where a pane goes or what a pane holds.

6. **One binding site per host, as with the Workspaces surfaces.** The Workspaces halves bind the switcher in exactly one place each (`workspace.paneSwitcherKey`, `overview.paneSwitcherKey`). The plugin half should bind it once in `internal/app`, not once per plugin: the deck is the app's, the key routing is the app's, and five plugins each growing their own copy is five places for the rule to drift. A plugin opts in by being deck-eligible, which it already is.

7. **Gated with the deck.** The entry appears only where `features.PluginContentPanes` is on and the plugin is deck-eligible. A key that opens a modal whose result has nowhere to land is worse than no key.

8. **Every deck-eligible plugin gets the entry — `tasks` and `tdmonitor` included, and the File Browser first among them.** Deck eligibility is the opt-in: a plugin that declares the interfaces and holds a deck offers the key, with no per-plugin intent question on top. A pane beside a task list is not meaningless — it is the td issue the task names, the diff the work produced. And the File Browser is not a strange host but the furthest along: its deck integration is the most complete of the five, and "open the td issue this file's TODO names" beside the preview is exactly the reading pattern this plan exists for. Per-host kind filtering stays M2's escape hatch if one catalog proves wrong in use, not a precondition.

## Unresolved questions

- ~~**`ctrl+n` in a terminal.** Any plugin context that forwards keys to a live PTY must not claim it — `ctrl+n` is a real control character. Audit before binding.~~ **Answered in M3: nothing claims it.** `tty.MapKeyToTmux` encodes `ctrl+n` as `C-n` and sends it straight to the pane. The tty layer's own chords are `ctrl+\` (exit), `ctrl+]` (attach), `alt+c` (copy), `alt+v` (paste), `ctrl+a` (select all) and the platform copy chord; its scrollback set is the arrow/page keys plus the `j/k/g/G/ctrl+d/ctrl+u` pager aliases, and while a pane is live the arrows need shift. The only two host `OnKey` hooks (`workspace.interactiveKey`, `overview.previewTerminalKey`) claim terminal search and `ctrl+t` between them. `internal/termpanes` and `internal/inlineedit` handle no keys at all. Above all of it, `workspace-interactive`, `file-browser-inline-edit`, `notes-inline-edit` and `workspace-doc-edit` forward every key two rungs before the switcher's is reached. The full sweep is recorded in `.claude/skills/keyboard-shortcuts/SKILL.md`.

## Work sequence

### M0 — Close the File-target mapping — **implemented**

The deck side of the File question is answered in the tree: `contentpanes` decks hold Document leaves on both Workspaces surfaces, the app deck already resolves `contentlink.KindFile` in its link path, and app-deck persistence exists (`persistAppContentDeck` through `contentpanes.State`/`Decode`, whose unknown-kind collapse is the degradation rule). The picker already resolves `KindFile` to `uirequest.TargetKindFile`, which the Workspaces hosts handle. The one genuine gap is `appContentKindForTarget`: the app host's target mapping lists only Issue, Note, Diff and Resource.

- ~~Add the `TargetKindFile → panelayout.Document` case and whatever value resolution it needs, mirroring the Workspaces hosts' handling.~~ Done: the per-kind body of `handleAppContentUIRequest` became `(*appContentDeck).contentRefForTarget`, which now carries File and resolves it through `terminallink.ResolveFile` as the deck's link path already did; `appContentKindForTarget` gained the `panelayout.Document` case for explicit `--at` cells.

**Behaviour change worth knowing about:** `sidecar open <path>` while a deck-eligible plugin is focused now lands in that plugin's deck instead of falling through to the workspace plugin, and an unresolvable path is declined with a reason rather than falling through. That is the intent — it matches what Issue, Note, Diff and Resource already did — but it is a routing change, not only a gap-fill.

### M1 — The host entry — **implemented**

- ~~`PaneKindsOnly` option in `workspacecreate` (drops Shell and Worktree rows); the kind catalog stays one table.~~ Done: one more `rowOpts` flag against the one `kindCatalog`. `Open` also stopped writing package-level `lastKind` when the requested kind was not offered — a `PaneKindsOnly` open would otherwise have silently moved the Workspaces list off Shell.
- ~~One binding site in `internal/app`~~ Done: `internal/app/pane_switcher.go`. The rung sits below every surface that types, immediately after `handleAppContentKey` and before `pluginClaimsKey`.
- ~~Keymap bindings for the browse/preview contexts named in decision 2~~ Done. Decision 2's *enumeration* was corrected against the tree while doing it — see the note in that decision.
- ~~`Commands()` entries so the footer and `?` find it.~~ Done: `paneSwitcherCommands()`, merged in `Model.footerHints()` outside the per-surface switch so a focused leaf gets it too, plus `paneSwitcherModalCommands()` describing the modal's own keys while it is up.
- **Proof:** `internal/keymap/pane_switcher_parity_test.go`, plus `internal/app/pane_switcher_test.go` driving the real key ladder.

### M2 — Kind filters and proofs across all five hosts — **implemented**

- ~~Enumerate the `tasks` and `tdmonitor` browse contexts and bind them per decision 2.~~ Done. Neither plugin mints its own context names, so the enumeration had to come from the modules: Tasks reports `tasksui.FocusContext` verbatim and td runs its own name through `monitor/keymap.ContextToSidecar`.
  - **Tasks:** `tasks-list`, `tasks-detail`, `tasks-response`, `tasks-response-detail` — exactly its four root contexts. Everything else Tasks reports is an overlay whose keys `BlocksGlobalKeys` hands to the tab at precedence level 2, above the switcher's rung, so "browse or preview" and "a key can reach the host here" turn out to be the same set.
  - **td:** `td-monitor`, `td-board`, `td-kanban` — the three views where td browses its own issues with nothing overlaid.
  - **Deliberately not bound:** `td-modal` and its sub-focus states (`td-epic-tasks`, `td-parent-epic`, `td-blocked-by-focused`, `td-blocks-focused`). `tdmonitor.BlocksGlobalKeys` claims every key in that context for the embedded td model, so a binding would never fire. This is the one genuinely wanted pane the entry does not reach — an issue detail is exactly where you want the file beside it — and closing the gap means changing td-modal's key routing, which is its own decision rather than a binding.
  - **Also not bound:** td's own overlays (`td-stats`, `td-handoffs`, `td-notes`, `td-help`, `td-tdq-help`, `td-board-picker`, `td-getting-started`, the sync prompt), which are not browse surfaces; and the input contexts `td-search`, `td-form`, `td-board-editor`, `td-confirm`, `td-close-confirm`. `td-global` is never reported at all — it is td's fallthrough binding table, not a context anyone stands in.
  - Because the strings are someone else's constants, `tasks.TestBrowseContextsCarryThePaneSwitcherEntry` and `tdmonitor.TestBrowseContextsCarryThePaneSwitcherEntry` derive them from the upstream packages, so a rename in either module fails a test here rather than silently unbinding a key.
- ~~Per-host kind filtering if M1's single catalog proves wrong in use.~~ **Not needed.** The one catalog held on all five hosts. What the proof run *did* turn up is that the modal was still titled "Create Workspace" over a list with no Shell and no Worktree row in it; it now reads **"Open Pane"** when `PaneKindsOnly` is set. That is one option on the same form, not a second modal.
- **Proof:** isolated `tmux-drive.sh` run (`paths` confirmed first; both axes private). `ctrl+n` opened the switcher with the pane-kinds-only catalog from **td** (`td-board`), **Notes** (`notes-list`), **Files** (`file-browser-tree`) and **Git** (`git-status`), and the td run was carried through end to end — kind list → td issue picker → `td-bcfe53` open in a pane beside the board. Tasks is not configured in this project, so its host is covered by `TestPaneSwitcherOpensFromEveryDeckEligiblePlugin/tasks` driving the real ladder instead.

### M3 — Audit and document — **implemented**

- ~~Sweep every context that forwards to a PTY and confirm none claims `ctrl+n`.~~ Done; result recorded under "Unresolved questions" above and in the skill.
- ~~`.claude/skills/keyboard-shortcuts/SKILL.md`~~ Done: the plugin half of the "reachable from every pane" rule, the full `ctrl+n` assignment table (every context that binds it and to what, plus the ones deliberately without a row and why each), and new Tasks / td sections noting that their context names are minted upstream.

## Acceptance evidence

- ~~Keymap parity test covering every deck-eligible plugin's browse context, and asserting the navigation/editor contexts are untouched.~~ `internal/keymap/pane_switcher_parity_test.go`.
- ~~Picker-to-target unit tests proving a plugin host resolves each kind to the same target shape the CLI and the Workspaces hosts produce.~~ `TestPaneSwitcherAnswersResolveAndOpenLikeTheCLI`.
- ~~`tmux-drive.sh` transcripts for the M2 proofs, fully isolated.~~ See M2.
- ~~A regression test that the entry is absent when `PluginContentPanes` is off.~~ `TestPaneSwitcherEntryIsGatedOnTheDeck/feature off`.
