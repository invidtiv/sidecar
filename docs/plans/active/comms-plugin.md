# Comms protocol plugin and shared live refresh

**Status:** Proposed; implementation has not started.

**Created:** 2026-09-07

**Planning task:** td-aa3741. This task covers the plan, not the implementation milestones below.

**Controlling plan:** This document controls the Comms viewer and the shared Sidecar changes it requires. Comms owns its executable, API, and change signal; Sidecar owns the browser and refresh lifecycle. Implementation tasks in either repository should link here rather than create a competing plan.

## Outcome

Open Comms as a Sidecar tab or a resource pane beside an agent terminal, see recent inter-agent messages, filter by topic or search their contents, and read a selected message with its thread and receipts. Messages and receipt changes appear without a manual refresh. Incoming activity preserves the message and scroll position the user is reading. Leaving the surface stops its background work; returning makes it current again.

This is a viewer of messages published through Comms. It does not import the private conversation history between an agent and its harness, replace Sidecar's Conversations plugin, or change agent lifecycle detection. Comms remains short-lived coordination data with its existing retention and identity semantics.

The first release is read-only and uses the existing `sidecar.plugin/v1` protocol. It is a real Comms client, not a wrapper around comms-web or an embedded web page. The shared host improvements must benefit Recall and other protocol plugins without a Comms-specific branch in Sidecar.

## Reading order and related contracts

1. This document: product scope, decisions, delivery sequence, and acceptance criteria.
2. [Plugin protocol](../../reference/plugin-protocol.md): the frozen wire contract, bounds, and freshness mechanisms.
3. [Plugin authoring guide](../../guides/active/creating-plugins.md): external executable setup and conformance workflow.
4. [Implemented plugin ecosystem plan](../implemented/plugin-ecosystem/README.md): architecture and existing delivery history. Its shipped work stays owned there; the additions here are a separate follow-on.
5. [Demo environments](../../guides/active/demo-environments.md) and [headless testing](../../guides/active/headless-testing.md): isolated consumer proof.

Sibling repositories are `~/code/comms`, `~/code/comms-web`, and `~/code/recall`. Read each repository's current instructions before changing it. The paths below describe the inspected source on 2026-09-07 and must be rechecked at implementation start.

## Current implementation and the gaps this plan closes

| Area | Verified source | Consequence |
| --- | --- | --- |
| External plugin transport | `internal/pluginhost/{runner,pluginmanager,plugindescribe,pluginpage}.go`; `docs/reference/plugin-protocol.md` | One request and one response per executable invocation. No resident plugin process or streaming response. `describe` is local, fast, and network-free. |
| Recall reference | `~/code/recall/internal/cli/sidecarplugin.go` and its tests | A tool can add a transport subcommand that projects its own domain into Sidecar's vocabulary. Comms can use the same integration shape without sharing Recall's retrieval logic. |
| Shared browser | `internal/pluginbrowser/{model,keys,pane,plugin,detail}.go` | Tables, search, filters, documents, timeline sections, forms, and navigation already exist. The standalone tab and pane leaves share the browser model. |
| Freshness declarations | `internal/pluginhost/vocabulary.go`, `plugindescribe.go` | `refresh.everySeconds` is clamped to 15–900 seconds. `refresh.watch` accepts bounded paths under the user's home, excluding home itself. |
| Automatic refresh bindings | `internal/plugins/workspace/live_panes.go`; `internal/overview/live_preview.go` | Resource panes have watch and timer scheduling. Their duplicated scheduling chooses the shortest visible interval and refreshes the visible resource set, which needs more precise dispatch when plugins have different schedules. |
| Standalone tab gap | `internal/pluginbrowser/plugin.go`; `internal/app/{pluginbrowser,scope}.go` | No matching automatic watch/poll binding was found for the standalone protocol tab. Focus currently calls `Refresh()`, which can keep an already-loaded collection without re-listing. |
| Explicit invalidation | `internal/app/pluginbrowser.go`; `internal/pluginbrowser/pane.go` | `sidecar plugin changed` reaches standalone tabs as well as panes. Global hosts receive broadcasts whether visible or hidden; visibility must be enforced before background calls. |
| Reading position | `internal/pluginbrowser/model.go: applyListed`; `pane.go: restoreCursor` | A replacement page resets cursor and scroll. The stable-ID restoration path is for restored tabs, not continuous refresh. |
| Refresh errors | `internal/pluginbrowser/model.go: applyListed` | A failed replacement request clears the row set. A live monitor should retain last-known results with an explicit stale/error state. |
| Comms read API | `~/code/comms/internal/httpapi/httpapi.go`; `internal/app/app.go` | Observe, search, message peek, thread, receipts, agents, and topics already have HTTP operations. Observe accepts a topic but no author filter. |
| Comms storage ownership | `~/code/comms/internal/service/service.go`; `internal/store/` | `comms serve` is the sole SQLite owner. The new plugin must use the existing HTTP client over the Unix socket. |
| Comms notifications | `~/code/comms/internal/app/{app,events}.go` | Agent and message wait operations use in-process coalesced notifications. These are not an all-change observer feed: receipts and every other visible mutation are not all covered. |
| Web reference | `~/code/comms-web/src/routes/api/{data,events}/+server.ts`; `src/lib/receipts.ts` | The web backend polls observe every 1.2 seconds and forwards over SSE; receipts have a separate four-second refresh. It is a UX reference, not a transport dependency. |
| Environment | `internal/pluginhost/env.go` | `COMMS_SOCKET`, `COMMS_STATE_DIR`, `COMMS_AUTO_START`, and `XDG_RUNTIME_DIR` are not inherited unless explicitly listed in `passEnv`. `XDG_STATE_HOME` is in the base allowlist. |

These findings establish a good fit for the content vocabulary and incomplete live-refresh generalization. This plan completes that lifecycle for all three placements rather than building a second Comms browser.

## Product scope

### Included in the first release

- One `Messages` collection with newest-first ordering, optional text search, and an optional topic filter accepting a Comms topic name or stable ID.
- An all-topics default over the operator-visible Comms observe API, including direct-topic traffic that the API returns. This is not an agent's personal inbox and has no implied privacy boundary.
- Rows with message title, author, topic, timestamp, and a short body excerpt. Stable Comms IDs remain the identity even when a display label changes.
- A detail document with full selected-message body within protocol bounds, message/author/topic identity, a bounded recent thread timeline, and a restrained receipts section.
- Standalone Comms tab, project workspace resource panes, and global Sessions resource panes using the same browser and freshness policy.
- Fast local change-driven refresh where the Comms notification path is watchable, plus visible-only 15-second reconciliation polling everywhere the local Unix-socket client works.
- Explicit refresh, honest empty/error/stale states, cancellation, stable reading position, bounded pagination, and recovery after Comms restart.
- A reproducible, ephemeral demo with conversations generated by isolated Comms identities.

### Deferred

- Compose, reply, read-through, join, follow, retire, purge, or any other message/domain mutation from Sidecar. The plugin declares no actions; viewing never changes subscription cursors or creates an observer identity.
- A custom three-column chat layout, presence sidebar, portraits, unread badges, arbitrary widgets, inline composer, or automatic scrolling to incoming messages.
- Separate Topics and Agents collections, a dynamically populated topic picker, an author filter, and a thread-only collection. Add these only as coherent API-backed journeys; do not filter one fetched page client-side and present it as a complete result set.
- Remote Comms routing, remote notification transport, TCP/auth configuration, and aggregation across daemons. A local observer can still see messages that remote agents published to that same Comms service.
- Harness transcript import, task-state storage, archival, notification sounds, or waking agents.
- Changes to the frozen Sidecar plugin protocol, a lower polling floor, resident plugin processes, or a plugin SDK dependency shared across repositories.

## Architecture and ownership

```text
Comms CLI / HTTP producers
          |
          v
Comms application service ----> sole store adapter
          |
          | post-success invalidation (no message content)
          v
Comms service-owned revision marker
          |
          | filesystem signal + periodic reconciliation
          v
Sidecar shared freshness lifecycle
          |
          | one-shot describe / list / get
          v
comms sidecar-plugin ----> existing Unix-socket HTTP API
          |
          | rows and documents
          v
Sidecar shared browser ----> tab / workspace pane / Sessions pane
```

The marker is an invalidation hint, never an alternate store, message log, delivery acknowledgment, or authoritative version of the messages. A changed marker tells a client to ask the API again. Comms must not know Sidecar instance names, invoke `sidecar plugin changed`, or depend on Sidecar being installed. The existing explicit-change command remains available for diagnostics and other plugins.

### Repository responsibilities

| Repository | Owns | Must not acquire |
| --- | --- | --- |
| Comms | Plugin wire projection, bounded HTTP queries, socket resolution, post-success change notification, service-side marker writer, help and conformance tests | Sidecar UI types, tmux control, Sidecar configuration writes, a second SQLite owner |
| Sidecar | Shared refresh eligibility/scheduling, browser state preservation, error presentation, bindings for all three placements, demo and host proof | Comms message semantics, Comms database paths/layout, provider-specific rendering rules |
| comms-web | Existing web experience used for reference | No changes required by this plan |
| Recall | Existing external plugin used for compatibility proof | No changes required unless a host regression is found and repaired in Sidecar |

Use narrow interfaces at the two real seams: a Comms HTTP reader consumed by the projection, and a service-owned change sink driven by application notifications. Keep wire conversion pure where practical. Do not link Sidecar's internal Go packages into Comms or shell out to the Comms CLI from the Comms plugin itself.

## Comms plugin contract

### Entry point and setup

Add `comms sidecar-plugin` to the CLI's discoverable command/help path. It reads one bounded JSON request, respects `deadlineMs`, writes exactly one `sidecar.plugin/v1` JSON response, and exits zero for either typed success or typed failure. Unknown methods, malformed parameters, and unsupported protocol identifiers produce typed errors. Diagnostics never go to stdout.

The supported deployment target is frozen-v1 Sidecar. Pre-freeze host support is not required; do not inherit Recall's historical identifier compatibility accidentally.

Example proposed configuration using the existing Sidecar config shape:

```json
{
  "plugins": {
    "external": [
      {
        "id": "comms",
        "command": ["comms", "sidecar-plugin"],
        "enabled": true,
        "placements": ["tab", "panes"],
        "passEnv": ["COMMS_SOCKET", "COMMS_STATE_DIR", "COMMS_AUTO_START", "XDG_RUNTIME_DIR"]
      }
    ]
  }
}
```

Reuse Comms's current socket resolver and readiness/lifecycle policy for `list` and `get`, with the host deadline bounding the entire operation, including preflight. Preserve explicit socket and no-auto-start overrides, including `COMMS_AUTO_START=0` passed through the plugin environment; never add a second daemon launcher. Auto-start/upgrade behavior must obey existing ownership rules for auto, supervised, and foreground daemons. `describe` performs no handshake, starts no daemon, and creates no directories.

Do not inherit `COMMS_AGENT_ID` or `COMMS_CONTEXT` for ordinary viewer operation. It is a read-only operator surface and needs no agent identity. Verify the command's dispatch path does not resolve/create a default identity as an incidental CLI startup effect.

### `describe`

Return local identity/build metadata and one stable collection ID, `messages`, with `search: optional`, `detail: true`, no actions, and no matchers in the first release. The first filter is `topic`, a text filter with an empty default meaning all topics. Declare no sort choices until a second ordering is actually implemented; newest-first is the single ordering.

Declare `project` context solely so a request on a remote-bound project can be rejected honestly. Local project context does not silently scope the feed. Labels and documentation must say that Comms shows the selected local service across projects. If `context.project.hostId` is non-empty, return typed `unavailable` naming that host; never interpret its remote paths as local paths.

Declare `refresh.everySeconds: 15`. Also declare the notification directory described below under `refresh.watch` when its normalized path satisfies the host's home-directory restriction. If it does not, omit the watch path and keep polling; never emit an invalid describe just to ask the host to accept an unsupported location.

Dynamic topics and agents are data, so do not fetch them in `describe` to manufacture choice lists. The topic text filter avoids a network-bound declaration and a fixed-choice limit.

### `list`

| Request state | Comms operation | Semantics |
| --- | --- | --- |
| Empty query | `GET /v1/observe?topic=...&limit=...&cursor=...` | Operator-visible messages, newest first |
| Non-empty query | `GET /v1/search?query=...&topic=...&limit=...&cursor=...` | Comms's existing search semantics and ordering |

Use stable `msg_...` IDs as row IDs. Columns are title (primary), author, topic, created timestamp, and excerpt (secondary). Blank titles fall back to a short deterministic body excerpt or the ID. Excerpts are plain sanitized text, never ANSI or an alternate rendering program.

Resolve author/topic labels with bounded metadata reads, not one subprocess or HTTP round trip per message. It is acceptable to show stable IDs when the bounded metadata page does not include an identity. Label enrichment failure must not discard successfully loaded messages or claim there were no matches. Do not store a second persistent index or unbounded process-independent cache.

Pass API continuation cursors through a small plugin cursor envelope bound to operation, normalized query, and topic. Reject a cursor reused under different parameters. Preserve Comms ordering and limits; do not post-filter a page to implement an undeclared query capability. Do not invent a `total` from the count of the fetched page. Audit the host's unknown-total presentation before declaring conformance.

Successful nonempty results use `answered`; successful empty results use `abstained`. Transport/API failure is an error or a `failed` page with an explanatory notice, never a successful empty collection. If row-set completeness is affected by a partial read, use `degraded` with coverage that explains the missing source. Cosmetic ID-label fallback alone is not a claim that the row set is incomplete.

The plugin may use a bounded notice to report polling-only freshness when the notification directory is unsupported or unavailable. Do not claim a fast live connection merely because the daemon is reachable.

### `get`

Fetch the selected message through `GET /v1/messages/{id}`. Once its identity is established, fetch its thread and receipts within the remaining shared deadline, with bounded concurrency. Use the existing API; viewing must never call `read-through`.

Represent the selected message as the document body. Put metadata in fields and thread/receipt content in the existing section vocabulary:

- Selected message: canonical ID, title, author identity, topic identity, created time, and Markdown body.
- Thread: request `latest=true` with a bounded limit, then render that returned tail in chronological order. Show the selected message separately even if it predates the fetched tail. Explicitly label omitted older replies when the API has another cursor. Never imply that a bounded tail is the complete thread.
- Receipts: short “Read by” summary with the API's actual read timestamps and bounded detail for reported recipients. Zero recipients means “No receipt recipients reported.” It does not mean nobody read the message. Unread, unavailable, and absent are distinct states.
- Partial failure: if the message loaded but thread or receipts failed, keep the body and make the affected section say it is unavailable. The one-shot plugin cannot pretend it retained a previous receipt snapshot. Whole-document refresh failure can retain the host's previous document, explicitly marked last known.

Use `updatedAt` only for an actual source timestamp with that meaning; do not stamp the fetch time as the message's update time. Set an appropriate `freshForSeconds` hint, but remember the host clamps it to at least ten seconds. Change-driven and return-to-visible refreshes must explicitly bypass the get cache.

Respect the protocol's response, body, section, and timeline bounds before serialization. Bound HTTP response decoding as well as emitted JSON; a large upstream receipt list must not create unbounded memory use before projection. A long thread or receipt set must be visibly bounded, not trigger oversize stdout rejection. An oversized optional subsection can report unavailable/truncated without discarding a successfully decoded selected message.

Follow the authoritative peek response for retention: Comms can still return an individually expired message while another member keeps its thread live. Do not turn `expires_at` into a host-side deletion rule. Only an API `not_found` makes the selected document unavailable. No source URL is manufactured because no stable comms-web deep-link contract is required here.

## Fast refresh without changing the plugin protocol

### Comms-owned invalidation marker

Add a small public local-client notification contract to Comms. For a Unix socket at `/path/comms.sock`, its dedicated notification directory is `/path/comms.sock.events`, containing `revision.json`. Resolve this path in one pure helper used by both the daemon and plugin; do not infer it from the SQLite database filename. This separates instances naturally when they use different sockets.

The marker contains only a schema identifier, server instance identity, and monotonic revision within that instance. It contains no messages, identities of participants, read receipts, filesystem corpus, or secrets. A restarted daemon changes the instance identity even if its revision starts over. This is an observable invalidation token, not a cursor suitable for replay or proof of completeness.

The daemon creates the directory with owner-only permissions and atomically replaces the marker from a bounded, service-owned writer using private temporary files inside that validated directory. An existing path must be a real, same-user-owned private directory; do not follow a symlink, accept a permissive/foreign directory, chmod unrelated content, or recursively clean up an arbitrary path derived from user configuration. An unsafe marker target disables that notification sink with a diagnostic while leaving the API available. Reuse the service's socket/process ownership guard so two instances cannot publish competing revisions beside the same socket. Watch the dedicated directory so atomic replacement remains observable without depending on a file inode. Do not watch SQLite, WAL, SHM, the entire state tree, or a busy socket directory.

Drive the writer from a broad application change signal after successful operations. Audit every public mutation path: agent registration/update/retirement, topic creation/ensure/update/archive, follow/unfollow, publish/direct/reply, read-through, and purge. Existing agent/message wait notifiers remain scoped to their existing predicates; add a broad observer signal rather than making their meanings ambiguous. A redundant invalidation after an idempotent success is acceptable; an invalidation before a failed transaction is not.

The application signal has no filesystem knowledge. After successfully acquiring the Unix listener, service lifecycle code subscribes and starts the marker writer before serving requests, writes an initial revision, and coalesces mutation bursts. On shutdown, stop accepting mutations, stop/join the writer, and release its subscription before releasing socket ownership. Leave the directory and last marker in place; startup replaces the marker after claiming the listener, and readers distinguish incarnations through the server instance. Never let a departing process delete or overwrite a successor's notification state. Use a short bounded coalescing window, initially about 200 ms with a maximum flush delay, so continuous writes cannot postpone the marker forever. Do not make a committed publish fail because writing a notification file failed. Log a bounded actionable service diagnostic, retry without a tight loop, and preserve API correctness while polling provides reconciliation.

Explicit expiry is also time-dependent: a message may become invisible without any write. The 15-second reconciliation poll remains enabled even when watching works, so expiry, missed events, marker-write failure, and older daemons do not leave a permanently stale viewer.

This is deliberately a new Comms-owned local capability. Document the marker schema, path derivation, best-effort semantics, restart behavior, and permissions in Comms. It requires no new HTTP operation or database migration. TCP-only services do not acquire a marker path through this plan.

### Watch eligibility and startup races

The frozen plugin host accepts watch paths only under the viewer's home directory. Standard home-based Comms sockets therefore get fast updates. A socket under `/tmp`, `/run`, or an external `XDG_RUNTIME_DIR` still works through the API, but uses the 15-second fallback. Keep that limitation explicit; do not widen the protocol's path policy in this feature.

The directory may not exist when `describe` runs, especially before an auto-started daemon is ready. Declare an eligible intended path without creating it. When reporting notification availability, a plugin must validate a bounded marker's schema and server instance against the successful API handshake; an abandoned marker from an older daemon is not proof that the current service emits notifications. The shared host must re-probe missing targets on a bounded cadence while visible, including after successful reads and fallback ticks, instead of caching absence forever until a new describe generation. All probes run in commands, not `Init`, `Start`, `Update`, or `View`.

Do not broadly watch an ancestor just to discover the missing directory. Re-probing on the fallback interval is sufficient. First entry always fetches immediately; the fast watch attaches once the daemon has created its directory. Marker replacement, directory removal/recreation, and service restart must all recover without restarting Sidecar.

## Shared Sidecar freshness lifecycle

### One policy, three bindings

Extract the plugin-specific watch-target preparation, per-target timer deadlines, eligibility, coalescing, and refresh debt into a small shared component adjacent to `pluginbrowser`. Reuse `livepanes`/`livewatch` for watcher lifecycle and signals; do not build another filesystem watcher. Bind it once in each placement:

1. Standalone protocol tab hosted by `internal/app` and `pluginbrowser.TabPlugin`.
2. Project workspace resource panes in `internal/plugins/workspace/live_panes.go`.
3. Global Sessions resource panes in `internal/overview/live_preview.go`.

The component consumes cached visible-browser descriptors and emits refresh commands through the existing `Calls` seam. Surface bindings answer which browsers are actually visible and whether interaction temporarily suppresses refresh. They do not implement different scheduling policies. Keep content refresh separate from `paneframe`, which owns pane presentation and geometry.

### Lifecycle rules

- Render a loading state first. Describes, filesystem probes, watcher construction, and process calls stay off the first-frame and render/update paths.
- Visibility means on screen, not merely instantiated, focused at some earlier time, or having a saved tab. Unfocused panes that are still visible remain eligible; hidden global tabs and off-screen Sessions rows are ineligible.
- On entry or return, re-list the visible collection and force re-fetch of its visible detail. Do not rely on `ensureListed` to decide that a previously loaded page is still current.
- A watch signal refreshes only browsers whose declared targets it invalidates. A timer refreshes only browsers whose individual interval is due. A plugin declaring 120 seconds must not be polled every 15 seconds because a neighboring plugin requests 15.
- Deduplicate identical watch registrations. Overlapping requests can use the existing host caches/coalescing seams, while every result remains addressed to the correct browser and generation.
- A background change while a call is in flight marks one refresh owed. Let the current bounded request finish and run one follow-up if necessary; repeated signals must not perpetually cancel slow calls. User changes of query/filter/selection still cancel superseded work immediately.
- Modals, query editing, selection gestures, and other existing refresh vetoes retain debt. Run it promptly when the veto lifts. Do not overwrite the user's in-progress query or clear a selection mid-gesture.
- Hiding, closing, switching context, disabling the plugin, or shutting down unregisters targets, invalidates pending ticks, and cancels unnecessary calls. Late results cannot redraw a hidden or repurposed browser. A watcher constructed after its owner disappeared is stopped, not leaked.
- Route `ChangedMsg` through the same visibility and debt rules. Explicit invalidation reaches all eligible placements, but does not cause hidden standalone tabs to make network calls.
- Compare semantic page/document content before replacing rendered state. An unchanged reply should not reset selection, scroll, or cause a full content rebuild. Do not include observation time in the equality key.

Keep the existing 15–900-second protocol clamp. This work changes host lifecycle correctness, not the executable invocation contract.

## Reading position, pagination, and failure behavior

### Separate refresh intent from query replacement

The shared browser must distinguish initial load, explicit query/filter change, pagination append, automatic refresh, and explicit user refresh. These are host-side intents, not new protocol methods. A new query intentionally starts a new result set; a background refresh preserves the user's place in the existing one.

For an unpaged list, capture the selected stable ID and viewport offset before requesting background refresh. If the selected row remains in the new page, restore both as far as the new geometry permits. Keep the selected detail tied to that ID while new rows appear above it. Preserve detail scroll across a same-ID refresh, clamping only when the content shrinks.

If the selected row no longer appears in the refreshed first page, its absence is not proof of deletion: newer messages may have pushed it out. Keep the current view and buffer at most one replacement first page, with a generic “Updated results available” control. Re-fetch the selected document independently to distinguish retained older content from `not_found`.

When the user has loaded older pages, do not replace the whole list with the latest first page, silently discard older rows, or append using a cursor from a different query. Keep the loaded view stable and buffer the new first-page result. The explicit refresh control applies the buffered page or fetches a fresh first page, resets pagination deliberately, and preserves the selected ID when present. If that ID is absent, select the first available row and update its detail as part of that explicit user action. Keep one bounded buffered page, not an unbounded incoming-message queue.

The first release has no automatic follow mode. The user can explicitly return to latest results; arrivals never force that decision. Selection stability, buffered-result application, and error behavior are generic browser features available to every plugin, with keys assigned through the existing keymap rules.

### Errors and retention

- Initial load failure shows an error with Retry and no claim of an empty service.
- A background read failure retains the previous successful page/document, marks it last known, and exposes the failure. A later success clears the stale state.
- A successful empty response replaces the list with an honest empty state. This differs from a transport failure.
- A selected message the API reports as `not_found` becomes an explicit unavailable document state. Retained text, if shown, is labeled last known. An expired ancestor that the API still returns for an active thread remains readable; the host does not override Comms's retention rule.
- A thread or receipt subsection can fail while the message body remains valid. Represent the subsection's failure without fabricating zero replies or zero readers.
- A changed query/filter cannot inherit the previous scope's rows under the new scope label while loading or on error. Preserve old content only when its scope remains explicit; otherwise show the new scope's loading/error state.
- New page data and old in-flight detail results are fenced by browser identity, generation, selected ID, and query/filter context. The detail title and selected row must never disagree because of a late response.

## Delivery sequence

Create implementation tasks when work starts. Each task should name its owned files, dependencies, consumer proof, and actual reviewer. The planning task above must not be used to mark the feature implemented.

### M0: Freeze the small consumer contract

**Ownership:** Comms plugin projection/tests and the controlling plan.

Confirm exact JSON envelopes and current CLI dispatch/readiness behavior with read-only calls against an isolated Comms service. Capture canonical describe/list/get fixtures, including empty results, a reply thread, receipts, and a missing message. Check host behavior for unknown total, document bounds, and remote context before writing the adapter around assumptions. Update this plan if live source contradicts an inspected contract.

**Exit evidence:** Frozen collection/filter IDs and fixtures derived from actual API responses; no new daemon, agent, or data in the user's live service.

### M1: Read-only Comms plugin through the real Sidecar host

**Depends on:** M0.

**Ownership:** `~/code/comms/internal/cli/sidecarplugin.go` and focused projection helpers/tests; Comms help registration and documentation.

Implement `describe`, `list`, and `get`, API client reuse, bounded enrichment/thread/receipts, typed errors, cursor binding, explicit remote refusal, and the 15-second refresh declaration. At this milestone manual refresh is sufficient to prove the adapter; do not call it a finished live viewer.

**Exit evidence:** Real `sidecar plugin check`/`call` results for the isolated instance, plus a visible message and detail in a real tab and resource pane. A before/after subscription snapshot proves reads changed no read cursors and created no agent identity.

### M2: Stable shared browser refresh

**Depends on:** M1's fixture contract; can be developed with a fixture executable.

**Ownership:** `internal/pluginbrowser` and focused host fixture modes.

Implement refresh intent, stable-ID/viewport restoration, independent detail re-fetch, semantic no-change handling, bounded buffered results for displaced/paged selections, and last-known error states. Preserve query cancellation and existing copy/selection behavior. Fix host unknown-total presentation if M0 finds it assumes completeness.

**Exit evidence:** Behavioral tests and a real cursor/scroll proof show arrivals above the selected row, more than one page of activity, slow queries, expired selected messages, and transient failure without reading-position jumps or false empty states.

### M3: Shared freshness across all placements

**Depends on:** M2.

**Ownership:** Shared freshness component, `internal/app` standalone binding, workspace/overview resource bindings, and their focused tests.

Extract the existing plugin scheduling policies into one component; use existing watcher infrastructure; add the standalone binding; route explicit changes through it. Implement per-target deadlines, visible-only work, debt, in-flight coalescing, asynchronous missing-path re-probes, and lifecycle fencing. Remove displaced duplicated policy code as bindings migrate.

**Exit evidence:** The same fixture refreshes in a standalone tab, workspace pane, and Sessions pane; hidden views are idle; returning refreshes immediately; two different declared intervals remain independent; disappearing owners release watchers and calls. Recall behaves correctly with no plugin changes.

### M4: Comms change signal and fast local updates

**Depends on:** M1. Integrate after M3 is available for consumer proof.

**Ownership:** Comms application notification seam, service marker writer/path helper, plugin watch declaration, and service/client documentation.

Add broad post-success invalidation, instance-scoped atomic revision marker, bounded coalescing/retries, and lifecycle cleanup. Declare the eligible notification directory and provide clear polling-only behavior when unsupported or absent. Cover read-through and other non-message changes explicitly; an observe-newest-ID comparison cannot detect them.

**Exit evidence:** Publish and receipt-only changes each update all visible placements through the marker path. Older/no-marker daemons and out-of-home socket paths still reconcile by polling. Killing/restarting only the isolated service changes instance identity and the viewer recovers.

### M5: End-to-end proof, demo, docs, and handoff

**Depends on:** M1–M4.

**Ownership:** Sidecar proof/demo scripts and relevant plugin docs; Comms command and notification-contract docs; scoped changelog entries when implementation lands.

Add a Comms option to the existing demo workflow rather than creating a competing Sidecar launcher. Proposed invocation: `./scripts/demo.sh single --comms`; this flag does not exist yet and belongs to this milestone. Compile/use explicit task binaries, seed public and direct conversations with two isolated logical identities, generate a small timed burst, and include zero/one/multiple receipt examples. Stop the generator and only the daemon started by the demo on exit.

Document setup, `passEnv`, all-topics scope, the fast-watch versus polling distinction, bounds, read-only semantics, remote refusal, and commands to inspect the plugin without a TUI. Update the protocol's host behavior documentation to match the completed visibility/refresh implementation without changing the wire identifier or promising unrestricted real-time streaming.

Obtain independent review of the Comms ownership boundary, notification completeness, Sidecar lifecycle, and real UI evidence. Fix findings before focused commits. Implementation can merge by repository in dependency order; no Sidecar Go-module dependency on Comms is needed. Publish releases or activate a user's live installation only as separately requested work.

**Exit evidence:** All acceptance criteria below, a reproducible demo command, reviewed scoped commits in each changed repository, and a clear record of which versions were proved together.

## Verification contract

### Focused automated coverage

| Seam | Required behavior |
| --- | --- |
| Plugin wire/CLI | Exactly one JSON response; typed failures exit zero; no network/daemon/identity creation in describe; unknown protocol/method refusal; full deadline budget; `COMMS_AUTO_START=0` with a missing socket starts nothing; no stdout logs; actual host conformance |
| Projection/API | Empty query versus search; topic resolution; cursor/filter binding; unknown total; bounded label fallback; oldest selected message with recent thread tail; oversize content labeling; receipt failure distinct from zero recipients |
| Read-only semantics | Observe, peek, thread, receipts, and search do not advance cursors, subscribe, join, publish, or mutate metadata |
| Change producer | Every relevant successful mutation invalidates; failure does not; receipt-only change; no write needed for expiry reconciliation; restart instance change and overlap fencing; burst coalescing; marker failure leaves committed operation successful; preexisting symlink/non-directory/foreign or permissive directory degrades without modifying it |
| Host scheduling | Three placement bindings share policy; independent poll intervals; only affected visible targets refresh; hidden tabs ignore invalidations; one owed refresh under bursts; visibility return; missing/recreated directory; teardown races |
| Browser state | Stable ID and viewport; detail scroll; query replacement isolation; loaded older pages and displaced selection; successful empty versus failure; buffered-page action; late result rejection; same-content refresh |
| Compatibility | Recall search-required collection remains idle without a query; its filters/details still work; frozen resource providers retain their protocol behavior; multiple configured instances remain distinct |

Use the real fixture executable where process cancellation, output bounds, and timeout behavior matter. Prefer fake clocks for scheduler tests and deterministic API fixtures for list/detail behavior; avoid timing-sensitive sleeps as the primary assertion.

### Real consumer proof

Use explicit binaries built from the implementation revisions. Run the normal Sidecar focused package tests for changed packages, then `go build ./...` and `go test ./...`. In Comms, run its required `make check` and `git diff --check`. Run both repositories' diff checks. Do not run application test suites for the plan-only change that creates this document.

The real proof must exercise:

1. Empty service → plugin tab loads an honest empty state → another isolated identity publishes → row appears without a keypress.
2. Select an older message and scroll its body → publish a burst → selection and detail scroll remain stable; updated results can be applied deliberately.
3. Load older pages → more than a page of new messages arrives → no silent page loss, duplication, or claim that the loaded window is complete.
4. Select a thread → publish a reply → timeline updates. Advance another identity's read cursor without publishing → receipts update independently.
5. Show equivalent content in standalone, project, and Sessions placements. Prove narrow and wide layouts, keyboard navigation, copy/selection, and independently visible list/document panes.
6. Hide each placement → publish → process counters show no hidden list/get activity → return → current data appears promptly.
7. Stop the isolated daemon → retained data is clearly stale → restart → recovery without restarting Sidecar. Cover auto-start behavior separately from an explicitly no-auto-start outage case.
8. Remove/recreate the notification directory, inject a marker-write failure, and use an out-of-home socket → polling recovers; unsupported watch placement is labeled honestly.
9. Expire a selected ancestor while its thread still has a live reply → it remains readable according to peek. Expire/purge the last live thread content until peek returns `not_found` → detail reports that it is unavailable. Absence from the first list page alone never triggers that conclusion.
10. Change filters during a slow response → only the current request's rows/detail appear. Open Recall beside Comms → Recall does not inherit Comms's cadence.

Measure rather than promise instantaneous updates. On the local marker path, target publish-to-visible and receipt-change-to-visible within two seconds under a modest burst, recording observed latency and subprocess counts. On the fallback path, changes must appear by the next 15-second poll plus bounded query/render latency. There must be no per-message process storm, no repeated cancellation that prevents a result from landing, and no background polling while the relevant surfaces are hidden.

### Isolation requirements

Run `./scripts/tmux-drive.sh paths` before using the harness. Confirm a private tmux socket and Sidecar state/config tree. Every hand-rolled proof must unset `TMUX` and `TMUX_PANE` or pass an explicit private `tmux -S` socket, including cleanup. Set `XDG_STATE_HOME`, `TMUX_TMPDIR`, `-config <temporary path>`, and `SIDECAR_ISOLATED_STATE=1` for Sidecar. Never stop, restart, replace, or clean up the default tmux server.

Comms needs its own explicit temporary socket, database/state directory, and identity contexts. Do not let CLI auto-start resolution fall back to the user's live socket. Use one short temporary directory below the user's home for fast-watch proof, because the protocol refuses `/tmp` watch paths; use a separate out-of-home fixture for polling-fallback proof. Keep Unix socket paths short enough for the platform limit. The home-based proof directory must still be entirely separate from real Sidecar and Comms state.

Every proof installs cleanup before starting processes, records exact child ownership, and stops `tmux-drive.sh` on success or error. Cleanup may remove only the proof directory and processes it created. Do not rely on broad process-name matching or the normal Comms daemon's shutdown endpoint.

## Acceptance checklist

- [ ] A configured `comms sidecar-plugin` passes the real Sidecar host's describe/list/get checks.
- [ ] Messages, topic filtering, search, selected bodies, recent threads, and receipts work against Comms's API without a second store or observer identity.
- [ ] Reading any placement leaves all Comms read cursors and domain state unchanged.
- [ ] Fast local invalidation covers message and receipt-only changes; periodic reconciliation covers time-based expiry and lost signals.
- [ ] Standalone, project, and Sessions placements share scheduling/visibility policy and produce equivalent content.
- [ ] Arrival bursts preserve stable selection and reading position; pagination remains bounded and honest.
- [ ] Empty, partial, failed, stale, expired, and unsupported-remote states are distinct and recoverable.
- [ ] Hiding/closing/disabling releases watches, timers, and unnecessary calls; reopening refreshes and late results are fenced.
- [ ] Recall and frozen resource providers pass compatibility checks without provider-specific workarounds.
- [ ] Normal local setup and custom socket/environment configuration are documented, including polling-only locations.
- [ ] Isolated tests, UI captures, latency/process evidence, and the Comms demo are reproducible from recorded revisions.
- [ ] Independent implementation review is complete and all findings are resolved before the plan moves to `implemented`.

## Remaining decisions and boundaries

The architectural decisions above are the implementation baseline, not claims that the capability already ships. Exact shared-component/file names, the final key for applying buffered results, and compact receipt wording should be settled against the existing keymap and a narrow/wide UI proof during M2/M3. They do not justify inventing a custom Comms layout.

If two-second local freshness cannot be met with bounded marker coalescing and one-shot calls, collect the trace first. A future streaming/event protocol proposal requires that evidence and its own compatibility decision; it is not an implicit fallback in this plan. If out-of-home fast refresh or remote Comms becomes necessary, extend the owning transport deliberately rather than weakening watch-path validation or reading another machine's paths locally.
