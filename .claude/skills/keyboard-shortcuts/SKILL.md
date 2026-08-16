---
name: keyboard-shortcuts
description: >
  Reference for keyboard shortcut implementation, keybinding registration,
  shortcut parity with vim and other TUI tools, and the complete shortcut
  assignment table across all sidecar plugins. Use when adding or modifying
  keyboard shortcuts, checking shortcut assignments, resolving key conflicts,
  or assessing alignment with vim conventions.
---

# Keyboard Shortcuts

Complete shortcut listings and context reference for all sidecar plugins. For implementation patterns, see `docs/guides/deprecated/ui-feature-guide.md`. For a detailed assessment of inconsistencies, vim alignment, mnemonic quality, and improvement proposals, see `references/assessment.md` in this skill directory.

## Architecture

- **Centralized binding registry**: `internal/keymap/bindings.go` is the single source of truth for key bindings.
- **Context-based dispatch**: Each plugin defines contexts; bindings are scoped to contexts.
- **Command palette** (`?`): Auto-discovers bindings for discoverability.
- **User overrides**: Supported via `~/.config/sidecar/config.json`.
- **Key sequences**: Compound commands like `g g` are supported with 500ms timeout.

### Adding a New Shortcut

1. Add the binding in `internal/keymap/bindings.go` under the appropriate context.
2. Add command handling in the plugin's `Update()` method (usually in a `handlers.go` file).
3. Add the command to the plugin's `Commands()` method for footer hint and command palette.
4. Keep command names short (1 word preferred) to prevent footer wrapping.

### TD Monitor Shortcuts

TD shortcuts are dynamically exported from TD itself via `ExportBindings()` and `ExportCommands()` in `pkg/monitor/keymap/`. TD is the single source of truth. To add TD shortcuts:

1. Add binding to TD's `pkg/monitor/keymap/bindings.go`
2. Add command constant to TD's `pkg/monitor/keymap/registry.go`
3. Add metadata to TD's `pkg/monitor/keymap/export.go`
4. Handle in TD's `pkg/monitor/model.go`

## Global Shortcuts

| Key | Command | Description |
|-----|---------|-------------|
| `j` / `down` | cursor-down | Move cursor down |
| `k` / `up` | cursor-up | Move cursor up |
| `G` | cursor-bottom | Jump to bottom |
| `g g` | cursor-top | Jump to top |
| `ctrl+d` | page-down | Page down |
| `ctrl+u` | page-up | Page up |
| `enter` | select | Select item |
| `esc` | back | Go back / close |
| `` ` `` | next-plugin | Next plugin |
| `~` | prev-plugin | Previous plugin |
| `]` | next-plugin | Next plugin |
| `[` | prev-plugin | Previous plugin |
| `1-5` | focus-plugin-N | Focus plugin by number |
| `?` | toggle-palette | Command palette |
| `!` | toggle-diagnostics | Diagnostics overlay |
| `@` | switch-project | Project switcher |
| `W` | switch-worktree | Worktree switcher |
| `#` | switch-theme | Theme switcher |
| `,` | open-configuration | Open Configuration (contexts that bind `,` win) |
| `i` | open-issue | Open issue |
| `r` | refresh | Refresh |
| `q` | quit | Quit (root contexts only) |
| `ctrl+c` | quit | Force quit |

## Configuration (`config` / `config-edit` / `config-confirm` contexts)

Opened with `,` or by clicking the header gear, always on Sidecar Setup. Like the Overview,
Configuration covers the plugin pane and owns keyboard focus: unhandled keys are swallowed
rather than leaking to the hidden plugin. `?` still opens the command palette.

| Key | Command | Context | Action |
|-----|---------|---------|--------|
| `j` / `k` / `up` / `down` | cursor-down / cursor-up | config | Move through sidebar destinations |
| `enter` | select | config | Open the selected destination |
| `/` | search | config | Focus Search (enters `config-edit`) |
| `tab` | focus-search | config | Move focus between sidebar and Search |
| `esc` | close-configuration | config | Return from a child route, else close and restore the prior surface |
| `down` | first-result | config-edit | Move from Search to the first visible result |
| `up` | focus-search | config-edit | Return to Search from the first result |
| `esc` | clear-search | config-edit | Clear the query and restore the full sidebar |
| `enter` / `y` | confirm | config-confirm | Confirm a consequential change |
| `esc` / `n` | cancel | config-confirm | Cancel it |

## Agent Overview (`overview` context)

Opened with `K` or by clicking the Sidecar logo. The Overview covers the plugin pane and
owns keyboard focus: a plugin left in interactive/text-input mode underneath it (embedded
shell, inline editor) does not receive keys while it is open, and unhandled keys are
swallowed rather than leaking to the hidden plugin.

| Key | Action |
|-----|--------|
| `h` / `l` / `left` / `right` | Move between lanes |
| `j` / `k` / `up` / `down` | Move within a lane |
| `enter` | Open the selected workspace (switches project) |
| `r` | Refresh the board |
| `esc` / `K` | Close the Overview |
| `q` | Quit Sidecar (confirmation modal) |

Global shortcuts stay live while it is open: `` ` ``/`~`, `[`/`]`, `1-9`, `@`, `#`, `W`, `?`, `!`,
`^`, `i`, `ctrl+c`, `q`. Plugin-switching keys (`` ` ``, `~`, `1-9`) close the Overview first.
`esc` on the Agents board or Workspaces list leaves the global space. `q` opens Sidecar's quit modal.

## Global Workspaces

Contexts: `global-workspaces` (list, root), `global-workspaces-filter`, `global-workspaces-rename`, `global-workspaces-create`, `global-workspaces-delete`, `global-workspaces-terminal` (typing), `global-workspaces-doc`, `global-workspaces-issue`, `global-workspaces-diff`.

There is no watched-preview focus: hiding the sidebar is layout only. `l` / `→` do not move focus to the preview. Clicking a file or td id focuses a content leaf with its own context; footer, help, and the palette follow `WorkspaceFocusContext()`.

| Key | Action |
|-----|--------|
| `j` / `k` / arrows / `g` / `G` | Move selection; preview follows; not typing |
| `enter` / `E` | Start typing in the selected live pane. A dead row stays put |
| click in pane | Start typing. Clicking Diff/Task action chips opens a leaf and does not type |
| click a file tab | Select that file in the document preview. `{` / `}` also cycle when the document is focused |
| click an issue tab | Select that issue in the issue preview. `{` / `}` also cycle when the issue is focused |
| click a list row | Select it; preview follows; not typing |
| click another row while typing | Switch session and stay typing |
| double-click a row | Open that identity in its owning project |
| wheel on terminal | Scroll only; do not activate |
| `ctrl+\` / `esc esc` | Stop typing and land on the list |
| `i` | Find TD task (`open-issue`). Not interactive |
| `n` | Create a worktree, choosing the owning project, then confirm the Git plan |
| `ctrl+n` | Create a shell, choosing the owning project (selected row then last-used default) |
| `D` | Delete the selected shell (shown only for shell rows) |
| `m` | Open the owning project's established merge strategy workflow for a safe worktree |
| `/` | Filter |
| `v` / `s` | Open View: sort the list. `v` matches the project sidebar; `s` is the original alias |
| `\` | Toggle sidebar |
| `esc` | Leave the global space (or clear the filter first) |
| `q` | Quit Sidecar (confirmation modal) |
| `K` | Toggle the global space |

`ctrl+]` attach stays project-only and is off unless `tmux_full_attach` is enabled. While typing, `i` and `q` go to the pane.

### Focused document (`global-workspaces-doc`)

Same tab keys as the project document pane: `q`/`esc` close, `x` close tab, `{`/`}` cycle tabs, `m` toggle render, `Y` yank path.

### Focused issue (`global-workspaces-issue`)

An unmodified click on a `td-…` link opens it beside the terminal. A
second click appends a tab; an already-open ID is focused. Click a
drawn tab to select it. The header is only the tab strip: ID plus
headline, truncated at the end so the ID stays visible. Footer hints
are Tab× Tab← Tab→. There is no close chip.

`enter` on a parent or subtask uses the same open-or-focus path (no
duplicate, no silent replace). Tabs stay in memory for the selected
row and are not written to disk.

| Key | Command | Description |
|-----|---------|-------------|
| `enter` | open-item | Open or focus the selected parent or subtask as a tab |
| `x` | close-tab | Close the active tab. Last tab closes the pane and forgets the set |
| `{` / `}` | prev-tab / next-tab | Previous / next issue tab |
| `y` | yank-issue | Copy issue as markdown |
| `Y` | yank-issue-key | Copy issue ID |
| `q` / `esc` | close | Close the pane and forget this row's in-memory tabs |

### Project issue pane (`workspace-issue`)

Same open/append/click/cycle/close journey and the same `{` / `}` / `x`
/ yank / enter keys. `tab` / `shift+tab` cycle panes; `\` toggles the
sidebar. `q` / `esc` hide the pane and retain tabs for that surface;
last `x` forgets. Switching shells or relaunching restores tabs, the
active tab, and each tab's scroll.

| Key | Command | Description |
|-----|---------|-------------|
| `enter` | open-item | Open or focus the selected parent or subtask as a tab |
| `x` | close-tab | Close the active tab. Last tab forgets the pane |
| `{` / `}` | prev-tab / next-tab | Previous / next issue tab |
| `y` | yank-issue | Copy issue as markdown |
| `Y` | yank-issue-key | Copy issue ID |
| `tab` / `shift+tab` | next-pane / prev-pane | Move focus between sidebar, terminal, document, and issue |
| `\` | toggle-sidebar | Toggle sidebar visibility |
| `q` / `esc` | close | Hide the pane. Tabs stay remembered for this surface |

## Sidebar Controls (All Two-Pane Plugins)

| Key | Action |
|-----|--------|
| `Tab` / `Shift+Tab` | Switch focus between panes |
| `\` | Toggle sidebar visibility |
| `h` / `left` | Focus left pane |
| `l` / `right` | Focus right pane |
| `+` | Grow sidebar width |
| `-` | Shrink sidebar width |

## Git Status Plugin

### Contexts

| Context | View |
|---------|------|
| `git-status` | File list (root) |
| `git-status-commits` | Recent commits sidebar (root) |
| `git-status-diff` | Inline diff pane (root) |
| `git-commit-preview` | Commit detail in right pane |
| `git-diff` | Full-screen diff |
| `git-commit` | Commit editor |
| `git-push-menu` | Push strategy selection |
| `git-pull-menu` | Pull strategy selection |
| `git-pull-conflict` | Conflict resolution |
| `git-history` | Commit history |
| `git-commit-detail` | Single commit view |

### File List Shortcuts

| Key | Command | Description |
|-----|---------|-------------|
| `s` | stage-file | Stage selected file |
| `u` | unstage-file | Unstage selected file |
| `S` | stage-all | Stage all modified |
| `U` | unstage-all | Unstage all |
| `c` | commit | Open commit editor |
| `A` | amend | Amend last commit |
| `d` / `enter` | show-diff | View file changes |
| `D` | discard-changes | Discard unstaged changes |
| `h` | show-history | Open commit history |
| `P` | push | Open push menu |
| `L` | pull | Open pull menu |
| `f` | fetch | Fetch from remote |
| `b` | branch | Branch operations |
| `z` | stash | Stash changes |
| `Z` | stash-pop | Pop stash |
| `ctrl+z` | stash-apply | Stash apply |
| `o` | open-in-github | Open in GitHub |
| `O` | open-in-file-browser | Open in file browser |
| `y` | yank-file | Copy file info |
| `Y` | yank-path | Copy file path |

### Full-Screen Diff (`git-diff`)

| Key | Command | Description |
|-----|---------|-------------|
| `,` / `.` | prev-file / next-file | Previous / next changed file |
| `s` / `u` | stage-file / unstage-file | Stage / unstage the file on screen |
| `v` | toggle-diff-view | Cycle the diff view mode |
| `w` | toggle-wrap | Toggle line wrap |
| `y` | yank-diff | Copy the diff |
| `c` | commit | Open the commit editor |
| `q` / `esc` | close-diff | Leave the diff |

This view has no tabs, so `{` / `}` are deliberately unbound here rather than
made to mean "next file" — that would be the one place in Sidecar where a brace
did something other than cycle tabs, and a silent wrong action is worse than a
no-op. File stepping is `,` / `.`, the same as in the Workspaces Diff pane.

### Commit List Shortcuts

| Key | Command | Description |
|-----|---------|-------------|
| `enter` / `d` | view-commit | Open commit details |
| `h` | show-history | Open history view |
| `y` | yank-commit | Copy commit as markdown |
| `Y` | yank-id | Copy commit hash |
| `/` | search-history | Search commit messages |
| `f` | filter-author | Filter by author |
| `p` | filter-path | Filter by path |
| `F` | clear-filter | Clear filters |
| `n` | next-match | Next search match |
| `N` | prev-match | Previous match |
| `o` | open-in-github | Open commit in GitHub |
| `v` | toggle-graph | Toggle commit graph |

### Pull Menu

| Key | Command |
|-----|---------|
| `p` | pull-merge |
| `r` | pull-rebase |
| `f` | pull-ff-only |
| `a` | pull-autostash |

## File Browser Plugin

### Contexts

| Context | View |
|---------|------|
| `file-browser-tree` | Tree view (root) |
| `file-browser-preview` | Preview pane |
| `file-browser-search` | Filename search |
| `file-browser-content-search` | Content search |
| `file-browser-quick-open` | Fuzzy file finder |
| `file-browser-project-search` | Ripgrep search modal |
| `file-browser-file-op` | File operation input |
| `file-browser-inline-edit` | Inline vim editor (all keys forwarded, global shortcuts bypassed) |

### Tree Shortcuts

| Key | Command | Description |
|-----|---------|-------------|
| `/` | search | Filter files by name |
| `ctrl+p` | quick-open | Find — a file by name (same key, same name as a workspace file pane) |
| `f` | project-search | Search — the project's contents (ripgrep) |
| `/` (preview) | search-content | InFile — this file's contents |
| `a` | create-file | Create new file |
| `A` | create-dir | Create new directory |
| `d` | delete | Delete (with confirmation) |
| `t` | new-tab | Open in new tab |
| `{` | prev-tab | Previous tab |
| `}` | next-tab | Next tab |
| `x` | close-tab | Close active tab |
| `y` | yank | Copy to clipboard |
| `p` | paste | Paste from clipboard |
| `s` | sort | Cycle sort mode |
| `m` | move | Move file/directory |
| `R` | rename | Rename |
| `ctrl+r` | reveal | Reveal in file manager |

## Conversations Plugin

### Contexts

| Context | View |
|---------|------|
| `conversations` | Session list single-pane (root) |
| `conversations-sidebar` | Session list two-pane (root) |
| `conversations-main` | Messages pane |
| `conversations-search` | Search mode |
| `conversations-filter` | Adapter filter |
| `conversation-detail` | Turn list |
| `message-detail` | Single turn content |
| `analytics` | Usage stats |

## Workspaces Plugin

### Contexts

| Context | View |
|---------|------|
| `workspace-list` | Workspace list (root) |
| `workspace-preview` | Preview pane |
| `workspace-doc` | File tabs beside the terminal (hide with `q`) |
| `workspace-issue` | Issue tabs beside the terminal (hide with `q`; last `x` forgets) |
| `workspace-diff` | Diff tabs beside the terminal (hide with `q`; last `x` forgets) |
| `workspace-create` | Create worktree input |
| `workspace-task-link` | Task selection modal |
| `workspace-merge` | Merge workflow modal |
| `workspace-interactive` | Embedded terminal |

### List Shortcuts

| Key | Command | Description |
|-----|---------|-------------|
| `n` | new-workspace | Create new workspace |
| `ctrl+n` | new-shell | Create new shell session (shadows the global `ctrl+n` cursor-down in this context) |
| `v` | open-view | Open View: sort the list (Manual, Activity, Recent, Name) |
| `V` | toggle-view | Toggle list/kanban |
| `D` | delete-workspace | Delete workspace / delete shell (confirm) |
| `d` | show-diff | Open working-tree Diff leaf |
| `p` | push | Push branch |
| `m` | merge-workflow | Start merge workflow |
| `T` | link-task | Link/unlink task |
| `s` | start-agent | Start agent |
| `enter` / `E` | interactive | Enter interactive mode |
| `i` | open-issue | Find TD task (global; not interactive) |
| `t` | attach | Full tmux attach (`tmux_full_attach`, default off) |
| `S` | stop-agent | Stop agent |
| `F` | find-file | Open a file pane on the fuzzy file finder |
| `P` | fetch-pr | Fetch a remote PR as a workspace |


### Interactive Mode

| Key | Command |
|-----|---------|
| `ctrl+\` | exit |
| `ctrl+]` | attach (`tmux_full_attach`, default off) |
| `ctrl+t` / `alt+t` | toggle / switch terminal panel (`workspace_terminal_panel`, default off) |
| `alt+c` | copy |
| `super+c` | copy (Cmd+C, when the emulator passes it through) |
| `alt+v` | paste |

### Document Pane

An unmodified click on a resolvable file path in workspace or shell terminal
output opens it beside that terminal. A second click appends a tab; a path
that is already open is focused (and `path:line` jumps). Click a drawn tab
to select it. The header is only the tab strip: each label is the relative
path, left-truncated so the filename end always wins. Document panes are
enabled by default; `--disable-feature=workspace_doc_panes` opts out for a
launch and also disables Diff (no pane tree). `shift`-drag and `alt`-drag remain terminal selection gestures.

The global Workspaces view uses the same tab strip and the same click / `{`
/ `}` / `x` keys. Those tabs stay in memory for the selected row.

`q` / `esc` hide the pane and remember the tab set for this shell or
workspace. `x` on the last tab forgets the set. Switching surfaces or
relaunching onto the same surface restores open files, the active tab,
render mode, wrap, scroll, and split ratio. `,` / `.` cycle Diff target
tabs only while a Diff leaf is focused; they do not cycle document tabs.

| Key | Command | Description |
|-----|---------|-------------|
| `j` / `down` | scroll-down | Scroll down |
| `k` / `up` | scroll-up | Scroll up |
| `ctrl+d` / `ctrl+u` | page-down / page-up | Scroll half a page |
| `g` / `G` | cursor-top / cursor-bottom | Jump to start / end |
| `ctrl+p` | find-file | Find a file by name in this pane (modal scoped to the pane) |
| `f` | search-project | Search the project in this pane (modal scoped to the pane) |
| `x` | close-tab | Close the active tab. Last tab closes the pane and forgets the set |
| `{` / `}` | prev-tab / next-tab | Previous / next file tab |
| `m` | render | Toggle rendered/raw markdown (markdown only; no-op otherwise) |
| `w` | toggle-wrap | Toggle line wrap |
| `I` | info | File info modal |
| `ctrl+r` | reveal | Reveal in the OS file manager |
| `Y` | yank-path | Copy the relative path |
| `+` / `-` | resize-pane-grow / resize-pane-shrink | Resize the workspace split |
| `tab` / `shift+tab` | next-pane / prev-pane | Move focus between sidebar, terminal, and document |
| `q` / `esc` | close | Hide the pane. Tabs stay remembered for this surface |

While a pane search is open (`workspace-doc-search`) it owns every key in
the pane: `esc` closes it, `enter` loads the hit in the active tab, and
`shift+enter` opens it in a new tab.

### Diff Pane

`d` / `show-diff` on the Workspaces list or preview opens a working-tree Diff
leaf beside the terminal. The leaf is not a root context: `q` / `esc` hide it.
`{` / `}` cycle Diff target tabs while the leaf is focused; `,` / `.` step
next/prev file inside the view.

**One rule everywhere: `{` / `}` is always "cycle the tabs of the thing I am
looking at."** Document, issue, File Browser and Diff leaves all obey it. The
Diff pane is the only surface with a second navigation axis — the files inside
the active target — and that axis gets its own pair, `,` / `.`.

The earlier arrangement was the reverse (`{` / `}` = file, `,` / `.` = tab), on
the reasoning that in-view file jumping is the more frequent act in a diff and
so deserved the better-known keys. That reasoning was sound in isolation and
wrong in aggregate: it made the diff the one place in Sidecar where `}` did not
mean "next tab", so the cost was paid on every context switch into and out of
the diff, by everyone, forever — while the benefit accrued only inside the diff.
Consistency of a key's *meaning* across surfaces beats optimality of its
*assignment* on one surface. If you are tempted to re-optimise a key for a
single pane again, that is the trade to weigh.

| Key | Command | Description |
|-----|---------|-------------|
| `d` | show-diff | Open working-tree Diff leaf (list and preview) |
| `q` / `esc` | close | Hide the pane. Tabs stay remembered for this surface |
| `x` | close-tab | Close the active tab. Last tab forgets the pane |
| `{` / `}` | prev-tab / next-tab | Previous / next Diff target tab |
| `,` / `.` | prev-file / next-file | Previous / next file in this target |
| `Y` | yank-id | Copy the target identity (`wt` / `c:…` / `r:…`) |
| `tab` / `shift+tab` | next-pane / prev-pane | Move focus between sidebar, terminal, and content |
| `\` | toggle-sidebar | Toggle sidebar visibility |

Moving around inside the viewer. These are the shared viewer's own keys
(`internal/workspacediff/keys.go`); they are registered in `bindings.go` for both
`workspace-diff` and `global-workspaces-diff` so the footer, help and palette show
them, and the viewer answers them before keymap dispatch.

| Key | Command | Description |
|-----|---------|-------------|
| `l` / `right` / `enter` | diff-open | Open the selected file's diff, or a commit's file list |
| `j` / `down` | diff-down / diff-scroll-down | Next item in the list; scroll the diff body |
| `k` / `up` | diff-up / diff-scroll-up | Previous item; scroll the diff body up |
| `h` / `left` | diff-back | Back to the file list (scrolls sideways first when panned) |
| `g` / `G` | diff-top / diff-bottom | Jump to the top / bottom |
| `ctrl+d` / `pgdown` | diff-page-down | Page down |
| `ctrl+u` / `pgup` | diff-page-up | Page up |
| `v` | toggle-diff-view | Cycle unified → side-by-side → full-file |
| `z` | toggle-diff-scope | Cycle working tree → commits → aggregate |
| `n` / `N` | diff-next-change | Next / previous change (full-file mode) |
| `f` | file-picker | Open the file picker (project pane only) |

### Issue Pane

An unmodified click on a `td-…` link in workspace or shell terminal
output opens it beside that terminal. A second click appends a tab; an
ID that is already open is focused. Click a drawn tab to select it.
The header is only the tab strip: each label is the issue ID plus
headline, truncated at the end so the ID stays visible. There is no
close chip. Footer hints are Tab× Tab← Tab→.

The global Workspaces view uses the same tab strip and the same click /
`{` / `}` / `x` keys. Those tabs stay in memory for the selected row
and are not written to disk. `q` / `esc` and last-`x` forget that row's
set.

In project Workspaces, `q` / `esc` hide the pane and remember the tab
set for this shell or workspace. `x` on the last tab forgets the set.
Switching surfaces or relaunching onto the same surface restores open
issues, the active tab, and each tab's scroll. Parent and subtask
`enter` uses the same open-or-focus path.

File tabs keep left-truncated paths so the filename survives. Issue
tabs keep the ID visible and truncate the headline.

| Key | Command | Description |
|-----|---------|-------------|
| `j` / `k` | scroll-down / scroll-up | Scroll down / up |
| `down` / `up` | | Walk parent, siblings, and subtasks |
| `ctrl+d` / `ctrl+u` | page-down / page-up | Scroll half a page |
| `g` / `G` | cursor-top / cursor-bottom | Jump to start / end |
| `x` | close-tab | Close the active tab. Last tab closes the pane and forgets the set |
| `{` / `}` | prev-tab / next-tab | Previous / next issue tab |
| `enter` | open-item | Open or focus the selected parent or subtask as a tab |
| `y` | yank-issue | Copy the issue as markdown |
| `Y` | yank-issue-key | Copy the issue ID |
| `tab` / `shift+tab` | next-pane / prev-pane | Move focus between sidebar, terminal, document, and issue |
| `q` / `esc` | close | Hide the pane. Tabs stay remembered for this surface |

## TD Monitor Plugin

Contexts: `td-monitor` (root), `td-modal`, `td-stats`, `td-search`, `td-confirm`, `td-epic-tasks`, `td-parent-epic`, `td-handoffs`.

Shortcuts are defined in TD's `pkg/monitor/keymap/` and auto-exported.

## Project Switcher

| Key | Command |
|-----|---------|
| `@` | toggle |
| `down` / `ctrl+n` | cursor-down |
| `up` / `ctrl+p` | cursor-up |
| `Enter` | select |
| `Esc` | close |

## Command Palette

Press `?` to open. Press `tab` to toggle between current-context and all-contexts view.

| Key | Action |
|-----|--------|
| `j` / `k` / `up` / `down` | Navigate |
| `ctrl+d` / `ctrl+u` | Page down/up |
| `enter` | Execute |
| `esc` | Close |
| `tab` | Toggle context filter |

## Known Conflicts and Design Decisions

Key conflicts exist across plugins (e.g., `d` = delete in file-browser, diff in git, delete-session in conversations). See `references/assessment.md` for the full inconsistency analysis, vim alignment audit, mnemonic analysis, and proposed improvement plan.

### Shift Modifier Convention (Current)

- `s`/`S`: stage / stage-all (git)
- `u`/`U`: unstage / unstage-all (git)
- `d`/`D`: diff / discard (git), delete/- (file-browser)
- `y`/`Y`: yank item / yank path
- `n`/`N`: next-match / prev-match (search contexts)
