# Remote host content-pane parity

Status: **implemented** on `pane-parity` (td-89c1cb, td-87358d, td-925cf9). Isolated unit and integration proof covers every PlanKind; the two-machine tmux-drive recipe is `docs/guides/active/remote-content-pane-proof.md` and was not executed against a live host in this work.

Related: [Sidecar as its own remote host runtime](../active/sidecar-remote-hosts.md) is the controlling transport plan; [The viewer owns the screen](../active/remote-host-viewer-screen.md) is the follow-on for relaying `sidecar open` / `layout` from a Sidecar-managed pane on the host onto the viewing Sessions preview; [Cross-project td issue links](../active/cross-project-issue-links.md) defines local-first issue ownership and fallback semantics; [Terminal resource providers](terminal-resource-providers.md) defines provider matching, safe documents, and lifecycle bounds.

## Decision first

When the user is viewing a workspace on a registered remote host, a clicked reference is resolved and loaded on the machine that owns that workspace, then rendered and placed by the viewing Sidecar through the same `contentpanes.Deck` and shared viewer models used for local workspaces.

The host boundary is explicit data:

```text
remote terminal or remote content pane
        |
        v
host-qualified content context
        |
        +--> URL --------------------------------------> local browser
        +--> tmux session -----------------------------> same registered host
        +--> file / issue / note / diff / resource ---> bounded read-only Sidecar verb on that host
                                                           |
                                                           v
                                              existing shared pane models
```

The viewer owns interaction, pane placement, focus, rendering, scroll state, and Sessions pane state. The remote host owns filesystem containment, git and td resolution, provider configuration and execution, content loading, and change observation. No remote path is passed to local filesystem, git, td, or provider code.

This is a presentation capability over data owned by files, git, td, tmux, and external providers. It does not add a public `sidecar open --host` surface merely for parity. The internal host content command exists as a transport endpoint for Sidecar itself. An agent that wants the bytes of a file or issue on that machine uses ordinary tools over SSH. An agent in a Sidecar-managed pane that wants that file *on the screen the user is looking at* is [The viewer owns the screen](../active/remote-host-viewer-screen.md), still without a `--host` flag.

## User contract

From a remote Sessions row, the following behaves as it would in Sidecar running locally on that host:

| Clicked reference | Required result |
| --- | --- |
| Relative, absolute, or `~/` file | Resolve with the same semantics as the selected remote workspace, open a Document pane, honor the line number, and refresh when that remote file changes. |
| `td-*` issue | Load through the remote host's td installation and td store, including its configured-project fallback search, owner badge, related issue navigation, and live refresh. |
| `sidecar://note/nt-*` | Load the note from the remote td store and refresh when that store changes. |
| Git commit/range/revision reference | Resolve and load against the remote checkout, then retain the existing Diff pane interactions and refresh rules. Working-tree identities remain an internal pane target; the terminal scanner does not currently promise a clickable working-tree token. |
| External resource key | Match and resolve through the remote host's provider configuration, executable, credentials, and safe-document pipeline. |
| Sidecar tmux session name | Attach/select only the matching session on the same remote host, never a local or different-host twin. |
| HTTP(S) URL | Open through the viewer's existing validated local-browser path; the URL itself has no host-owned resolution. |

Links inside a remotely loaded pane retain the same host-qualified context wherever that pane already exposes content links locally. A remote file containing `td-1234` must open the remote issue, not switch back to the viewer's current project or provider set.

A link that cannot be resolved says why. It never silently does nothing, never opens a same-named local object, and never falls back from a failed remote request to local I/O.

## Current behavior and root cause

The semantic click path is already shared. `targetactivation.PlanForSpan` maps scanned spans to the same plan vocabulary for project Workspace and global Sessions, and `contentpanes.Deck` owns shared Document, Issue, Note, Diff, and Resource pane lifecycle and placement.

The I/O side is deliberately local-only:

- `internal/overview/content_deck.go:previewDeckContext` refuses every remote workspace. Its comment names the exact danger: a remote path can exist locally and show this machine's diff or file under the remote row's name.
- `internal/overview/terminal_link_state.go` canonicalizes the selected workspace path with local `filepath.EvalSymlinks`, and `internal/overview/preview_links.go` revalidates files and diffs through the viewer's filesystem and git.
- `internal/contentpanes/viewers.go` calls `docview.Load`, `issueview.Load`, `noteview.Load`, and `workspacediff.View` with a path. Those models open files or spawn local `td` and `git` processes.
- `internal/app/resourceproviders.go` publishes one viewer-local provider matcher snapshot and resolver to both surfaces. A remote row therefore cannot see a provider configured only on the host, and must not borrow credentials or matcher precedence from the viewer.
- `internal/overview/live_preview.go` derives local `livewatch` targets from the selected path. A remote pane could load once through an ad hoc read and still become quietly stale.
- `internal/overview/preview_links.go:attachPreviewSession` intentionally searches only local rows because the click currently carries no source host.
- `termpreview.LinkScope` names the viewing surface rather than the registered host, and `contentlink.ResolutionIndex` keys file resolutions by root and candidate only. Equal paths on two machines can therefore collide unless source identity is part of the scope and cache key.

The parent remote-host plan recorded this failure during Phase A and kept the fail-closed guard. This plan replaces the missing read boundary; it does not weaken the guard.

## Scope

In scope:

- Terminal-link recognition, fresh resolution, pane opening, typed content loading, manual reload, conditional live refresh, and nested-link activation for every kind in `targetactivation.PlanKindsFromSpans`.
- Global Sessions, which is the only surface that currently hosts registered remote workspaces. The source and viewer seams remain presentation-neutral so a future remote project Workspace can reuse them without another loader path.
- Local/remote parity for content display, nested navigation, safe pane actions, and refresh in the existing Document, Issue, Note, Diff, and Resource panes.
- Honest capability, version-skew, disconnected-host, stale-result, and unsupported-content states.
- Bounded one-shot read-only content verbs on the remote host, invoked lazily over the existing SSH ControlMaster.

Out of scope:

- Turning the Files, Git, Tasks, or td plugins into complete remote project browsers.
- Remote file mutation or inline editing. The existing `e` path starts a local tmux editor against a local path; on a remote Document pane it must be unavailable with an actionable message rather than touching the viewer's filesystem. Host-aware inline editing is a separate capability and plan.
- Remote Document pane file finding and project-wide search. `ctrl+p` and `f` currently walk `doc.root` locally and must be hidden or explicitly refused for a remote source in the file steel thread. In-document search remains available because it uses the already loaded body. Host-backed finder/search is a separate follow-on.
- Relaying `sidecar open` from a process on the remote host back to the viewer. Sidecar-managed panes whose geometry lease is held by a connected viewer are [The viewer owns the screen](../active/remote-host-viewer-screen.md). Arbitrary processes (cron, a random SSH) stay out of scope there as well.
- A persistent daemon, mounted filesystem, SSHFS dependency, or general-purpose remote command API.
- Adding content payloads or viewer requests to the inventory stream.

## Architecture

### 1. One host-qualified content context

Add a presentation-neutral context owned near `contentpanes`, not in Overview:

```go
type SourceContext struct {
    HostID          string // empty means local
    HostIncarnation uint64 // viewer registry incarnation; never sent as authority
    ProjectKey      string // unscoped key on the owning host
    ProjectRoot     string
    WorkspaceID     string // unscoped durable inventory identity
    WorkspaceKind   workspaceinventory.Kind
    WorkspaceKey    string
    Root             string // selected shell workdir or worktree path on the owning host
}
```

`contentpanes.SurfaceContext` carries this source context alongside `Root`, `DiffRoot`, `Surface`, and `Epoch`. A local source has an empty `HostID` and delegates to today's loaders. A remote source is accepted only while the registered host incarnation and selected inventory identity still match.

The remote service never trusts `Root` by itself. It revalidates the configured project and workspace identity through the host's own inventory/state rules, canonicalizes the root on that machine, and refuses a root that is no longer owned by that shell/worktree. Relative file paths must remain contained by that validated root. Explicit absolute and `~/` file targets retain today's local semantics: they may open a regular readable file outside the project after the host revalidates them, because running Sidecar locally on that host permits the same deliberate click. Diff and implicit relative resolution never escape the validated checkout.

Persist only `HostID`, stable project/workspace identity, roots needed as non-authoritative hints, and references; never serialize `HostIncarnation`, loaded bodies, or rendered rows. Registry incarnation is a viewer-process counter, not durable identity. After decoding pane state, look up and bind the current incarnation before any load; memory-only cache entries may retain that active incarnation. Sessions already scopes workspace IDs by host, and every decoded state still revalidates the current host incarnation and workspace before loading.

### 2. Narrow read-only CLI verbs over the existing request seam

Keep `sidecar host serve --stdio` one-way and inventory-focused. Its cheap observation loop, notification semantics, reap call graph, and failure isolation stay unchanged. Use the already proven `hosts.RunSidecar` request seam for content rather than adding a second resident protocol before measurements justify one.

Add a small read-only content application service exposed through structured CLI verbs suitable for `hosts.RunSidecar`:

```text
sidecar content describe [--if-revision REV] --json
sidecar content resolve --workspace ID --kind file|diff --target VALUE --json
sidecar content read --workspace ID --kind KIND --operation OP --target VALUE [--if-revision REV] --json
```

The exact command spelling may be refined during implementation, but the contract is settled:

- Every verb is non-interactive, read-only, and strictly enumerated. There is no generic remote executor.
- `resolve` returns identity and metadata only; it does not eagerly ship a body for every recognized link.
- The host re-resolves the durable workspace identity to its authoritative root on every request. Viewer-supplied paths are hints or targets, never authority.
- `read` returns a strict kind- and operation-specific DTO plus a revision/fingerprint. `--if-revision` permits a small `notModified` answer, so a refresh never needs a metadata preflight followed by a second body read.
- `hostproto.VerbCapabilities` advertises an additive `ContentReadV1` capability without changing the inventory protocol version. A new viewer and old host fail explicitly; an old viewer ignores the new capability.
- `RunSidecar` retains its deadline, cancellation, exit-code, stdout-contamination, and SSH ControlMaster behavior. Every response envelope implements `hosts.ResultValidator`/`ValidRemoteResult`, and each operation has a limit on its final JSON-encoded bytes rather than only its raw payload. The host preflights encoded size and returns a small structured truncation, paging, or oversize result before `RunSidecar`'s 1 MiB stdout cap can truncate invalid JSON; content code does not enlarge the global cap to accommodate an unbounded snapshot. Document bounds account for JSON escaping or base64 expansion rather than assuming a 500 KiB raw file necessarily fits.
- The same content service powers the local adapter directly and the remote CLI wrapper, so resolution, containment, typed errors, and bounds cannot drift by surface.
- Read-only call-graph tests analogous to `TestServeIsReadOnly` prove that no tmux mutation, file write, td mutation, git mutation, config save, or arbitrary shell execution is reachable.

The SSH connection is already multiplexed. A resident content session would add application request correlation, backpressure, reconnect, cancellation, and version negotiation without itself solving bounded payloads or stale content. Start with one-shot verbs, measure open and refresh latency plus process and CPU cost at one, four, and eight visible panes, and add a resident transport only if an explicit exit gate is crossed.

`internal/hosts` owns invocation and SSH transport reuse. Overview depends on a narrow source interface, not command arguments, exit codes, or SSH details, so fake sources can prove the whole UI journey without a network.

### 3. Typed loader adapters, not remote branches in views

Extend `contentpanes.Config` with a source aggregate whose narrow methods return typed domain results that the existing models consume:

```go
type ReadRequest struct {
    Ref        contentlink.Ref
    IfRevision string
}

type ReadResult[T any] struct {
    Value       T
    Revision    string
    NotModified bool
}

type IssuePayload struct {
    Data  *issueview.Data
    Owner *issueview.Owner
}

type Source interface {
    Resolve(context.Context, SourceContext, contentlink.Pending) (contentlink.Ref, error)
    LoadDocument(context.Context, SourceContext, ReadRequest) (ReadResult[filepreview.PreviewResult], error)
    LoadIssue(context.Context, SourceContext, ReadRequest) (ReadResult[IssuePayload], error)
    LoadNote(context.Context, SourceContext, ReadRequest) (ReadResult[*noteview.Data], error)
    LoadDiff(context.Context, SourceContext, ReadRequest) (ReadResult[DiffPayload], error)
    ResolveResource(context.Context, SourceContext, resource.Reference, bool /* refresh */) (resource.Document, error)
}
```

The exact Go shape may be one aggregate or several small loader interfaces; the invariant is more important than the spelling: a refresh loader receives the last adopted revision, and its typed unchanged result can complete the model's in-flight refresh gate without replacing content or repainting. Changed results are converted to the same model messages used today. View models retain their current request-generation, epoch, rendering, scroll, tab, and stale-result behavior. They do not learn about SSH, JSON, HostID branching, or protocol envelopes.

The local default adapter delegates to the existing file/td/git/provider functions. Focused negative-control tests pin local commands, results, and pane behavior before remote support is enabled. Remote payload structs are explicit wire DTOs converted at the client boundary; do not JSON-marshal `tea.Msg`, `error`, `os.FileMode`, or arbitrary internal model structs directly.

For documents, transmit bounded raw bytes and metadata and perform syntax highlighting/rendering with the viewer's active theme. For issues and notes, execute the existing read-only td logic on the host; issue fallback candidates come from the remote host's config and retain the existing owner-root adoption semantics. For diffs, extract the current git loading operations behind a loader seam and return bounded operation-specific DTOs for working-tree summary/detail, commit or range detail, selected commit-file content, full-file pages, and cursor-driven subloads. There is no universal remote diff snapshot: every operation has truthful truncation or paging. For resources, the host owns provider selection/execution and returns the provider wire response; the viewer runs the existing validation/sanitization gate again before adopting a `resource.Document`. Because every remote provider invocation is a new process, the remote source adapter owns a bounded viewer-side cache keyed by host ID, host incarnation, deterministic descriptor fingerprint, provider identity/instance, and reference. A normal resolve honors the returned freshness/expiry semantics; `refresh=true` bypasses and replaces that cache. Host-incarnation or descriptor-fingerprint change discards it. Credentials, commands, and unsanitized provider data never enter the cache or cross the wire.

### 4. Remote-aware link preparation and nested activation

Keep recognition local: the viewer already has the rendered terminal or document text, and `contentlink` scanning is state-free. Make only resolution source-aware:

- `termpreview.LinkCoordinator` receives the `SourceContext`. Local file/diff fresh resolution stays byte-for-byte on the current resolver; remote resolution is a content request to the selected host.
- Overview selects provider matchers by source. Local rows use the app's current matcher snapshot. A remote `content describe` result supplies only that host's matchers and a deterministic descriptor fingerprint computed from the validated, ordered descriptor wire content; process-local `resourceprovider.Snapshot.Generation()` is never used across invocations. The viewer caches the descriptors by host incarnation plus fingerprint. While a remote row is visible, a bounded descriptor cadence conditionally calls `content describe --if-revision`; selecting the row or explicit reload triggers an immediate conditional describe, and a host-incarnation change discards the cache. The cadence is a named, tested constant tuned with the content refresh measurements, not an assumed inventory signal. Until describe succeeds, resource-looking text remains ordinary text rather than being underlined with a promise the host cannot keep.
- Provider matcher precedence and whole-match behavior remain `resourceview.ReferenceForLocator`/`contentlink` rules. The remote host chooses among its instances; the viewer does not guess or merge local and remote matchers.
- Every content-pane model retains its `SourceContext`. Its rendered-link hit regions and open handlers use that context, so a nested issue/file/resource click stays on the originating host.
- Session activation carries the source `HostID` and matches `(HostID, tmux session)`. A remote output link cannot attach a local twin, and a local output link cannot jump remotely merely because a host has the same name.
- URLs remain the deliberate exception: after the shared safety check, the viewer opens them locally.

### 5. Live truth through conditional visible-pane refresh

A one-time remote load is not parity. Use bounded conditional reads only for visible active remote tabs, keep the last good body while a refresh is in flight or fails, and preserve the local `livewatch` path unchanged.

- At most one conditional read is in flight per visible pane. The remote refresh scheduler offers a tick through the model's existing `Observe` then `Refresh(false)` gate; the injected remote loader sends the model's previous source revision and receives either `notModified` or a new typed payload.
- A `notModified` response completes the model's refresh bookkeeping without repainting. A changed response is converted once into the model's existing refresh-result message and applied through its no-change and scroll-preservation path. The scheduler must not preflight with one remote call and then trigger a second load.
- Overview's `livepanes` lifecycle remains the authority. A small remote scheduler binding sits beside the local filesystem binding, or the shared layer is generalized only as far as `visible target -> refresh opportunity`; remote paths never enter local `livewatch`.
- Document revisions derive from authoritative file metadata and content identity, issue/note revisions from the owning td store, and diff revisions from the worktree/git state needed by the current operation.
- The conditional read is the model refresh command, preserving request generations, no-change fingerprints, scroll, and tab state. The scheduler never applies content directly or launches a second refresh after a changed answer.
- Resource panes retain their existing provider freshness/manual-refresh contract. Provider descriptors use their own conditional revision; no filesystem watch is invented for a network resource.
- Hiding, closing, switching rows or hosts, or losing the host incarnation cancels pending work. Hidden tabs perform no remote checks.
- Measure steady-state latency, process count, and CPU with one, four, and eight visible panes. Batch conditional reads per host if measurements justify it before considering a resident session.

While the remote row still exists, a connected-stale host or content-verb failure keeps the last successfully loaded body visible with a host-stale/error indicator and leaves the terminal channel alone. Recovery checks only currently visible tabs and never fans out work for hidden panes. If a host becomes disconnected, disabled, or otherwise non-showing, the existing Sessions row/preview reconciliation remains authoritative and may release or rebind the preview; this plan does not retain disconnected workspace rows, and it must never preserve the body by rebinding it to a local or different-host source.

### 6. Fencing, refusal, and version skew

Every remote answer is accepted only when all of these still match:

- registered `HostID` and registry client incarnation;
- unscoped remote project/workspace identity validated by the host;
- Overview preview generation and host-scoped workspace ID;
- content deck tab ID, request generation, surface epoch, and expected reference;
- exact `RunSidecar` invocation generation and the content source generation that issued it.

A same-ID host retarget, selected-row change, closed/reopened tab, superseding click, or newer content invocation makes the old answer inert. Equal paths and equal td IDs do not substitute for identity.

Refusals are specific and actionable:

- Missing content capability: `Update Sidecar on <host> to open files and issues from that host.`
- Missing `ContentReadV1`: say to update Sidecar on the named host. Version strings may be shown for diagnosis but are display-only and never used to infer ordering. A host that advertises the capability but returns an incompatible result is a contract failure; an intentional incompatible evolution requires a new additive capability.
- Host disconnected, disabled, or unavailable: refuse the request and let existing Sessions row/preview reconciliation own teardown; never rebind the content locally. A stale host is still connected and gets the same bounded attempt as current `RunSidecar`; if that attempt fails while the row remains, mark only the content stale and keep its last body.
- Workspace replaced or root escaped: refuse as stale identity; never retry against the raw path.
- File/diff/issue/note not found: render the existing pane-shaped error from the host's authoritative lookup.
- Provider unavailable/config/auth/rate-limit failures: preserve the typed provider code, retryability, and setup hint after sanitization.
- Oversized, malformed, or stdout-contaminated payload: fail the one pane/request without partial decoding; keep the terminal and other panes usable.

## Work sequence

### Slice 0 — characterize and pin the regression — **done** (td-650c7a)

- Add a Sessions test with a remote row whose `Path` also exists locally but contains different content. Prove the current guard refuses rather than showing the local twin.
- Pin all `targetactivation.PlanKindsFromSpans` against local and remote dispatch coverage.
- Pin local content loads and local provider matching as negative controls.

### Slice 1 — source identity and the Document seam — **done** (td-fff4bc, td-b83111)

- Add `SourceContext`, thread it through `contentpanes.SurfaceContext`, deck tabs, nested-link handlers, cached Sessions pane state, and live bindings.
- Extract only Document resolve/load/refresh behind the source seam and bind the local adapter to today's functions.
- Keep every remote kind refused. Focused Document tests must remain byte/behavior compatible before any wire code lands; issue, note, diff, and provider seams arrive only with their user-facing slices.

### Slice 2 — the minimum content service for files — **done** (td-5c259b)

- Extract the read-only file resolve/read application service with its strict Document DTO, encoded-size bounds, structured errors, and conditional revision contract.
- Add only the `sidecar content resolve/read` operations needed by the file journey, `ContentReadV1` capability advertisement, and the `hosts.RunSidecar` Document source adapter. Do not front-load all-kind DTOs or loader refactors.
- Add local/direct versus remote/JSON file-contract tests, `ValidRemoteResult`, contamination, encoded oversize, cancellation, timeout, nonzero exit, and old-host capability tests. Do not invoke a content verb before first frame or merely because a host is registered.

### Slice 3 — file steel thread — **done** (td-30e99a, td-9d8bd9)

- Make remote file fresh-resolution and bounded loading use the remote adapter.
- Remove the blanket remote refusal from `previewDeckContext` only for a compatible content source whose host is not disconnected, disabled, or unavailable; stale remains attemptable.
- Open a Document pane through the ordinary deck, honor line targets, preserve scroll/tab behavior, and show host provenance.
- Add conditional remote document refresh through the existing model refresh path.
- Before admitting remote Documents, gate inline edit, `ctrl+p` file finding, and `f` project search with explicit remote-not-supported messages; retain in-document search because it uses the loaded body. Audit every other Document action for local I/O.

This is the first integrated proof: remote terminal text -> click -> host containment/bytes -> local shared Document pane -> remote edit on the host -> conditional change detection -> same pane refresh. No other content kind is required to land before this journey works end to end.

### Slice 4 — td issues and notes — **done** (td-a4dd72)

- Add issue/note methods and DTOs to the content service and source interface only in this slice.
- Run issue/note loaders and td-store revision resolution on the host with the existing read-only td environment.
- Use the remote config for cross-project issue candidates; adopt and badge the remote owning project exactly as local cards do.
- Bind parent/child/sibling and nested note/issue links to the same source context.
- Before admitting a remote Issue pane, hide or explicitly refuse `Open in td` and audit every Issue/Note action so none switches to or invokes a viewer-local tool against remote identity.
- Prove conditional live refresh for visible issue/note panes without checking hidden tabs.

### Slice 5 — git diffs — **done** (td-b11294, td-ea957f)

- Add diff methods and bounded operation DTOs to the content service/source, then move git process execution behind that seam while keeping `workspacediff.View` as the only renderer/state machine.
- Resolve specs and load working tree, commit, range, selected commit-file, full-file page, and cursor-driven detail operations on the host.
- Audit every Diff action before enabling the remote source; any mutation or local-command path is hidden or explicitly refused in this slice.
- Preserve current diff size and untracked-file bounds, add truthful per-operation truncation/paging, and refresh visible panes through conditional git revisions.

### Slice 6 — remote resource providers — **done** (td-56c5d2, td-ccb1ad)

- Add provider describe/resolve DTOs only in this slice. Describe returns a deterministic fingerprint of validated ordered descriptors, never a process-local generation. Publish a host-scoped matcher snapshot and conditionally re-describe on row selection, explicit reload, the bounded visible-row cadence, or host-incarnation replacement.
- Resolve through the remote manager, preserving matcher precedence, timeouts, process-group cleanup, typed errors, safe Markdown, source URL validation, and freshness.
- Add the bounded viewer-side remote resource cache keyed by host/incarnation/descriptor fingerprint/provider instance/reference. Thread the model's manual-refresh intent through the source so refresh bypasses and replaces the cache while ordinary reloads honor provider freshness.
- Audit Resource actions before enabling the remote source; validated HTTP(S) source URLs still open locally, while no action may reuse a viewer-local provider instance.
- Prove that a provider configured only locally never claims remote text and one configured only remotely does.

### Slice 7 — session links, refusals, documentation, and full proof — **done** (td-eaed0d)

- Scope session-link attachment by source host.
- Verify the per-kind action audits landed in the same slices that admitted those kinds. No action may launch a local command against remote identity.
- Complete capability/refusal UX, host provenance, nested-link parity tests, and stale/reconnect behavior.
- Update the CLI/reference and remote-host website docs only for the internal content capability, user-visible supported clicks, version requirement, and explicit inline-edit limitation.
- Add an isolated demo/proof recipe that can use the existing `remote-spike.sh` harness or a loopback SSH host without touching either default tmux server or real state tree.

## Acceptance and proof matrix

### Contract and unit evidence

- Local adapter tests prove local file, issue, note, diff, resource, URL, and session behavior does not change and starts no SSH/content command.
- Content service and CLI tests cover every kind and operation, local/JSON equivalence, cancellation, per-operation encoded response bounds, structured oversize/paging, `ValidRemoteResult` on every response envelope, malformed/contaminated stdout, typed errors, capability absence, timeout, disconnect, stale-host attempts, and recovery.
- Host validation tests refuse an unconfigured project, replaced shell/worktree, relative traversal or symlink escape, control characters, unknown kind, arbitrary command text, and oversized locator/spec/path; a deliberate absolute regular-file target retains the existing local rule.
- Async tests discard replies after row switch, tab close/reopen, host retarget with the same ID, invocation cancellation, and newer request generation.
- Parity tests cover every `PlanKind` on local and remote Sessions, plus nested links from every remotely loaded pane kind.
- Action audits prove remote Documents cannot run inline edit, file finder, or project search locally; remote Issues cannot open the local td plugin; and every admitted pane kind has no remaining viewer-local I/O path. In-document search and validated browser URLs remain available.
- Live tests prove only visible active remote tabs are checked, changed results reuse the local refresh path, unchanged revisions produce no visible churn, a connected-stale/content-verb failure retains the last body, disconnected/disabled row teardown follows existing reconciliation without local rebinding, and all timers/processes end on hide/close/shutdown.
- Provider tests cover deterministic descriptor fingerprints across fresh one-shot processes, immediate row-selection/manual reload, unchanged revision, bounded visible-row cadence, hidden-row silence, ordinary result-cache hits, explicit refresh bypass/replacement, expiry, descriptor change, and host-incarnation cache invalidation.

### Real user journey

Run against a real second machine or loopback host with both axes isolated on both ends: private tmux sockets, private Sidecar state/config/cache trees, explicit `SIDECAR_ISOLATED_STATE=1`, and pre/post hashes of real state plus default-server session/PID checks.

Prepare deliberately conflicting data so local fallback cannot masquerade as success:

1. The viewer and host have the same project path and filename with different marker text.
2. The host has a td issue/note absent locally, a dirty git change absent locally, and a resource provider instance/config absent locally.
3. Local and remote tmux servers have a same-named session.

Then prove through the actual Sessions TUI with `tmux-drive.sh` and real SGR clicks:

1. Select the remote shell and click the file reference. A Document pane opens with only the remote marker and correct target line.
2. Change the file on the host. The same pane refreshes inside the documented conditional-refresh window while retaining tab/focus/scroll semantics.
3. Click the remote td issue and note. Their cards show remote-only data; a remote td update refreshes them; a nested issue link remains on the host.
4. Click a scanner-supported remote commit/range/revision reference. The Diff pane shows the host-only data. Separately open or restore a working-tree Diff pane and prove its host-only change refreshes after a host commit/index change; no unrecognized working-tree terminal token is implied.
5. Click the provider key. Only the host provider claims it, and its sanitized document, typed failure behavior, manual refresh, and validated source URL match a local Sidecar on that host.
6. Click the same-named session link. The remote row becomes interactive; the local twin is untouched.
7. Make only the content verb time out or become unavailable. The terminal stays live and interactive, loaded content becomes honestly stale, and recovery checks visible tabs without reopening hidden tabs.
8. Run against an older remote Sidecar. Terminal observation still works and the click gives the explicit update requirement; no local I/O runs.

Finish with `go test ./... -count=1`, focused race tests for request/refresh lifecycle, `go build ./...`, `git diff --check`, startup tracing showing no new pre-frame phase, measured one/four/eight-pane refresh cost, and an independent review of the service boundary, containment, cancellation, fencing, provider sanitization, and two-machine proof.

## Rejected alternatives

### Resolve remote paths locally

This is the current bug. It can fail closed on one machine and silently show the wrong checkout on another. Path equality is not host identity.

### Mount the remote checkout or require SSHFS

This makes correctness depend on an external mount, translates only filesystem reads, and still leaves td, git, provider configuration, credentials, change signals, and session identity on the wrong machine.

### Add requests and content payloads to `host serve`

That couples inventory health to interactive content load, enlarges the protocol whose one-way/read-mostly shape is already proven, and lets a slow provider block the stream that tells the user whether the host is healthy. Independent bounded invocations keep the failure domains honest.

### Add a resident multiplexed content session now

The existing SSH ControlMaster already amortizes connection setup. A second resident application protocol adds request IDs, backpressure, reconnect, cancellation, and version negotiation before measurements prove those costs buy a perceptible improvement. Begin with bounded one-shot verbs and conditional visible-pane checks; cross to batching or a resident transport only against an explicit one/four/eight-pane latency and resource exit gate.

### Server-render pane text

Shipping rendered ANSI would duplicate theme, width, scroll, tab, search, selection, and pane state on the host. Typed content keeps the existing viewer models as the only presentation implementation.

### Make all Sidecar plugins remote-aware

The reported journey is opening references from a remote Sessions terminal. A remote project browser for Files/Git/Tasks is a larger product decision and not required to make this click truthful.
