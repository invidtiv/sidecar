# The Pane Switcher Everywhere — `ctrl+n` in Notes, Files, Git and the rest

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

   Contexts that must keep `ctrl+n` as-is: `notes-editor`, `notes-search`, `file-browser-quick-open`, `file-browser-project-search`. Contexts that should gain it: `notes-list`, `notes-preview`, `file-browser-tree`, `file-browser-preview`, `git-status`, `git-history`, `git-diff`, `git-commit-preview`, and the `tasks` / `tdmonitor` equivalents once their contexts are enumerated.

3. **The switcher offers pane kinds only, outside Workspaces.** Shell, Worktree and Terminal split create *workspace rows*, not panes; a Notes plugin has no workspace to put them in and no pane tree to split a live terminal into. In a plugin host the kind list shows File, Git diff, td issue, Note and the configured resource providers. Mechanically this is `AllowTerminalSplit: false` plus a new `PaneKindsOnly` option that drops the Shell and Worktree rows — the same data-driven catalog, one more flag, no second modal.

4. **Placement stays `auto|right|below`.** The app deck plans through the same `panelayout` policy, including the fourth-pane 2×2 rule, so the placement row means exactly what it means in Workspaces. Explicit cells remain the CLI's precision tool (`sidecar open --at`), unchanged.

5. **The result routes through the app deck's existing open path.** The picker resolves to a `uirequest.Target`; the host maps it to a `contentlink.Ref` and calls `openAppContentOutcome`, which is the same function `handleAppContentUIRequest` already uses. Nothing new decides where a pane goes or what a pane holds.

6. **One binding site per host, as with the Workspaces surfaces.** The Workspaces halves bind the switcher in exactly one place each (`workspace.paneSwitcherKey`, `overview.paneSwitcherKey`). The plugin half should bind it once in `internal/app`, not once per plugin: the deck is the app's, the key routing is the app's, and five plugins each growing their own copy is five places for the rule to drift. A plugin opts in by being deck-eligible, which it already is.

7. **Gated with the deck.** The entry appears only where `features.PluginContentPanes` is on and the plugin is deck-eligible. A key that opens a modal whose result has nowhere to land is worse than no key.

## Unresolved questions

- **Does the app deck open File panes?** Link resolution handles `contentlink.KindFile`, but `appContentKindForTarget` — the `--at` mapping — lists only Issue, Note, Diff and Resource. If the deck genuinely cannot hold a document leaf, the File row must be hidden in plugin hosts, and that is a bigger gap than this plan: File is the row users will reach for first from the Git and Notes plugins. **Settle this before anything else; it may promote itself to M1.**
- **`tasks` and `tdmonitor`**: both are deck-eligible, but neither was considered when the switcher was designed. Do they want the entry, or is a pane beside a task list meaningless? Decide per plugin rather than assuming the interface implies the intent.
- **Does the File Browser want it at all?** Its whole surface is already a file list with a preview. "Open a file pane" there may be a strange thing to offer, where "open the td issue this file's TODO names" is not. Possibly a per-host kind filter rather than one catalog.
- **`ctrl+n` in a terminal.** Any plugin context that forwards keys to a live PTY must not claim it — `ctrl+n` is a real control character. Audit before binding.

## Work sequence

### M0 — Settle the File-pane question

- Determine whether `contentpanes.Deck` in an `appContentDeck` can hold a Document leaf, end to end: open, tab, persist, restore, live-refresh.
- If it cannot, that is this plan's real first milestone and the rest waits on it.
- **Proof:** `sidecar open <path>` against a plugin surface with the deck flag on, in an isolated `tmux-drive.sh` run; the pane is there after a relaunch.

### M1 — The host entry

- `PaneKindsOnly` option in `workspacecreate` (drops Shell and Worktree rows); the kind catalog stays one table.
- One binding site in `internal/app`: `ctrl+n` opens the switcher when the focused plugin is deck-eligible and the flag is on; the picker's answer routes to `openAppContentOutcome`.
- Keymap bindings for the browse/preview contexts named in decision 2, and **not** for the navigation/editor contexts named beside them.
- `Commands()` entries so the footer and `?` find it.
- **Proof:** a keymap parity test in the shape of `pane_switcher_parity_test.go` — every deck-eligible plugin's browse context binds `open-pane` to `ctrl+n`, and no list-navigation context does.

### M2 — Per-plugin opt-in and kind filters

- Decide `tasks` and `tdmonitor` (unresolved above); decide the File Browser's kind list.
- Per-host kind filtering if M1's single catalog proves wrong in use.
- **Proof:** isolated `tmux-drive.sh` snapshots — from Notes, `ctrl+n` → td issue → pane beside the note; from Git, `ctrl+n` → File → pane beside the commit.

### M3 — Audit and document

- Sweep every context that forwards to a PTY and confirm none claims `ctrl+n`.
- `.claude/skills/keyboard-shortcuts/SKILL.md`: the plugin half of the "reachable from every pane" rule, and the full `ctrl+n` assignment table.

## Acceptance evidence

- Keymap parity test covering every deck-eligible plugin's browse context, and asserting the navigation/editor contexts are untouched.
- Picker-to-target unit tests proving a plugin host resolves each kind to the same target shape the CLI and the Workspaces hosts produce.
- `tmux-drive.sh` transcripts for the M2 proofs, fully isolated (both tmux socket and state tree — run `paths` first).
- A regression test that the entry is absent when `PluginContentPanes` is off.
