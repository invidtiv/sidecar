# M0 mockups

Screen mockups for the shared browser that renders every protocol plugin. Each `.tui.yaml` is a deterministic mockup for the TUI mockup tool; the `.txt` beside it is the rendered grid with colour stripped, so the layout can be read in a diff. Controlling document: [../README.md](../README.md); contract: [../protocol.md](../protocol.md).

**These mockups follow [docs/reference/design-language.md](../../../../reference/design-language.md).** Read it before editing one or adding another. The chrome here is not an approximation of Sidecar's: the header, the footer, the gradient pane borders, the inside pane titles, the leaf tab strip, the drag rail and the modal are drawn by studio components transcribed from the Go that paints the real app, so a mockup lands on the same columns and the same hex values as a capture of the running binary. Only the plugin's own data layout is invented here.

| File | Placement | Shows |
| --- | --- | --- |
| `recall-global-tab.tui.yaml` (`.results.txt`) | Global `Tab` | Recall's `results` collection with a query line, a `degraded` outcome and its notice, and the detail box rendering a resource with `fields`, an `Evidence` body section, and a `Timeline` section. Every key in the footer is host-owned. |
| `recall-global-tab.tui.yaml` (`.action-form.txt`) | Global `Tab` | DEX's `people` collection with view pills and a sort indicator, and the action form the host builds from an `act` declaration with one `multiline` input. |
| `ongoing-pane-tab.tui.yaml` (`.txt`) | `Panes` | Ongoing's `projects` collection as a collection tab in the shared Resource leaf beside a live terminal, opened by `sidecar open --plugin`. Narrow, so rows reflow to primary/secondary lines and the opened project is a second tab in the same leaf rather than a side box. |
| `recall-studio.tui.yaml` (`.empty-query.txt`, `.results-answered.txt`, `.results-degraded.txt`, `.abstained.txt`, `.scope-picker.txt`, `.narrow-pane.txt`) | Global `Tab` and `Panes` | One plugin across six host renderings: the host-answered empty query for a `search: required` collection; all three `list` outcomes rendered honestly (`answered`, `degraded` with two notices, `abstained` with every source answering); two resource shapes with `body`, `fields` and `items` sections; the scope form the host builds from four declared `choice`/`text` inputs; and the same collection reflowed into a 52-column Resource leaf. |

What the mockups fix and what they leave open:

- Fixed: the query line sits above the table and only when `search` is not `none`; view pills and the sort indicator share one line; notices sit under the table; the detail renders `fields` as a grid, then sections in declared order with a titled rule each; an action form is a modal with one control per declared input.
- Fixed by `recall-studio`: `total` and the outcome pill share the right end of the query line and do not move when notices appear; row `status` gets a reserved, unlabelled right-hand column rather than a plugin-declared one; a field whose value overruns its column takes a whole line instead of wrapping; with no resource open, the detail box in a `Tab` placement shows the plugin's other collection instead of a blank card; below the table floor the `primary` cell takes line 1 and the `secondary` cell takes a dimmed line 2, with the remaining short columns folded into the head of line 2.
- Open: column width negotiation when the plugin's hints exceed the box; whether the detail box in a `Tab` placement is beside or below the list at narrow widths.

Regenerate a snapshot after editing a mockup:

```bash
node "$TUI_REPO/bin/tui.js" render recall-global-tab.tui.yaml --state "Results and detail" | sed 's/\x1b\[[0-9;]*m//g; s/[[:space:]]*$//' > recall-global-tab.results.txt
```

State names, for `--state`: `Results and detail` and `Action form` in `recall-global-tab`; `Empty query`, `Results · answered`, `Results · degraded`, `Abstained`, `Scope picker` and `Narrow pane` in `recall-studio`. `ongoing-pane-tab` has one unnamed state and needs no flag.
