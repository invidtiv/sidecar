# Global Sessions Persistence

Quit Sidecar with the global Sessions browser open — a row selected, a pane tree composed on it, a terminal split running — and restarting puts you back exactly there: same tab, same row selected, same panes open, the terminal split reattached to its still-running tmux session with scrollback intact. On top of that, `sidecar layout get`/`apply` answer for this surface, because a tree an agent cannot read or compose is a parity bug.

**Owns:** persistence and restart-restore of the global Sessions browser's selection and per-row pane trees, the host-independent pane-layout codec, and the Sessions destination of the `sidecar layout` CLI.

**Relationship to [live-terminal-leaf-extraction.md](../implemented/live-terminal-leaf-extraction.md):** that plan's decision 8 deliberately left the global surface's tree memory-only and named the change a separate decision; this plan is that decision, answering its open question 2 with *yes, persist it*. Its phase 4 listed three follow-ons — this plan takes the first two (persistence, layout CLI) and explicitly does not take the third; see "Not in this plan".

**Relationship to [terminal-splits-and-windowing.md](terminal-splits-and-windowing.md):** that plan owns the persistence evolution rules for `state.PaneLayoutJSON` (additive fields; unknown kind ⇒ drop the leaf and collapse its split), and this plan inherits them unchanged. Its B3 (splits on non-workspace surfaces) stays there.

## What already works, and the exact gaps

Restart already restores more than it may appear to. The top-level space and tab come back via `state.SetLastScope` and `GlobalTab.persistID` (`internal/app/scope.go`) — quit on the Sessions tab and Sidecar reopens on it. The project workspace surface restores its trees per project through `WorkspaceState.PaneLayouts` (`surface → *PaneLayoutJSON`). The tmux sessions underneath every live leaf are durable, `sidecar-tp-*` splits included. And `PaneLayoutJSON` is already the right vehicle: presentation-neutral, additive, and its `Session` field already records a live leaf's durable selector by tmux session name, never by pane id.

Three gaps remain, and they are all code this plan can point at:

1. **The selected row is not persisted.** `previewState.workspaceID` and the sidebar list selection die with the process; no state key records them. Restart lands on the top of the list.
2. **The per-row pane trees are memory-only.** `previewPaneCache` (`internal/overview/preview.go`) holds `root`, `focus`, `nextID`, the `contentpanes.Deck`, and the `termpanes.Deck` per row and persists nothing — extraction-plan decision 8, deliberately deferred to here.
3. **The codec is host code.** The translation between a pane tree and `PaneLayoutJSON` lives on the project plugin: `encodePaneNode`/`encodeSurfacePaneLayout` and `decodePaneNode` with per-kind `decodeDocLeaf`/`decodeIssueLeaf`/`decodeNoteLeaf`/`decodeDiffLeaf`/`decodeResourceLeaf` methods spread across `doc_panes.go`, `issue_panes.go`, `note_panes.go`, `diff_panes.go`, `resource_panes.go`. The global browser cannot persist without this translation, and duplicating it would be the same trap `termpanes` just closed: two hosts hand-rolling one lifecycle. The refactor precedes the feature, again.

The layout CLI has the same shape of gap: `internal/cli/layout.go` resolves destinations by `--shell` and `--project`, both of which land on a project surface. The Sessions surface has no address, so its trees are invisible to `layout get` and unreachable by `layout apply`.

## Settled decisions

1. **Restore means reattach, never replay.** The persisted tree stores identities — document paths, issue keys, note IDs, diff refs, resource references, tmux session names — and scroll positions, exactly as `PaneLayoutJSON` does today. Content is re-fetched on restore; a live leaf reattaches to its tmux session. Nothing buffered is written to disk.
2. **The codec moves to one host-independent home before the global surface persists anything.** A new package, `internal/panecodec`, owns `(panelayout tree, contentpanes deck, termpanes deck) ↔ *state.PaneLayoutJSON` for every kind both surfaces host. Hosts supply what is genuinely theirs — content loaders returned as `tea.Cmd`s, target resolution — through a small options struct, the same division `contentpanes` and `termpanes` already draw. The project workspace adopts it with no behaviour change, proven the same way phase 1 of the extraction was: its suite passes unedited.
3. **Storage is global `state.json`, keyed by durable inventory row ID.** Two new keys: `sessionsSelected` (the row ID last shown) and `sessionsPaneLayouts` (`row ID → *PaneLayoutJSON`). Row IDs (`projectKey + ":shell:" + key`, `projectKey + ":worktree:" + path` — `workspaceinventory`) survive restarts. Per-project `WorkspaceState` is the wrong home: rows span every project, and the browser is a global surface with global state precedent (`Scope`, pinned IDs, list sort).
4. **The project surface and the global row for the same workspace keep separate trees.** They are different surfaces with different budgets and focus policy; sharing one persisted layout would make a browsing gesture in the global browser silently rewrite a project workspace's composition. The extraction plan drew this line; persistence keeps it.
5. **Persist only composed trees, and persist on change.** A row whose tree is the bare primary preview writes nothing; closing a row's last extra pane deletes its entry. This keeps `sessionsPaneLayouts` proportional to what the user built, not to what they browsed past. Deleting a row through the delete flow removes its entry, the same moment `pruneDeletedTerminalRows` releases its live state.
6. **Restore is lazy and selection-driven.** The inventory catalog arrives asynchronously; when the persisted selected row appears, the sidebar selects it and its tree decodes into `previewPaneCache` exactly as a cached row does today. Other rows' persisted trees decode when first shown, not at startup — nothing in this plan may add filesystem work to the pre-first-frame path (startup-latency doctrine). A persisted entry whose row never returns is not an error; see open question 3 for when it is garbage.
7. **Terminal split restore follows the project surface's reattach rule.** The persisted `Session` names a `sidecar-tp-*` session; `termpanes.EnsureSession` reuses it when alive — which is what makes scrollback survive the restart — and recreates it when the machine or server restarted underneath. A recreated session is a fresh shell in the right directory, honestly empty.
8. **The Sessions surface gets a layout CLI destination.** `sidecar layout get`/`apply` grow a `--sessions [ROW]` destination (name per open question 1) answered by the overview when `ScopeGlobal` is on screen, with the same decline rule the project surface has — not on screen ⇒ exit 4, because a stale answer is worse than a refusal. `apply` speaks the same kind vocabulary it speaks today, `shell` included, and the same `panelayout` cap and floor rules refuse the same requests the modal refuses.

## Phase 1 — Extract the codec

No user-visible change. The project workspace ends the phase encoding and decoding through `internal/panecodec` where it held plugin methods.

1. **Create `internal/panecodec`** with encode — tree plus decks in, `*state.PaneLayoutJSON` out — and decode, which reconstructs the tree and decks and returns the content-load commands for the host to run. Per-kind logic moves from the five `decode*Leaf` methods; host-owned resolution (project roots, provider registries, issue stores) enters through the options struct.
2. **Adopt it in the project workspace**, deleting `encodePaneNode`, `encodeSurfacePaneLayout`, `decodePaneNode`, and the five `decode*Leaf` methods.
3. **Extend the parity scan** (`internal/parityscan`) to assert neither host declares a private encode/decode of `PaneLayoutJSON`.

**Ship criteria:** every existing test in `internal/plugins/workspace` and `internal/state` passes unedited; `grep -E 'func \(p \*Plugin\) (encode|decode).*(Pane|Leaf)' internal/plugins/workspace` returns nothing; persisted state written by the new codec is byte-compatible with state written before it (same fixture in, same JSON out).

## Phase 2 — Persist and restore the global surface

1. **Persist the selected row** to `sessionsSelected` when the preview binds to a row — debounced, so arrowing through the sidebar costs one write, not one per row.
2. **Persist composed trees** to `sessionsPaneLayouts` through the codec at the same moments the project surface persists — pane opened, closed, resized, renamed — under decision 5's only-composed rule.
3. **Restore on startup:** select the persisted row when the catalog delivers it; decode a row's persisted tree on first show into `previewPaneCache`; reattach terminal splits per decision 7. The existing row-scoped lifecycle (detach on navigate-away, reattach on return) needs no change — restore is just the cache warm path fed from disk.
4. **Prune on row deletion**, wired where `pruneDeletedTerminalRows` already runs.

**Ship criteria:** the real-app proof below passes; a fresh profile (no state) starts exactly as today; a persisted layout naming a missing document, issue, or dead tmux session degrades by the windowing plan's rule — drop the leaf, collapse its split — rather than failing the restore.

## Phase 3 — The layout CLI answers for Sessions

1. **Add the destination** (open question 1 settles the flag) resolving to the running instance's global surface and, optionally, a named row; default is the selected row.
2. **Route `layout get`** through the same projection the project surface reports — grid, kinds, targets, sessions, caps, floors — sourced from the row's tree.
3. **Route `layout apply`** through the same all-or-nothing uirequest verdict path, creating panes on the row's tree through the same create paths the modal drives.

**Ship criteria:** `sidecar layout get --json` on the Sessions surface returns the selected row's tree; an `apply` that opens a doc pane and a terminal split beside the preview yields the same tree the modal would; every refusal (cap, floors, off-screen) matches the interactive surface's refusal for the same request.

## Not in this plan

- **Panes and terminal splits on the non-workspace surfaces** (Files, Git, td, Notes) — the extraction plan's phase 4 item 3 and terminal-splits-and-windowing.md's B3. It is wanted, and `termpanes` plus this plan's `panecodec` make it mostly adoption rather than construction — but it multiplies surfaces rather than deepening these two, carries per-plugin layout-host work with its own questions, and neither direction depends on the other. It deserves its own plan, written after this one ships, and B3 remains its authority until then.
- **Renaming the "Create Workspace" modal.** The name is already wrong — the modal creates panes, splits, and terminals as often as workspaces — but the fix is one string in `internal/workspacecreate/form.go` plus docs, orthogonal to persistence. Do it as a standalone change whenever; the pane-everywhere plan above is its natural home only if it waits that long.

## Acceptance evidence

- **Phase 1:** full suite green with no test edited for the refactor; the encode/decode grep empty; the byte-compatibility fixture green.
- **The restart round-trip, driven end to end** on a fully isolated run (`scripts/tmux-drive.sh`, tmux socket *and* state tree isolated — `tmux-drive.sh paths` first): open Sessions, select a non-first row, open a doc pane and a terminal split, write a marker line in the split, quit, relaunch, and snapshot the same tab, row, tree, and the marker still in the split's scrollback.
- **The degradation table:** persisted layouts naming a deleted row, a missing document, a dead `sidecar-tp-*` session, and an unknown pane kind each restore to the documented lesser state without an error state or a crash.
- **CLI parity:** `layout get --json` and a two-pane `apply` on the Sessions surface, asserted equal in shape to the same operations on a project surface, in the idiom of the existing catalog parity tests.

## Open questions

1. **The destination flag's name.** `--sessions` matches the fleet vocabulary in the header; `--global` matches `ScopeGlobal` in the code. Whichever is chosen, the row selector (by display name? by durable ID?) needs the same ambiguity rules `--shell` already has.
2. **Should `sessionsSelected` also capture pane focus within the row's tree?** Restoring focus to the exact leaf is more faithful; restoring to the primary preview is simpler and never focuses a leaf that failed to restore. Lean: persist `focus` (it is already in the cache and the JSON has `Active` precedent), fall back to primary when the leaf is gone.
3. **When is a persisted layout for an absent row garbage?** A worktree row absent because the branch is checked out elsewhere returns; a shell row absent because the session died usually does not. Options: prune entries whose row is absent after a full inventory sweep for N consecutive launches, or cap the map's size LRU-style. Needs deciding before `sessionsPaneLayouts` can grow unbounded, but not before phase 2 ships behind decision 5's only-composed rule, which keeps growth slow.
