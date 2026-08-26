# Live Terminal Leaf Extraction

One host-independent home for the state of a *live terminal leaf*, so a surface can hold as many of them as its pane tree allows — and, on top of that, terminal splits in the global Sessions browser at parity with the project workspace.

This is the missing half of the pane-leaf story. Passive leaves already have their host-independent lifecycle in `internal/contentpanes`, adopted by both surfaces; live leaves do not, and the two hosts each hand-roll their own. That is why `AllowTerminalSplit` is `true` in `internal/plugins/workspace` and `false` in `internal/overview`, and why closing that gap is a refactor before it is a feature.

**Owns:** the shared live-terminal-leaf seam, and the global Sessions browser's Terminal split row.

**Relationship to [terminal-splits-and-windowing.md](terminal-splits-and-windowing.md):** that plan delivered the live Terminal leaf inside the project workspace (its phase A) and remains the authority on placement policy, the live-leaf cap, sidebar badges, and the deferred chord tier. Its A6 assumed the global browser would get split terminals for free by mirroring "whatever the primary terminal gets on that surface". That turned out not to be free: the two hosts implement a live terminal in incompatible shapes, so A6's rule is correct as an *outcome* and silent about the work. This plan takes ownership of that work and of resolved question 2 in that document.

**Precedent to follow:** [`internal/contentpanes`](../../../internal/contentpanes/deck.go) — "the host-independent lifecycle of Sidecar's passive Document, Issue, Note, Diff, and Resource panes. A Deck deliberately does not render or persist itself." Everything below is that sentence with "passive" replaced by "live".

**Related:** [pane-switcher-everywhere.md](pane-switcher-everywhere.md) extends where the create modal opens from; it does not change what the modal may offer, which is this plan's concern.

## The problem, stated from the code

The project workspace holds exactly two terminals as parallel pairs of flat fields on `Plugin`:

| primary | peer | what it is |
| --- | --- | --- |
| `primaryTerminal` | `panelTerminal` | `*tty.Model`, the transport and input owner |
| `primaryTerminalTarget` | `panelTerminalTarget` | which session/pane it is attached to |
| `previewScroll` | `termPanelScroll` | rows back from the live bottom |
| `previewFreeze` | `termPanelFreeze` | the window pin held by a gesture or a document |
| `previewFreezeDoc` | `termPanelFreezeDoc` | which of those two holds the pin |
| `primaryLinkState` | `panelLinkState` | resolved terminal links |
| `primaryLinkContext` | `panelLinkContext` | the link surface's identity |
| `primaryRowAnalyzer` | `panelRowAnalyzer` | row classification for links and selection |
| — | `termPanelOutput` | the peer's captured buffer |
| — | `termPanelSession`, `termPanelPaneID` | the peer's durable session identity |
| — | `termPanelVisible`, `termPanelFocused`, `shellLeafSurface` | whether the leaf is in the tree, whether it has the keyboard, and which workspace owns it |

`termPanel*` alone is 526 references across 35 non-test files, plus 93 more for `shellLeaf`/`shellSplit`. Worse than the count is the shape: three data types — `terminalHistorySource`, `terminalScrollbarHit`, and `InteractiveState` — carry a `TermPanel bool` field whose whole job is to say *which of the two terminals* a request belongs to. "There are exactly two, and a boolean picks one" is encoded in the plugin's vocabulary, not just its fields.

The global Sessions browser holds exactly one, as singular fields on `previewState`: `terminal`, `terminalTarget`, `buffer`, `offset`, `freeze`, `history`, `selection`, `pointer`, `wheel`, `termBar`, `linkState`, `rowAnalyzer`, `interactive` — about 163 references across its non-test files. Its own comment is explicit: "syncPreviewTerminal reconciles *the one resource-bearing preview* with the one pane actually visible."

So the honest options are to duplicate the project side's pairs into a third and fourth copy on a surface that has none yet, or to give one leaf's state a name. This plan takes the second.

What is **not** the problem, and therefore not in scope to rebuild: transport and input (`internal/tty` owns those, behind the `previewTerminal`/`tty.Model` seam both hosts already use), rendering (`internal/termpreview` owns headers, rows, links, row analysis), geometry and chrome (`internal/panelayout`, `internal/paneframe`), and passive pane content (`internal/contentpanes`). The gap is the per-leaf state bundle and its lifecycle, and nothing else.

## Settled decisions

1. **A new package, `internal/termpanes`, named and shaped after `contentpanes`.** It owns one live terminal leaf's state and the lifecycle of a keyed collection of them. It does not render, does not persist itself, and returns every potentially expensive act as a `tea.Cmd`. Hosts keep target/layout policy, exactly as they keep it for `contentpanes`.
2. **The collection is keyed by pane-tree leaf ID**, mirroring `Deck.panes map[int]*pane`. Leaf ID is the only identity that survives a split, a close, and a re-focus, and it is already what `paneframe` hit regions and `panelayout` plans speak in.
3. **`TermPanel bool` becomes a leaf ID.** `terminalHistorySource`, `terminalScrollbarHit`, and `InteractiveState` carry the leaf the request belongs to. This is the change that makes N terminals expressible rather than merely tolerated; a plan that leaves the bool in place has not done the extraction.
4. **The project workspace migrates first, and both of its terminals move.** Moving only the peer would leave the pairs half-dissolved and prove nothing about N. When the extraction is right, the `primary*`/`panel*` field pairs are deleted, not renamed.
5. **Phases 1 and 2 change no behaviour.** Their proof is that the existing suites — terminal parity, scroll, surface, interactive, wheel-boundary — pass unchanged, with no test edited to accommodate the refactor. A test that has to change is a behaviour change that needs explaining.
6. **On the global surface a terminal leaf is scoped to the selected row**, exactly as its primary terminal already is. Navigating away detaches; navigating back reattaches. The tmux session is durable, so the peer survives the round trip the same way the primary does, and `previewUnavailable`, the generation counter, and `closePreviewTerminal` govern both by one rule rather than two.
7. **`panelayout.LiveLeafCap` stays at 2 and stays global to a tree.** The global browser's tree then holds at most its primary plus one peer, which is the same budget the project workspace works within, and the same refusal string (`shellCapMessage`) reaches the modal through `OpenOpts.TerminalSplitDisabled`, which already exists for this purpose.
8. **The global surface's pane tree remains memory-only.** It caches per workspace ID in `previewState.paneCache` and persists nothing; only the project workspace writes `state.PaneLayoutJSON`. A peer terminal created in the global browser therefore does not survive a Sidecar restart, while its tmux session does. Changing that is a separate decision — see open questions.
9. **`AllowTerminalSplit` keeps its post-fix meaning: "this host can run a second live terminal beside its own", and gates exactly the Terminal split row.** Passive rows are never gated on it. Bundling them is what cost the global browser its resource-provider rows.

## Phase 1 — Extract, project workspace only

No user-visible change. The project workspace ends the phase holding two `termpanes` entries where it held two sets of fields.

1. **Create `internal/termpanes` with the leaf state type.** One struct per live leaf: the `*tty.Model` seam, its target, the captured buffer, scroll offset, freeze and freeze-owner, history reach, link state, link context, row analyzer, and the leaf's durable session/pane identity. Constructor and decode are pure; opening, closing, resizing, and reading history return commands.
2. **Add the keyed collection** with the operations both hosts need: attach a leaf, release a leaf, look up by leaf ID, iterate live leaves, and answer `panelayout.LiveLeafCount` from the tree rather than from its own bookkeeping (one source of truth for the cap).
3. **Replace `TermPanel bool` with a leaf ID** in `terminalHistorySource`, `terminalScrollbarHit`, and `InteractiveState`, and at their seven construction sites.
4. **Move the project workspace's primary terminal onto the collection**, deleting `primaryTerminal`, `primaryTerminalTarget`, `previewScroll`, `previewFreeze`, `previewFreezeDoc`, `primaryLinkState`, `primaryLinkContext`, `primaryRowAnalyzer`.
5. **Move its peer terminal onto the collection**, deleting the `panel*` and `termPanel*` state fields. `termPanelVisible` and `shellLeafSurface` are the two that do not move as-is: the first becomes "is there a live leaf in the tree", derivable from `panelayout`, and the second is a host policy (which workspace owns the split), which stays with the host.
6. **Keep `syncShellLeaf` as the one reconciliation point** between the flag and the tree, and make it read the collection rather than the flag.

**Ship criteria:** every existing test in `internal/plugins/workspace` passes unedited; `grep termPanel` over non-test sources returns only host-policy names (placement, seed, legacy migration), not state; `grep 'TermPanel bool'` returns nothing.

## Phase 2 — Adopt on the global Sessions browser

Still no user-visible change. The global browser ends the phase holding one `termpanes` entry where it held one set of fields, which is the whole point: the same code path at N=1.

1. **Move `previewState`'s terminal fields onto the collection** — `terminal`, `terminalTarget`, `buffer`, `offset`, `freeze`, `history`, `linkState`, `rowAnalyzer` — leaving `selection`, `pointer`, `wheel`, and `termBar` for step 2.
2. **Decide per-leaf versus per-surface for the interaction state.** `selection`, `pointer`, `wheel`, and `termBar` are gestures, and a gesture belongs to the leaf it started in — the project workspace already learned this for documents (`docSelectLeaf`: "a drag is answered by where it began, never by where the pointer has since gone"). Move them per-leaf on both surfaces, or state why not.
3. **Route `syncPreviewTerminal`, `closePreviewTerminal`, and `syncTerminalGeometry` through the collection**, so "the one resource-bearing preview" becomes "the live leaves this row owns".
4. **Make `interactive bool` a focused-leaf question** rather than a surface-wide one, matching `paneHost.Chrome`, which already asks per node.

**Ship criteria:** every existing test in `internal/overview` passes unedited; the two `pane_host.go` files still answer only what is in their own leaves; no second compositor, border rule, or divider renderer appears.

## Phase 3 — Terminal splits in the global Sessions browser

The capability, which is now an addition rather than a rebuild.

1. **Offer the row:** `AllowTerminalSplit: true` in `Model.openCreate`, with `TerminalSplitDisabled` set to the shared cap message when `panelayout.LiveCapReached` holds for the selected row's tree. The modal already renders a disabled row with its reason inline and refuses every create path for it.
2. **Create the peer session** on the global surface through the same session-naming and seeding path the project workspace uses (`sidecar-tp-*`), so a split created on either surface is the same kind of object and is reaped by the same rules.
3. **Place it** through `panelayout.PlanOpen` with the modal's `Auto · Right · Below` placement, unchanged — the placement vocabulary is already shared and already reaches this surface for passive panes.
4. **Bind the row-scoped lifecycle** from decision 6: detach on navigate-away, reattach on return, release on row deletion.
5. **Give it the same exits the project surface has:** the header ✕, the confirm-before-closing-a-running-process modal, and a session that ends underneath the user. `shellCloseMode`'s three modes are host-independent policy and should move to `termpanes` with the state they act on.
6. **Rename via the clickable pane title** — `paneRegions.Title` already registers the region for `panelayout.Shell` leaves on this surface, and `internal/overview/rename.go` already owns the modal. This is wiring, not new UI.

**Ship criteria:** create-via-modal on the global browser yields two live terminals in one row's tree; divider drag resizes both with one resize per pane on release; closing the neighbour leaves the survivor attached; navigating to another row and back reattaches both with scrollback intact; the cap refuses the third with the reason on the row.

## Phase 4 — Follow-ons, not blockers

- **Persisting the global surface's tree.** Decision 8 leaves it memory-only. If a peer terminal there should survive a restart, `state.PaneLayoutJSON` is the existing vehicle and the global browser would become a second writer of it, keyed by workspace ID.
- **`sidecar layout get`/`apply` on the global surface.** The CLI reports and composes the project workspace's tree. Once the global browser hosts live leaves, the same projection can answer for it — and the parity rule says it eventually should.
- **Terminal splits outside the two workspace surfaces** (Files, Git, td, Notes) remains [terminal-splits-and-windowing.md](terminal-splits-and-windowing.md)'s B3, and becomes tractable once `termpanes` exists, since a plugin would adopt the package rather than reimplement the pairs.

## Acceptance evidence

- **Phases 1 and 2:** the full suite green with no test edited for the refactor, and the two greps from their ship criteria returning empty.
- **A source-scanned parity test**, in the idiom of [`internal/parityscan`](../../../internal/parityscan/parityscan.go), asserting that both hosts drive live leaves through `termpanes` rather than through fields of their own — the same trick `TestPaneSwitcherSurfacesStayInParity` uses to prove neither host grew a private target-resolution path.
- **A catalog parity assertion** that, with `AllowTerminalSplit` true on both surfaces, the two kind catalogs are identical — the current test asserts they differ by exactly the Terminal split row, and phase 3 is done when that difference is zero.
- **Real-app proof on a fully isolated run** (`scripts/tmux-drive.sh`, both the tmux socket and the state tree isolated — confirm with `tmux-drive.sh paths` that nothing resolves under `~/.local/state/sidecar` or `~/.config/sidecar`): the phase 3 ship criteria driven end to end on the global browser, with snapshots.
- **A live-leaf cap table** covering both surfaces: at the cap the row is disabled with its reason, Enter on it is inert, and each placement button refuses.

## Open questions

1. **Do gestures belong to the leaf or to the surface?** Phase 2 step 2 proposes per-leaf, by analogy with `docSelectLeaf`. It needs deciding before the fields move, because moving them twice is the expensive order.
2. **Should the global browser persist its pane tree?** Decision 8 says no for now. The argument for is that a terminal split you made is work you expect to find again; the argument against is that the global browser's tree is scoped to a row the user is passing through, and persisting it makes a browsing gesture durable.
3. **Does `LiveLeafCap` stay per tree or become per process?** Two live terminals per surface is four across both if a user has each open. The cap exists for control-mode subscriptions and resize cost, which are process-wide, so the current per-tree reading may be the wrong unit — but changing it is a behaviour change for the project workspace and belongs in a separate decision.
