# Sidecar Configuration — implementation decisions

Arbiter decisions for the full-mockup implementation run (2026-08-15). Companion to
`sidecar-configuration-design.md` (brief) and `sidecar-configuration-seam-map.md` (recon).
Implementation agents follow these unless they hit a contradiction — then they stop and report.

## Resolved design questions (from the brief §Questions)

1. **First live pages:** all mockup pages, full scope (user decision).
2. **Remember last section:** no. The gear and `sidecar setup` always open Sidecar Setup;
   a deliberate direct destination is honored only when a caller supplies one.
3. **Apply timing:** settings apply immediately through the existing save boundary.
   Restart-scoped settings (panel enablement, some feature flags, interactive terminal keys
   for existing terminals) say so inline next to the control at save time — muted, honest,
   one line. Nothing blocks the save.
4. **Diagnostics:** in-app page per the mockup. The existing `!` diagnostics modal stays;
   the new page is the durable home. (Later consolidation is out of scope.)
5. **First-run:** never auto-opens Configuration. Empty states expose the contextual action
   (mockup screen 00); the gear is always present.

## Gap rulings (seam map §12)

| # | Gap | Ruling |
|---|-----|--------|
| 1 | Update channel selector | **Cut.** No channel subsystem exists; it implies release-infra work. About renders update status without a channel control. Flagged to Marcus as the one real "substantial subsystem". |
| 2 | Notes/Tasks enable route (08a) | **Diverge from mockup where reality differs.** Notes is in-repo with no external command: toggling Notes just flips `notes_plugin` (with restart note) — no install route. Tasks has a real external suite (`version.TasksDescriptor`): the parameterized enable route does the PATH check, Homebrew check, and a user-confirmed `brew install marcus/tap/tasks` (new small install-exec path reusing `version.Runner`). The route is parameterized so future external integrations reuse it. |
| 3 | Truecolor detection | **Build small.** Detect via `COLORTERM` (truecolor/24bit), `$TERM` heuristics, and `$TERM_PROGRAM` for terminal identification. Repair route 01b with generic instructions + terminal-specific copy only for recognized terminals (iTerm2, Apple Terminal, WezTerm, Ghostty, kitty, Alacritty as data table). No emulator config is ever touched. |
| 4 | tmux version | **Build.** Parse `tmux -V`; check >= 3.0. Availability check stays `LookPath`. |
| 5 | Header clock | **Build.** `ui.showClock` finally gets a renderer inside `headerGeometry`'s width budget; drops first under narrow-width pressure. Update the view_test assertions accordingly. |
| 6 | Per-project "Open in" preference | **Build small.** New optional `projects.list[].openIn` preference; when set it pre-selects/prefers that app in the Open-in flow; last-used memory remains the fallback. Configurable from the Projects page per the brief. |
| 7 | Copy-on-select | Backed. Save immediately; note applies to new terminals (existing hosts keep their config until rebuilt). |
| 8 | Project rename/remove/reorder | **Build.** Through Load→mutate→Save. Remove requires config-confirm. Rename/remove leave `state.json` workdir keys untouched (stale keys are harmless); do not migrate state in this slice. Reorder via keyboard (and mouse where cheap). |
| 9 | Path completion | **Build.** User-initiated after a typed prefix; never enumerates before typing; Tab accepts; arrows/mouse navigate. |
| 10 | Settings search | **Build as a hand-written static index** (page, item label, keywords). No schema framework. |
| 11 | `plugins.notes` dead config | Leave dead. Enablement remains `features.flags`. Do not wire the field in this slice. |
| 12 | Panel toggles restart-scoped | Accept; inline "takes effect after restart" note on save. No live re-plan in this slice. |
| 13 | Nerd Font runtime apply | **Build trivially:** assign `styles.PillTabsEnabled` on save so it applies live. |
| 14 | About doc links | **Build small:** open the docs URL via the OS opener (`open` on darwin); reuse/extend the cleanest existing seam. |

## Standing constraints for every implementation agent

- Read, in order: the brief, the mockup (`~/code/tui/mockups/sidecar-design.tui.yaml`),
  the seam map, this file. The brief + mockup are the fidelity contract.
- Reuse the named seams; do not invent parallel systems (no settings schema framework,
  no new modal stack for navigation — explicit route states with parent-return).
- All persistence through `internal/config` Load→mutate→Save. Never rewrite config
  outside that boundary; never mutate anything without the confirmation the brief requires.
- Shared presentation components live in one place (`internal/app/config*` or a
  dedicated package) and every page composes them; no copy-pasted layout.
- `go build ./...` and `go test ./...` green before every commit.
- Never touch the default tmux server; proofs only via `scripts/tmux-drive.sh`
  (run `paths` first, always `stop`).
- Commit at coherent boundaries with focused messages; do not push.
