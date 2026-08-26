# Live Terminal Leaf Extraction

**Status:** Implemented. The shared `internal/termpanes` lifecycle is adopted by both workspace surfaces, global Sessions offers the parity Terminal split, the integrated gates pass, and the isolated real-app proof covers creation through cap refusal. **Tracking:** `td-ab1bf4`, `td-6aee8c`, `td-c11e1a`, and integration `td-539a36`.

This work established one host-independent home for the state of a *live terminal leaf*, so a surface can hold as many of them as its pane tree allows, and delivered terminal splits in the global Sessions browser at parity with the project workspace.

This was the missing half of the pane-leaf story. Passive leaves already had their host-independent lifecycle in `internal/contentpanes`, adopted by both surfaces; live leaves did not, and the two hosts each hand-rolled their own. The implementation first closed that lifecycle gap, then enabled the global surface through the same `WorkspaceTerminalPanel` feature flag as the project workspace.

**Owns:** the shared live-terminal-leaf seam, and the global Sessions browser's Terminal split row.

**Relationship to [terminal-splits-and-windowing.md](../active/terminal-splits-and-windowing.md):** that plan delivered the live Terminal leaf inside the project workspace (its phase A) and remains the authority on placement policy, the live-leaf cap, sidebar badges, and the deferred chord tier. Its A6 assumed the global browser would get split terminals for free by mirroring "whatever the primary terminal gets on that surface". That turned out not to be free: the two hosts implemented a live terminal in incompatible shapes, so A6's rule was correct as an *outcome* and silent about the work. This plan took ownership of that work and of resolved question 2 in that document.

**Implementation precedent:** [`internal/contentpanes`](../../../internal/contentpanes/deck.go) — "the host-independent lifecycle of Sidecar's passive Document, Issue, Note, Diff, and Resource panes. A Deck deliberately does not render or persist itself." The implementation applied that shape to live leaves.

**Related:** [pane-switcher-everywhere.md](../active/pane-switcher-everywhere.md) extends where the create modal opens from; it does not change what the modal may offer, which was this plan's concern.

## The original problem, stated from the code

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

`termPanel*` alone is 485 references across 38 non-test files, plus 89 more for `shellLeaf`/`shellSplit`. Worse than the count is the shape: four data types — `terminalHistorySource`, `terminalScrollbarHit`, `InteractiveState`, and `terminalLinkRevalidatedMsg` — carry a `TermPanel bool` field whose whole job is to say *which of the two terminals* a request belongs to. And the encoding has escaped the plugin: `panelayout.TargetTermPanel` is a named focus-ring stop for "the panel", with Shell leaves deliberately skipped in the leaf walk because "which of the two live terminals owns the keyboard is not the leaf ring's answer". "There are exactly two, and a name picks one" is in the shared vocabulary, not just the plugin's fields.

The global Sessions browser holds exactly one, as singular fields on `previewState`: `terminal`, `terminalTarget`, `buffer`, `offset`, `freeze`, `history`, `selection`, `pointer`, `wheel`, `termBar`, `linkState`, `rowAnalyzer`, `interactive` — about 163 references across its non-test files. Its own comment is explicit: "syncPreviewTerminal reconciles *the one resource-bearing preview* with the one pane actually visible."

So the honest options are to duplicate the project side's pairs into a third and fourth copy on a surface that has none yet, or to give one leaf's state a name. This plan takes the second.

What is **not** the problem, and therefore not in scope to rebuild: transport and input (`internal/tty` owns those, behind the `previewTerminal`/`tty.Model` seam both hosts already use), rendering (`internal/termpreview` owns headers, rows, links, row analysis), geometry and chrome (`internal/panelayout`, `internal/paneframe`), and passive pane content (`internal/contentpanes`). The gap is the per-leaf state bundle and its lifecycle, and nothing else.

## Settled decisions

1. **A new package, `internal/termpanes`, named and shaped after `contentpanes`.** It owns one live terminal leaf's state and the lifecycle of a keyed collection of them. It does not render, does not persist itself, and returns every potentially expensive act as a `tea.Cmd`. Hosts keep target/layout policy, exactly as they keep it for `contentpanes`.
2. **The collection is keyed by pane-tree leaf ID**, mirroring `Deck.panes map[int]*pane`. Leaf ID is the only identity that survives a split, a close, and a re-focus, and it is already what `paneframe` hit regions and `panelayout` plans speak in.
3. **`TermPanel bool` becomes a leaf ID.** `terminalHistorySource`, `terminalScrollbarHit`, `InteractiveState`, and `terminalLinkRevalidatedMsg` carry the leaf the request belongs to. So does the focus ring: `panelayout.TargetTermPanel` retires, Shell leaves become ordinary `TargetLeaf` stops, and "which live terminal owns the keyboard" is answered by leaf ID like every other focus question. This is the change that makes N terminals expressible rather than merely tolerated; a plan that leaves the bool in place has not done the extraction.
4. **The project workspace migrates first, and both of its terminals move.** Moving only the peer would leave the pairs half-dissolved and prove nothing about N. When the extraction is right, the `primary*`/`panel*` field pairs are deleted, not renamed.
5. **Phases 1 and 2 change no behaviour.** The existing terminal parity, scroll, surface, interactive, and wheel-boundary behavioral assertions and outcomes remain intact. Private white-box fixtures and selectors that directly named the deleted host fields or `TargetTermPanel` are migrated mechanically to the leaf-ID collection because they cannot compile against the new ownership model; those migrations do not weaken or remove behavioral assertions.
6. **On the global surface a terminal leaf is scoped to the selected row**, exactly as its primary terminal already is. Navigating away detaches; navigating back reattaches. The tmux session is durable, so the peer survives the round trip the same way the primary does, and `previewUnavailable`, the generation counter, and `closePreviewTerminal` govern both by one rule rather than two.
7. **`panelayout.LiveLeafCap` stays at 2 and stays global to a tree.** The global browser's tree then holds at most its primary plus one peer, which is the same budget the project workspace works within, and the same refusal string (`shellCapMessage`) reaches the modal through `OpenOpts.TerminalSplitDisabled`, which already exists for this purpose.
8. **The global surface's pane tree remains memory-only.** It caches per workspace ID in `previewState.paneCache` and persists nothing; only the project workspace writes `state.PaneLayoutJSON`. A peer terminal created in the global browser therefore does not survive a Sidecar restart, while its tmux session does. Changing that is a separate decision — see open questions.
9. **`AllowTerminalSplit` keeps its post-fix meaning: "this host can run a second live terminal beside its own", and gates exactly the Terminal split row.** Passive rows are never gated on it. Bundling them is what cost the global browser its resource-provider rows.

## Phase 1 — Extract, project workspace only

No user-visible change. The project workspace ends the phase holding two `termpanes` entries where it held two sets of fields.

1. **Create `internal/termpanes` with the leaf state type.** One struct per live leaf: the `*tty.Model` seam, its target, the captured buffer, scroll offset, freeze and freeze-owner, history reach, link state, link context, row analyzer, and the leaf's durable session/pane identity. Constructor and decode are pure; opening, closing, resizing, and reading history return commands.
2. **Add the keyed collection** with the operations both hosts need: attach a leaf, release a leaf, look up by leaf ID, iterate live leaves, and answer `panelayout.LiveLeafCount` from the tree rather than from its own bookkeeping (one source of truth for the cap).
3. **Replace `TermPanel bool` with a leaf ID** in `terminalHistorySource`, `terminalScrollbarHit`, `InteractiveState`, and `terminalLinkRevalidatedMsg`, and at their five construction sites. (The other two case-sensitive `TermPanel` hits in `focus.go` are the focus target below, not the bool.)
4. **Replace `panelayout.TargetTermPanel` with leaf-ID targets.** Shell leaves stop being skipped in `Ring`'s leaf walk and become `TargetLeaf` stops — but *appended after the passive leaves*, preserving the current cycle order (sidebar, passive leaves, then the live terminal), so decision 5 holds. Collapsing live leaves into placement order is a real Tab-order change and is proposed separately or not at all.
5. **Move the project workspace's primary terminal onto the collection**, deleting `primaryTerminal`, `primaryTerminalTarget`, `previewScroll`, `previewFreeze`, `previewFreezeDoc`, `primaryLinkState`, `primaryLinkContext`, `primaryRowAnalyzer`.
6. **Move its peer terminal onto the collection**, deleting the `panel*` and `termPanel*` state fields. `termPanelVisible` and `shellLeafSurface` are the two that do not move as-is: the first becomes "is there a live leaf in the tree", derivable from `panelayout`, and the second is a host policy (which workspace owns the split), which stays with the host.
7. **Keep `syncShellLeaf` as the one reconciliation point** between the flag and the tree, and make it read the collection rather than the flag.

**Ship criteria:** the existing `internal/plugins/workspace` behavioral suite passes with its assertions preserved; only private white-box fixtures and selectors that named deleted fields or `TargetTermPanel` are migrated mechanically. `grep termPanel` over non-test sources returns only host-policy names (placement, seed, legacy migration), not state; `grep -E 'TermPanel +bool'` and `grep TargetTermPanel` return nothing. (Not `grep 'TermPanel bool'` — gofmt aligns `terminalLinkRevalidatedMsg`'s field as `TermPanel  bool`, and a single-space grep declares victory while that bool survives.)

## Phase 2 — Adopt on the global Sessions browser

Still no user-visible change. The global browser ends the phase holding one `termpanes` entry where it held one set of fields, which is the whole point: the same code path at N=1.

1. **Move `previewState`'s terminal fields onto the collection** — `terminal`, `terminalTarget`, `buffer`, `offset`, `freeze`, `history`, `linkState`, `rowAnalyzer` — leaving `selection`, `pointer`, `wheel`, and `termBar` for step 2.
2. **Decide per-leaf versus per-surface for the interaction state.** `selection`, `pointer`, `wheel`, and `termBar` are gestures, and a gesture belongs to the leaf it started in — the project workspace already learned this for documents (`docSelectLeaf`: "a drag is answered by where it began, never by where the pointer has since gone"). Move them per-leaf on both surfaces, or state why not.
3. **Route `syncPreviewTerminal`, `closePreviewTerminal`, and `syncTerminalGeometry` through the collection**, so "the one resource-bearing preview" becomes "the live leaves this row owns".
4. **Make `interactive bool` a focused-leaf question** rather than a surface-wide one, matching `paneHost.Chrome`, which already asks per node.

**Ship criteria:** the existing `internal/overview` behavioral suite passes with its assertions preserved; private white-box fixtures and selectors are migrated mechanically where the deleted host fields require it. The two `pane_host.go` files still answer only what is in their own leaves; no second compositor, border rule, or divider renderer appears.

## Phase 3 — Terminal splits in the global Sessions browser

The capability, which is now an addition rather than a rebuild.

1. **Offer the row:** `AllowTerminalSplit` in `Model.openCreate` (`internal/overview/global_create.go`) follows the same `WorkspaceTerminalPanel` feature flag the project workspace consults — not a literal `true`, or the global browser would offer splits while the project surface has them flagged off — with `TerminalSplitDisabled` set to the shared cap message when `panelayout.LiveCapReached` holds for the selected row's tree. The modal already renders a disabled row with its reason inline and refuses every create path for it. `global_create_providers_test.go` currently asserts the row's *absence* on this surface; inverting it is this phase's deliberate behaviour change, not test breakage.
2. **Create the peer session** on the global surface through the same session-naming and seeding path the project workspace uses (`sidecar-tp-*`), so a split created on either surface is the same kind of object and is reaped by the same rules.
3. **Place it** through `panelayout.PlanOpen` with the modal's `Auto · Right · Below` placement, unchanged — the placement vocabulary is already shared and already reaches this surface for passive panes.
4. **Bind the row-scoped lifecycle** from decision 6: detach on navigate-away, reattach on return, release on row deletion.
5. **Give it the same exits the project surface has:** the header ✕, the confirm-before-closing-a-running-process modal, and a session that ends underneath the user. `shellCloseMode`'s three modes are host-independent policy and should move to `termpanes` with the state they act on.
6. **Rename via the clickable pane title** — `paneRegions.Title` already registers the region for `panelayout.Shell` leaves on this surface, and `internal/overview/rename.go` already owns the modal. This is wiring, not new UI.

**Ship criteria:** create-via-modal on the global browser yields two live terminals in one row's tree; divider drag resizes both with one resize per pane on release; closing the neighbour leaves the survivor attached; navigating to another row and back reattaches both with scrollback intact; the cap refuses the third with the reason on the row.

## Phase 4 — Follow-ons, not blockers

- **Persisting the global surface's tree.** Decision 8 leaves it memory-only. If a peer terminal there should survive a restart, `state.PaneLayoutJSON` is the existing vehicle and the global browser would become a second writer of it, keyed by workspace ID.
- **`sidecar layout get`/`apply` on the global surface.** The CLI reports and composes the project workspace's tree. Once the global browser hosts live leaves, the same projection can answer for it — and the parity rule says it eventually should.
- **Terminal splits outside the two workspace surfaces** (Files, Git, td, Notes) remains [terminal-splits-and-windowing.md](../active/terminal-splits-and-windowing.md)'s B3, and is tractable now that `termpanes` exists, since a plugin can adopt the package rather than reimplement the pairs.

## Acceptance evidence

- **Phases 1 and 2:** the full suite is green; private white-box fixtures and selectors that could not compile after deletion of host-owned fields and `TargetTermPanel` are migrated mechanically, while behavioral assertions and outcomes are preserved and not weakened. The two greps from the phase ship criteria return empty.
- **A source-scanned parity test**, in the idiom of [`internal/parityscan`](../../../internal/parityscan/parityscan.go), asserting that both hosts drive live leaves through `termpanes` rather than through fields of their own — the same trick `TestPaneSwitcherSurfacesStayInParity` uses to prove neither host grew a private target-resolution path.
- **A catalog parity assertion** that, with `AllowTerminalSplit` true on both surfaces, the two kind catalogs are identical — the current test asserts they differ by exactly the Terminal split row, and phase 3 is done when that difference is zero.
- **Real-app proof on a fully isolated run** (`scripts/tmux-drive.sh`, both the tmux socket and the state tree isolated — confirm with `tmux-drive.sh paths` that nothing resolves under `~/.local/state/sidecar` or `~/.config/sidecar`): the phase 3 ship criteria driven end to end on the global browser, with snapshots.
- **A live-leaf cap table** covering both surfaces: at the cap the row is disabled with its reason, Enter on it is inert, and each placement button refuses.

## Resolved and deferred questions

1. **Gestures belong to the leaf.** Phase 2 moved selection, pointer, wheel, and terminal-bar gesture state into each live leaf on both surfaces, matching the `docSelectLeaf` rule that a drag is answered by where it began.
2. **The global browser does not persist its pane tree.** Decision 8 remains the shipped behavior. Persisting that browsing state is a separate future decision.
3. **Does `LiveLeafCap` stay per tree or become per process?** Two live terminals per surface is four across both if a user has each open. The cap exists for control-mode subscriptions and resize cost, which are process-wide, so the current per-tree reading may be the wrong unit — but changing it is a behaviour change for the project workspace and belongs in a separate decision.
