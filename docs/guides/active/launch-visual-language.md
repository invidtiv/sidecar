# Launch visual language

For what Sidecar looks like *today*, read [../../reference/design-language.md](../../reference/design-language.md); this file is the launch design study that theme was transcribed from.

Source of truth: Claude Design project `3172ac49-4413-4a60-9235-0afa5c77cf77`,
file `Agenda TUI Refresh.dc.html`. Read it with the `DesignSync` MCP
(`get_file`) rather than re-deriving from screenshots.

The doc covers twelve rendered screens. The ones that matter here:

| Option | Screen |
| --- | --- |
| `3a` | Agenda/outline rows — the base row grammar |
| `4a` | Full Sidecar shell: header, tab strip, footer, td plugin, detail rail |
| `5a`–`5g` | git, files, conversations, workspaces, notes, command palette, create form |
| `6a`/`6b` | Outline on a real column grid |

## Palette

Every colour in the doc, with the role it plays. Dark scheme; `#e8e6e1` is the
design-doc page background and is not part of the theme.

### Structure

| Hex | Role |
| --- | --- |
| `#0f1113` | canvas background (`BgPrimary`) |
| `#131619` | header and footer bars (`BgSecondary`) |
| `#171b1f` | selected row background (`BgTertiary`) |
| `#1c2126` | hairline rules under header / above footer (`BorderMuted`) |
| `#242a2e` | inactive drag-rail dots |
| `#2f3438` | section-header rule glyphs (`────`) |
| `#3c4247` | recessive metadata — timestamps, ids in gutters, key labels |
| `#5a6167` | muted labels, counts, section headers that are not active |
| `#7b848c` | inactive tab text (`TabTextInactive`) |
| `#8b9298` | body secondary text (`TextSecondary`) |
| `#cfd3d6` | body primary text (`TextPrimary`) |
| `#e2e6e9` | emphasised row text |
| `#ffffff` | selected/active row title, app name |

### Accents

| Hex | Role |
| --- | --- |
| `#c0982f` | gold — the single primary accent: active tab, cursor `❯`, footer key glyphs, active section header, P2 |
| `#4a8f8f` | teal — identifiers (issue ids, branches, agent names), open state |
| `#5b8f63` | green — success / done / live (`#5f8a76`, `#7fae86` are lighter variants used on filled chips) |
| `#b0574f` | red — failing, P1, destructive |
| `#9a6fb0` | purple — reviewable lane (`#8a6d80` recessive variant) |
| `#4b8fd6` | blue — links and markdown headings |

Gold is deliberately the *only* strong accent in chrome. Everything else earns
its colour from semantics (id, state, priority), never from decoration.

## Layout grammar

1. **No drawn boxes around panels.** Panes are separated by whitespace and the
   grip rail, not by borders. The command palette (`5f`) is the one exception —
   it floats, so it keeps a border.
2. **Section headers are a rule, not a box:**
   `LABEL ` + `─`-fill in `#2f3438` + right-aligned count/id. Active section
   label is gold; inactive is `#5a6167`.
3. **Tab strip, not pills.** Centre-aligned, space-separated plugin names in
   `#7b848c`; active one gold and bold. Left edge is `sidecar / <scope>`, right
   edge is the clock.
4. **One right-hand column.** Dates, counts and states all share a single
   right-aligned 80px-equivalent column. Nothing else floats right.
5. **Cursor is `❯ ` in gold** plus a `#171b1f` row background. Non-selected rows
   get two spaces of gutter so text never shifts.
6. **Drag rail** is a vertical column of `┆` in `#242a2e`, with the four cells
   under the pointer promoted to `┃` in `#5a6167`.
7. **Footer** is one line of `key`(gold) + ` label `(`#8b9298`) pairs ordered by
   frequency, with a recessive `↻ HH:MM:SS` refresh stamp on the right.
8. **Relative times are coarse** — `2h`, `yest`, `21:58`. No second-level
   counters anywhere.
9. **Empty sections say so** rather than disappearing (`6a`); `6b` shows the
   collapsing variant.
