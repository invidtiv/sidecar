# Files on a remote-bound project

Status: **active; nothing implemented.** This is slice 4 of [Remote destinations in `@` and `W`](remote-project-switcher.md), split out because it adds a host verb and a plugin-wide source seam rather than a landing rule. **Created:** 2026-09-01 **Verified against the tree on 2026-09-01.**

Related: [Sidecar as its own remote host runtime](sidecar-remote-hosts.md) is the ssh transport and the `hostproto` hello. [Remote host content-pane parity](../implemented/remote-host-content-pane-parity.md) is the `contentpanes.Source` read path this plan reuses for previews. [The viewer owns the screen](../implemented/remote-host-viewer-screen.md) is the lease.

## Decision first

Bound to `[aerie] Sidecar`, the Files tab shows aerie's tree and aerie's file bytes. It never shows a same-named checkout on this disk, and it never writes anything anywhere.

Files reads through two host verbs and refuses everything else by name:

```text
Files, bound to [aerie] Sidecar
   |
   +-- tree            -> sidecar content tree   (NEW verb, ContentTreeV1)
   +-- preview         -> sidecar content read   (existing, ContentReadV1)
   +-- find by name    -> sidecar content catalog (existing, ContentReadV1)
   |
   +-- new/rename/delete/move -> refused, naming aerie
   +-- inline edit, external editor -> refused, naming aerie
   +-- blame, project search        -> refused, naming aerie (no verb)
   +-- filesystem watcher           -> absent; refresh is explicit
```

A refusal that names the host is a finished state, not a gap. A tree that silently shows this machine's files under a remote label is the failure this whole plan exists to prevent, and it is the one outcome no test may pass with.

## User contract

| Gesture | Required result |
| --- | --- |
| Files tab while bound to a connected host that advertises `ContentTreeV1` | Aerie's tree for the bound project root or worktree. Directories expand lazily against the host. Nothing on this disk is stat'ed. |
| Enter / cursor onto a file | Aerie's bytes, rendered by this machine's highlighter and markdown renderer, through `contentpanes.Source`. |
| Files tab while bound to a host that does **not** advertise `ContentTreeV1` | Today's unavailable view, with the reason: that host's Sidecar is too old for the tree contract. Never a guess from a version string, never a local tree. |
| Files tab while bound to a host that is connecting, stale, or unreachable | Unavailable view naming the host and the health reason Sessions already shows. No stale tree presented as live. |
| `ctrl+p` find-by-name while bound | The host's own file catalog (`content catalog --kind file`), capped and gitignore-filtered as the host filters it. |
| Any write — new file, new directory, rename, delete, duplicate, move, drag-drop, inline edit, external editor | Refused, naming the host. Nothing is created, moved, or removed on either machine. |
| Blame, project content search | Refused, naming the host: no host verb answers them yet. |
| `r` / the refresh command while bound | One `content tree` round trip for the root plus the currently expanded directories. |
| A host snapshot generation bump while Files is on screen | The tree refreshes on that signal. There is no timer poll and no filesystem watcher. |
| Returning to a local project | Exactly today's Files. No remote state survives the unbind. |

## Current behavior

`internal/plugins/filebrowser` refuses wholesale while `ctx.HostID != ""`, at six points: `Init` returns after clearing state without building a tree (`plugin.go:409`), `Start` returns nil (`plugin.go:457`), `refresh` returns nil (`plugin.go:693`), `Update` swallows every message (`plugin.go:814`), `Commands` returns nil (`plugin.go:1312`), and `View` paints `plugin.FormatRemoteUnavailable` (`view.go:61`).

`FileTree.loadChildren` (`internal/plugins/filebrowser/tree.go:142`) is the plugin's only filesystem read for structure: `os.ReadDir` plus `entry.Info()` plus the gitignore matcher. `BuildTree(BuildSpec)` already runs off the UI goroutine and shares no state with the rendered tree, which is what makes a network round trip acceptable in that position. Previews load through `filepreview.LoadPreview(rootDir, path, epoch)` and arrive as `filepreview.PreviewLoadedMsg{Epoch, Path, Result}`.

`sidecar content` (`internal/cli/content.go`) has `describe`, `resolve`, `catalog`, and `read`. `catalog --kind file` returns a flat gitignore-filtered path list from `filefind.ScanPaths`. There is no directory listing and no entry metadata. Hosts advertise `ContentReadV1` and `UIRequestRelayV1` in `serveVerbCapabilities` (`internal/hostserve/serve.go:604`).

`plugin.Context` carries `HostID`, `HostIncarnation`, `ProjectKey`, `HostWorkspaces`, `RemoteControlSpawner`, `RemoteRunner`, and `HostVerbs`. It does not carry the bound worktree, so a plugin cannot currently name the workspace it should browse.

## Settled decisions

1. **The tree needs a new host verb.** `content catalog --kind file` is a finder index, not a tree: no directories (so no empty ones, and no structure to expand), no size or mtime, no per-directory bounds, and a whole-repo walk on every open. Files needs directory listings with entry metadata, requested lazily.

2. **That verb is `sidecar content tree`.** It belongs beside the other read-only workspace-scoped verbs, behind the same durable workspace identity and the same JSON cap. `content`'s own help text stops saying "not a general file browser" and says what it now is: the read-only content and tree contract a viewing Sidecar invokes. It remains non-interactive, read-only, and strictly enumerated.

3. **Listings are batched by path, not by depth.** `--path` is repeatable. Opening the tree is one call for the root plus every remembered expanded directory; a user expanding an unfetched directory is one more. Depth recursion would make the common case (a deep expanded set, narrow at each level) pay for subtrees nobody asked for.

4. **The verb is capability-gated as `ContentTreeV1`.** A host that predates it is read as false and Files refuses, naming the host. No inference from version strings — the same rule `ContentReadV1` already states.

5. **`loadChildren` becomes the one seam.** A `TreeSource` interface with a local implementation (today's code, unchanged in behaviour) and a remote one. There is no second tree type, no second sort, no second gitignore rule, and no second flatten. Adding a parallel remote tree would be the same mistake as a second compositor.

6. **Previews go through `contentpanes.Source`, not a new path.** `DocumentReadResult.Value` is already `filepreview.PreviewResult`, the exact payload `PreviewLoadedMsg` carries, so the remote preview is a different producer of the same message and the rendering, wrapping, scrollbar, and search code do not change.

7. **Writes stay refused, and say so by name.** Files owns no host write verb, and this plan does not invent one. Every mutating gesture answers with `plugin.FormatRemoteUnavailable`-shaped text naming the host. Refusing is the finished behaviour for this slice, not a placeholder.

8. **There is no remote watcher.** `internal/livewatch` is a filesystem signal and does not cross the boundary. The tree refreshes on explicit request and on the host snapshot generation the viewer already receives. A timer poll of `content tree` over ssh is not added: it is cost with no signal behind it.

9. **The bound worktree becomes part of the plugin context.** Files needs a durable workspace id to browse — `ProjectKey + ":worktree:" + key`, the same identity `workspaceSourceContext` already builds. `plugin.Context` gains the bound worktree key and `ReinitHost` carries it.

10. **Git decoration is out of scope.** The tree carries no git status today, so nothing is lost. When the Git plugin's own slice adds a host status verb, the tree can decorate from it.

## Open questions

- **Per-directory entry cap.** `content read` is capped under 768KiB of encoded JSON. A directory with tens of thousands of entries needs its own bound and a `truncated` flag the tree can render honestly. The number is not settled; pick it when the wire type is written, and make it a named constant with the reason in its comment.
- **Symlink presentation.** The local tree follows `os.ReadDir`'s `IsDir` and does not distinguish a symlink. Whether the remote listing reports symlinks separately is worth deciding at the wire type rather than inheriting an accident.

## Slices

Each sub-slice is independently testable and leaves the tree in a shippable state. Every commit references its td task.

### 4a — the `content tree` host verb

`internal/contentservice`: a `Tree` method over one or more relative paths under a workspace root, returning per-path entry listings with name, directory flag, size, modification time, ignored flag, and a per-listing truncation flag. Containment is enforced the way the other verbs enforce it: a path that escapes the workspace root is rejected, not clamped.

`internal/cli/content.go`: `sidecar content tree --workspace ID [--path REL]... [--json]`, with the enumerated exit codes the sibling verbs use (0 listed, 1 internal, 2 usage, 5 rejected).

`internal/hostserve/serve.go`: `ContentTreeV1: true` in `serveVerbCapabilities`, and the field on `hostproto.VerbCapabilities` with the degradation rule in its doc comment.

Proof: contentservice unit tests for containment, ignored marking, a truncated directory, and an unknown workspace; CLI tests for the JSON contract and each exit code.

### 4b — the tree source seam and a remote tree

`internal/plugins/filebrowser`: a `TreeSource` interface consumed by `BuildSpec` / `loadChildren`, a local implementation that is today's code moved rather than rewritten, and a remote implementation over `ctx.RemoteRunner` gated on `ctx.HostVerbs().ContentTreeV1`.

`plugin.Context` gains the bound worktree key; `bindRemoteDestination` and `ReinitHost` pass it; the plugin composes its browse workspace id from `ProjectKey` and that key.

`Init`/`Start`/`refresh`/`Update` stop returning early on `HostID` alone and instead branch on whether a usable remote source exists. `View` keeps the unavailable state for every case where one does not, with the reason distinguishing "host offline", "host too old", and "no bound workspace".

Proof: a bound plugin with a fake `TreeSource` builds and expands aerie's tree; a local twin directory containing `LOCAL-TWIN` is present on disk and never appears; a host without `ContentTreeV1` refuses naming the host; expanding an unfetched directory issues exactly one listing call.

### 4c — remote previews and find-by-name

Preview loading becomes source-aware: local keeps `filepreview.LoadPreview`; bound produces the same `PreviewLoadedMsg` from `contentpanes.Source.LoadDocument`, carrying `IfRevision` so a refresh is one round trip. Quick open's candidate list comes from `content catalog --kind file` while bound.

Proof: the preview pane shows `REMOTE-MARKER` and never `LOCAL-TWIN` for a twin path; a `notModified` answer leaves the rendered content in place; quick open lists the host's paths.

### 4d — honest refusals for everything else

Narrow the blanket refusal to the gestures with no host verb, each answering with text naming the host: file operations, drag-drop, inline edit, external editor, blame, project content search. `Commands()` returns the reachable subset while bound rather than nil, so the footer tells the truth about what this surface can do.

Tree refresh binds to the host snapshot generation already delivered in project scope; no watcher is started while bound.

Proof: each refused gesture is asserted to name the host and to leave both filesystems untouched; no `startWatcher` while bound.

### 4e — proof and docs

The slice 2.5 loopback fixture is the default proof: `./scripts/loopback-remote.sh up`, bind `[loopback] Loopback`, and confirm the tree, a preview, a refused write, and that the local twin is never shown. `docs/reference/cli.md` documents `content tree`. The controlling plan's slice 4 row moves to implemented.

## Proof and isolation

Same bar as every slice before it: private tmux sockets and private Sidecar state on both sides, `SIDECAR_ISOLATED_STATE=1`, no live workstation as a default. The specific tripwire this plan owes is the twin: the fixture plants a same-named project on the viewer with `LOCAL-TWIN` content, and a test that passes while showing those bytes has failed regardless of what it asserted.

Packages: `internal/contentservice`, `internal/cli`, `internal/hostserve`, `internal/hostproto`, `internal/plugin`, `internal/plugins/filebrowser`, `internal/app`.

## Changelog

- **2026-09-01** — Created. Split out of remote-project-switcher.md slice 4 once it was clear Files needs a new host verb (`content tree`), a plugin-context addition (the bound worktree key), and a source seam at `loadChildren`, rather than a landing rule.
