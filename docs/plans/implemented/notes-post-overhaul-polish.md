# Notes post-overhaul polish

**Status:** Implemented. All delivery stories and epic `td-8ca977` are independently reviewed and closed; integrated tests, build, diff checks, race coverage, and isolated real-app proofs pass. **Source:** td note `nt-22509205`, items 3 and 7-25 only. **Epic:** `td-8ca977`

## Outcome

Notes is a stable project surface whenever the td panel is enabled. A project that has not run `td init` gets a clear mouse-and-keyboard setup path rather than a dead screen. Once inside Notes, creating, deleting, filtering, reading, and editing feel immediate and native: the list and editor retain place, the header exposes useful state without crowding content, and built-in or `$EDITOR` editing follows the user's preference.

Sidecar remains a presentation client. td owns note persistence and initialization; Sidecar does not add a Notes CLI, write a second note store, or modify agent instruction files during Notes setup.

## Scope map

| Note item | Delivery story | Contract |
|---|---|---|
| 3 | `td-cdc3ba` | Notes stable/default-on behind the td-panel preference; initialization modal and preference link |
| 7, 17, 20, 25 | `td-982583` | Default editor preference, Mac/Emacs keys, select all, and context-safe note-ID copy |
| 8, 9, 19 | `td-889d49` | One optimistic create/delete state owner with rollback and next-note focus |
| 10, 11, 12, 16 | `td-43b82d` | Native multi-click selection, EOL click placement, and scoped `$EDITOR` mouse forwarding |
| 13, 14, 15, 18, 21, 22 | `td-8c2abd` | Shared geometry, filter control, spacing, text color, and compact save symbols |
| 23, 24 | `td-42cd0f` | Verify the removed attach path and render loose numbered outlines as lists without breaking source mapping |

Items 1, 2, and 4-6 were removed in the source note. Everything below its `Next round` heading is deliberately out of scope.

## Product decisions

### Notes and td activation

“td activated” means the existing `plugins.td-monitor.enabled` preference. It cannot mean “a `.todos` directory already exists”: that would hide Notes before the requested initialization journey is reachable and would require startup-time filesystem work or dynamic plugin registration.

- `notes_plugin` defaults on, while explicit config and CLI overrides keep their current precedence.
- Notes is assembled only when both its feature flag and the td-panel preference are enabled.
- Turning td off temporarily hides Notes without overwriting the user's Notes preference.
- Configuration keeps one Notes toggle, removes the Beta label, and explains the td dependency. There is no second enablement setting.
- Uninitialized detection happens during the existing asynchronous first load, never in plugin planning, construction, `Init`, or `View`.
- The setup modal says that `td init` creates `.todos/`, may update `.gitignore`, and will not modify `AGENTS.md` or `CLAUDE.md`. Its actions are Initialize td, Notes preferences, and Not now.
- td initialization preflight/execution is one shared boundary used by the td and Notes surfaces. Success lets both refresh; refusal and errors remain actionable.

### Editor choice and terminal limits

The existing `plugins.notes.defaultEditor` field becomes a real persisted setting. The first supported choices are Built-in and `$EDITOR` in pane. Enter and a body click follow that preference, while explicit commands preserve access to both paths.

The built-in editor owns native text semantics. It supports word/line multi-click, click-after-EOL, Emacs motions, and `super` shortcuts when the terminal delivers the modifier. Portable alternatives such as `alt+a` remain advertised because some terminal emulators reserve Command-key chords and never send them to Sidecar.

The in-pane `$EDITOR` owns its terminal semantics. Sidecar forwards translated, pane-local click/drag/release/wheel events only while the application reports mouse tracking. It can make Vim/Neovim mouse interaction work when configured by the editor, but it cannot force an arbitrary terminal editor to understand mouse input. Keyboard editing remains correct when mouse reporting is absent.

Notes has no full-screen tmux attach mode. The current empty attach key, nil attach callback, absent command/footer, and inert `ctrl+]` are retained and regression-tested.

Notes also accepts informal numbered outlines as notes, even where strict CommonMark would fold them into prose. In particular, an ordinal such as `3.` or `7.` at the start of a source line may interrupt the preceding paragraph without a blank line. This forgiveness is Notes-scoped: Files, issue views, and other Markdown consumers keep the shared renderer's standard semantics. The implementation must preserve the original source line/column anchors so clicking the rendered list still opens the unchanged note at the right caret.

### Optimistic mutations

Optimism is an application-state contract, not a cosmetic spinner removal.

- Create inserts a temporary local note and opens it immediately. Canonical td success replaces the temporary identity without dropping typing, selection, place, or queued persistence.
- Confirmed delete removes the row immediately. The same list index now selects the former next note; deleting the last row falls back to the previous note.
- Pending creates and delete tombstones participate in load reconciliation so a late refresh cannot resurrect or erase the wrong row.
- Failure restores the exact prior list/editor/cursor state and surfaces an error. Undo and retry must describe durable truth.

### Presentation

The list header is one aligned row: `Notes` at left, with an unparenthesized count and colored Active/Archived/Deleted control at right. The current-state shortcut toggles back to Active, and the control is reachable by mouse and keyboard. One blank row separates the header from note rows.

Preview and built-in edit continue to consume one geometry value. That value gains one content column on each side, one row below the status row, and one bottom row. The editor uses the same theme body color as the raw preview. The status row keeps date metadata and ends with one theme-aware symbol: green saved dot, yellow unsaved star, or red save-error mark. Narrow panes discard date detail before discarding the actionable state.

## Delivery order

The stories intentionally serialize where they share Notes layout, mouse, or state ownership. Valid focused verification follows its candidate; broad gates run once on the reviewed integrated branch.

1. `td-cdc3ba` — activation, shared td setup, and Configuration foundation.
2. `td-42cd0f` — forgiving numbered-outline rendering and attach closeout.
3. `td-8c2abd` — final layout and visual geometry.
4. `td-982583` — editor preference, keyboard behavior, and ID copy.
5. `td-43b82d` — mouse behavior against the settled geometry and editor routing.
6. `td-889d49` — optimistic lifecycle state, isolated as the highest-risk change.

Each story requires independent review before closing. A review finding is fixed and re-reviewed against the same story rather than hidden in a later batch.

## Verification

Focused tests establish:

- default/override plugin gating and startup-path purity;
- setup modal first-key, mouse, failure, conflict, success, and preference routing;
- no changes to agent instruction files during Notes initialization;
- list/header geometry and filter toggle behavior at narrow and wide widths;
- preview/edit cursor, scroll, wrap, selection, and hit-region parity after spacing;
- save-symbol states, theme colors, and narrow-header degradation;
- strict and loose numbered-list stability and source anchors across note switches, resize, edit, and save;
- editor preference persistence and context-specific key ownership;
- word/line multi-click, Unicode/wrapped EOL targeting, and mouse-reporting boundaries;
- optimistic mutation responsiveness, load reconciliation, rollback, and post-delete focus.

The integrated candidate must pass `go test ./...`, `go build ./...`, `git diff --check`, and race coverage for the Notes mutation/save state machines. Real-app proof starts with `./scripts/tmux-drive.sh paths`, isolates both tmux and Sidecar state/config, and covers:

1. uninitialized project -> Notes setup modal -> initialize -> loaded Notes;
2. disable Notes and disable td across isolated restarts;
3. narrow/wide list filter, edit spacing, and save/error status;
4. note switching with Markdown lists intact;
5. built-in and mouse-aware Vim click/scroll/select behavior;
6. slow create/delete showing immediate UI state, canonical success, and rollback.

Every proof run stops `tmux-drive.sh`. Nothing may stop, restart, replace, or attach to the machine's default tmux server, and no proof may resolve under the real Sidecar state or config tree.

## Deferred

- Notes organization, tags/folders/wiki links, and the non-synced-notes product story from the source note's `Next round` section.
- Forcing mouse support into terminal editors that do not enable mouse reporting.
- Replacing td ownership with a Sidecar database, API, or CLI.
- The broader td note-link and external-conflict phases still controlled by `notes-plugin-overhaul.md`.
