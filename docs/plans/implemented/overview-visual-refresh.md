# Overview visual refresh — implementation contract

Approved design. Mockup: the Agent Overview board gains colour encoding for project identity, agent type, and worktree-vs-shell, and the board grid is put back on a single set of column widths.

Three packages change, in dependency order: `internal/styles` → `internal/kanban` → `internal/overview`. This document is the contract between them. Anything not specified here is the implementer's call, but the exported signatures below are fixed — later phases are written against them.

## Design decisions (settled, do not relitigate)

1. **Project spine.** Every card carries a left accent glyph on all content lines, coloured by a stable per-project hue. Solid `▌` for a worktree, hairline `▏` for a shell.
2. **Kind glyph.** `⑂` for a worktree, `❯` for a shell, muted, between project name and workspace name. Redundant with the spine on purpose — colourblind-safe.
3. **Agent chip.** Provider name on a raised background fill, provider-coloured foreground. The only filled background on an unselected card.
4. **Status + relative age** on line two, in the lane's colour. The old `Meta` freshness line (`current`) is gone; only abnormal freshness gets a word (`stale`, `refreshing…`).
5. **Attention mark.** `▲ ` prefixes the status text of cards in the blocked lane.
6. **Card = 3 content rows + 1 blank gutter.** `CardHeight` stays 4.
7. **Ordering.** Group by project in configured project order; most-recent-first within a project group.
8. **Empty lanes** render a dim `·`, not `No agents`. Loading and error lanes keep their message.
9. **All colour is themeable.** No hex literal outside `internal/styles`.

## Phase 1 — `internal/styles`

### `ColorPalette` additions (themes.go)

```go
ProjectHues []string          `json:"projectHues"` // ordered ramp, cycled by project
AgentColors map[string]string `json:"agentColors"` // provider name (lowercase) -> hex
LaneWorking string            `json:"laneWorking"`
LaneBlocked string            `json:"laneBlocked"`
LaneDone    string            `json:"laneDone"`
LaneIdle    string            `json:"laneIdle"`
LanePaused  string            `json:"lanePaused"`
```

`ProjectHues` follows the existing `TabColors` / `GradientBorder*` array pattern. `AgentColors` is the first map-valued palette field; extend `applyGenericOverrides` to accept `map[string]interface{}` for it, validating each value with `IsValidHexColor` exactly as `applySingleOverride` does. The five lane fields are plain strings and must be reachable through `applySingleOverride`'s switch, like every other string field.

### Defaults, derived in `NormalizePalette`

Derivation runs only when a field is empty, so a theme that says nothing still gets a coherent board and no built-in theme other than `DefaultTheme` needs editing:

- `ProjectHues` empty → use `TabColors`. Both empty → a package-level default ramp.
- `AgentColors` → start from a package-level default map, then overlay whatever the palette supplied (per-key, caller wins).
- `LaneWorking` ← `Success`, `LaneBlocked` ← `Warning`, `LaneDone` ← `Info`, `LaneIdle` ← `TextSecondary`, `LanePaused` ← `TextMuted`.

Run every resulting colour through `EnsureContrastOn` against the chrome surfaces, the same way the existing text roles are handled. Agent chip foregrounds are held against `SurfaceRaised`, since that is the chip fill.

`DefaultTheme` gets explicit values matching the approved mock:

- Project hues, in order: `#A78BFA` `#22D3EE` `#FB923C` `#F472B6` `#60A5FA` `#A3E635`. Six hues, deliberately excluding green and amber — those belong to status.
- Agent colours: `claude` `#D97757`, `codex` `#7DD3FC`, `grok` `#E2E8F0`, `antigravity` `#5EEAD4`, `gemini` `#60A5FA`, `cursor` `#C4B5FD`.
- Lane colours: working `#34D399`, blocked `#FBBF24`, done `#7AA2F7`, idle `#9CA3AF`, paused `#6B7280`.

### New file `internal/styles/overview.go`

```go
// ProjectHue returns a stable hue for a project key. The same key always maps to
// the same ramp entry for a given ramp length, so a project keeps its colour
// across restarts.
func ProjectHue(projectKey string) lipgloss.Color

// AgentColor returns the chip foreground for a provider, case-insensitively.
// Unregistered providers get TextMuted rather than borrowing a hue.
func AgentColor(provider string) lipgloss.Color

// AgentChipFill is the background behind an agent chip.
func AgentChipFill() lipgloss.Color // == SurfaceRaised

// LaneColor maps an agentstatus lane id ("working", "blocked", "done", "idle",
// "paused") to its colour. Unknown ids get TextMuted.
func LaneColor(lane string) lipgloss.Color
```

`ProjectHue` must be a pure function of the key and the ramp — hash the key with `hash/fnv`, index modulo `len(ramp)`. Do not depend on map iteration order or on the order projects happen to load in.

`ApplyThemeColors` populates the package-level ramp/map these read from. Keep them behind the same mutex discipline the rest of the file uses.

Do not use `agentstatus` lane constants here — `styles` must not depend on it. Plain strings.

## Phase 2 — `internal/kanban`

### Styled card lines (board.go)

Additive. `Title` / `Subtitle` / `Detail` / `Meta` stay, because `internal/plugins/workspace` builds cards with them and must keep working untouched.

```go
// Span is a styled run of text within a card line.
type Span struct {
    Text       string
    Foreground color.Color // nil inherits
    Background color.Color // nil for none
    Bold       bool
}

// Line is one rendered row of a card.
type Line struct{ Spans []Span }
```

`Card` gains `Lines []Line`. When `Lines` is non-empty it takes precedence over the four string fields, and `defaultCardLine` renders from it. That means a consumer building styled cards needs no custom `RenderCard` at all.

Rendering rules for `Lines`:

- Line `i` of the card renders `Lines[i]`; rows past the end are blank.
- Spans are laid left to right, truncated against a running width budget so the cell never exceeds its column width. Use `ansi` for width, never `len`.
- Selected card: paint the selection background across the full cell width on every line, keeping each span's foreground. The spine must stay its project hue.

### Geometry (board.go)

`Layout` gains `ColumnWidths []int`, summing with the `n-1` separators to exactly `innerWidth`; distribute the division remainder to the leftmost lanes rather than dropping it. Keep the existing `ColumnWidth` field populated (the workspace plugin reads `Layout` too) — set it to the base width.

### Render (component.go)

The bug being fixed: three different widths are computed for one grid. Rules use `innerWidth`, lane headers use `Width(W)` joined by `│` (= `n·W + n − 1`), and card cells use `W−1` joined by `│` (= `n·W − 1`). After this change every row is exactly `innerWidth` wide and every divider sits at the same x.

- Card cells use `ColumnWidths[i]`, not `ColumnWidths[i] - 1`.
- Lane headers use `ColumnWidths[i]`.
- The rule directly under the title stays a flat run of `─`.
- The two rules bracketing the lane headers are built as per-column runs of `─` joined by `┬` (the rule above the headers, where the divider begins) and `┼` (the rule below). Junctions land exactly on the `│` columns.
- Lane header text becomes `LABEL count` — no parentheses — with the count in a muted style and the label in `HeaderColor`. This also affects the workspace plugin's board; that consistency is intended.
- `CellEmpty` with an empty `Message` renders a dim `·`. `CellEmpty` with a message, `CellLoading` and `CellError` keep rendering the message as today.
- When a lane holds more cards than the visible window, the final content row of that column shows a muted `▾ N more`. It is an indicator, not a card: it must not be clickable and must not appear in `Regions`.
- Hit regions must keep matching the painted geometry after the width change.

### Do not

Change the `mouse` integration, selection semantics, scroll behaviour, or `internal/plugins/workspace`. If a workspace test fails, the kanban change is wrong.

## Phase 3 — `internal/overview`

All in `model.go`.

- `syncBoard` builds `kanban.Card{ID, Lines}` instead of the four strings. Delete the custom `renderCard` if `defaultCardLine` covers it.
- Line 1: spine + project name (project hue, bold) + kind glyph (muted) + workspace name (`TextPrimary`).
- Line 2: spine + agent chip (`AgentColor` on `AgentChipFill`) + status label in `LaneColor`, prefixed `▲ ` when `Presentation.Attention` — plus ` · ` and a relative age from `Presentation.ChangedAt`.
- Line 3: spine + branch (worktree) or `tmux <TmuxName>` (shell), muted. Prefer `TaskID` over `Branch` where the current `choose` call already does.
- Spine glyph: `▌` for `KindWorktree`, `▏` for `KindShell`, in `ProjectHue(ProjectKey)`.
- Abnormal freshness (`m.refreshing[key]`, `m.stale[key]`) appends `· refreshing…` or `· stale` to line 3 rather than occupying a line of its own.
- Relative age: a small helper — `12s`, `3m`, `1h`, `2d`. Under 5s reads `now`. Zero `ChangedAt` renders nothing (and no separator dot).
- Lane labels become `WORKING`, `NEEDS ATTENTION`, `DONE`, `IDLE`, `PAUSED`, each with `HeaderColor: styles.LaneColor(<lane id>)`.
- Drop the `"No agents"` empty message so the component's dim `·` applies.
- Sort within a lane: project group in configured project order (`m.projects` order, which `projectOrder` already builds), then `ChangedAt` descending inside the group. This replaces the current recency-first comparator.
- `summary()` gains an agent count: `12 projects · 16 agents`. Keep the existing loading and `tmux unavailable` strings.
- The compact fallback (`renderCompact`) must keep working. Give it the project hue and agent colour too, but it stays one line per card.
- Error cards (`project unavailable`) keep their current lane and use a muted spine.

## Testing

Every phase adds table tests next to the code it changes.

- `styles`: `ProjectHue` stability for a fixed key and ramp; unknown provider falls back to `TextMuted`; each derivation fires only when its field is empty; a supplied `AgentColors` entry overlays the default for that key and leaves the others; invalid hex in an override is rejected.
- `kanban`: **the alignment invariant is the important one** — for a range of widths and lane counts, assert every rendered row has identical display width (`ansi.StringWidth`) and that `┬`/`┼`/`│` occupy the same column indices on every row that has them. Also: span truncation never exceeds the cell; `Lines` takes precedence over the string fields; the string-field path is unchanged; the overflow indicator appears only when cards are hidden and never in `Regions`.
- `overview`: ordering groups by project then recency; age formatting boundaries; a shell card and a worktree card produce different spine glyphs and kind glyphs; refreshing and stale states land on line 3; empty lanes carry no message.

Existing tests in `internal/overview`, `internal/app`, and `internal/plugins/workspace` must pass unchanged. If one needs editing, that is a signal to re-read this contract before editing it.

`internal/plugins/workspace` pins its own tmux server in `TestMain`. Never run `tmux kill-server`, and never target the default socket.
