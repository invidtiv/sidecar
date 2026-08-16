# Sidecar Configuration — Design Notes

Status: exploratory design brief. This records the direction so far; it is not yet an implementation plan.

## Design artifacts and handoff

An implementation agent should read this brief first, then open the companion interactive TUI Studio mockup at `~/code/tui/mockups/sidecar-design.tui.yaml`. The mockup's named screens and per-screen implementation notes are the visual and interaction reference; this document explains the product intent, scope boundaries, and implementation constraints behind them.

The intended implementation home is the Sidecar repository containing this brief. Treat the two artifacts together: use the mockup to preserve layout, hierarchy, focus, hover, and route behavior; use this brief to resolve why a screen exists and which behavior must remain shared with the existing application. If a material conflict or ambiguity remains after reading both, ask before changing the user journey.

### Existing TD context

The existing **Configuration and setup design reference** epic (`td-9a8176`) remains open as historical context and a place to find the retained supporting work; it is not a ready-to-execute version of the old Doctor proposal.

- **Already tracked and still useful:** the first-run/empty-state journey audit (`td-f2536e`), the contextual Shell-versus-Workspace explainer (`td-f81139`), and a small optional isolated fixture for safe manual proof (`td-2d804e`). The fixture is intentionally not a prerequisite for the first Configuration slice.
- **Closed as superseded:** the full command-line `sidecar doctor` story (`td-575f4e`) and the pretyped Doctor-shell empty-state story (`td-2240a7`). Do not reopen or implement them from their old descriptions.
- **Stories an implementer should create when starting:** only the focused first-slice work selected from this brief—typically the full-frame Configuration route and gear entry, Setup/empty-state recovery and focused repairs, real setting persistence through existing boundaries, narrow `sidecar setup` launch routing, and visual/interaction proof. Split or sequence those only when concrete dependencies appear; do not create a large speculative epic merely to mirror every mockup screen.

## Product intent

Create a durable, keyboard-first Configuration experience for Sidecar.

It should help a first-time user become productive, while also being the ordinary place an experienced user goes to inspect and change Sidecar settings. It replaces the narrow framing of a one-purpose `sidecar doctor` flow without losing the useful diagnostic journey.

The reference is Apple System Settings: a persistent left navigation panel, a focused detail pane, and quick movement between categories.

## Current direction

- A gear control in Sidecar’s header opens Configuration.
- Configuration takes over Sidecar’s central content area; Sidecar’s normal header and footer remain visible.
- The view uses Sidecar’s existing rounded, gradient-bordered frame rather than a generic dialog treatment.
- Escape returns to the exact prior Sidecar surface and focus context.
- The active Configuration destination is visibly highlighted in the sidebar on every page.
- Configuration is not a dismissible welcome overlay or a one-time wizard.
- First-run guidance is a section within Configuration. Existing users use the same navigation to inspect and change settings.
- The first page is named **Sidecar Setup**. It is an action-oriented readiness home, not a passive status page or a one-time wizard.
- Setup lists only meaningful issues and lets the user open a focused repair flow for each one. It explains a change before making it.
- Empty or blocked product surfaces must provide a thin, situational route back to Configuration. This keeps Setup discoverable after a restart, a shell handoff, or a user leaving the guided flow.

## Recommended first slice

Build the full-frame in-app experience first, with a small set of real settings rather than a general settings framework.

Suggested initial sections:

1. General — startup behavior and theme.
2. Projects — configured project list and the path-adding journey.
3. Terminal — visible status and the relevant Sidecar terminal settings.
4. Diagnostics — a clear route to environment checks and actionable next steps.

The first-run page can simply point to incomplete areas, for example “Add a project” and “Check your terminal.” It should be skippable and remain available later.

For the initial mockup, Sidecar Setup shows two actionable issues—adding a project and setting up tmux—alongside confirmed-ready items. Enter opens the selected repair rather than trying to fix every issue inline.

### Repair-flow prototype: tmux

Selecting the tmux fix opens a focused repair screen rather than the general Terminal page. It states the observed issue, explains why Sidecar needs tmux, and recommends the next safe action. On a Homebrew machine it offers to open a new Sidecar shell with `brew install tmux` prefilled, but never executes the command automatically or uses sudo. Copy command, recheck, and not-now are explicit alternatives. This is the prototype for other Setup repairs.

## Entry points and failure recovery

Configuration is an in-app experience, not a second full-screen command-line TUI. The normal entry is the header gear, placed immediately beside the project selector and registered as an ordinary mouse hit region and keyboard action. It opens **Sidecar Setup** by default and remembers a deliberate direct destination only when a caller supplied one.

Add `sidecar setup` as a narrow launch command. It should start Sidecar normally with **Sidecar Setup** selected; it does not duplicate configuration controls, validation, or rendering in the terminal. A future non-interactive configuration command is a separate product decision, not an implication of this entry point.

Startup recovery is part of that command's promise:

- If startup reaches the app, show the normal in-app Setup route.
- If startup fails before Sidecar can render (for example, malformed configuration, an inaccessible config/state path, or a terminal initialization failure), exit nonzero with a short plain-language diagnosis and the specific recoverable next step when known.
- The fallback should offer a privacy-preserving support path: how to inspect or copy the error summary, documentation/help, and a link to file a GitHub issue. It must never upload logs, configuration, or a support bundle automatically, and it must not print secrets from configuration.
- A later opt-in diagnostic bundle can include Sidecar version, OS/terminal facts, and a redacted startup summary. It should be generated only after user confirmation and be easy to review before attaching to an issue.
- Do not silently fall back to a different settings UI or attempt to rewrite a user's configuration in order to launch.

The current CLI has agent-oriented commands and a `sidecar open` request path for a running instance, but no `setup` command or settings-routing request. Implement `sidecar setup` as a small explicit startup/route handoff rather than overloading `open` or inventing a parallel TUI protocol.

### Recovery entry points inside Sidecar

The header gear remains available on every normal Sidecar surface. Empty and blocked states add a short contextual recovery action rather than a large onboarding screen:

- A no-project or empty-workspace state points to Sidecar Setup or directly to Projects/Add Project, depending on the missing prerequisite.
- A diagnostic failure points to its focused repair route; returning always restores the parent page and preserves an in-progress draft where applicable.
- The existing project selector currently reports that no projects are configured and supports the existing add-project flow. The new empty-state Configuration action is an intentional addition, not behavior to assume already exists everywhere.

This keeps a user who leaves Setup to inspect an otherwise empty area from landing at a dead end after restart.

## Proposed information architecture

The sidebar should reveal the product in terms users understand, not mirror the JSON shape. Sidecar Setup is the default entry; the rest is available when a user has a reason to adjust it.

### Setup

**Sidecar Setup** is the action-oriented landing page. It summarizes only meaningful readiness work and opens focused repairs. Initial checks: project list, tmux availability, terminal color capability, and agent-instructions availability.

### Sidecar

**Appearance**

- Unified, searchable built-in and community theme list, with four-color swatches and a result count.
- Theme preview occurs as selection moves. Enter saves and Escape restores the prior resolved theme.
- Global and current-project scope selector when the active project is configured; project scope overrides the global theme.
- Project scope is an explicit project selector, not an implicit “this project” context. With no configured projects, only the global theme is available until the user adds one.
- Nerd Font display.
- Terminal window title.
- Header clock.

**Projects**

- Add, rename, remove, and reorder known projects.
- Per-project theme and preferred “Open in” application.
- Per-project worktree setup override.
- Keep the page action-first: a compact count and prominent **Add project** control, followed by the configured-project list. Selecting a project reveals its location, relevant overrides, and clearly separated edit/remove actions. Do not add defensive disk-scanning copy when Sidecar is not auto-discovering projects.
- **Add project** opens a focused configuration route with Name, Location, and optional Theme fields. Location completion is user-initiated after a path prefix is typed; it can navigate matching folders with keyboard or mouse, accept with Tab, and must not enumerate directories before the user begins typing. Save validates name/path uniqueness and directory accessibility, then returns to Projects with the new project selected.
- This establishes the configuration form pattern: labels occupy a fixed left column, their inputs begin at one shared column, and dependent help or completion content aligns with its input rather than with the pane edge.
- In Add Project, Theme expands the shared picker inline beneath its form field. It must preserve the project draft and the Projects sidebar selection; Escape collapses the picker back to the Theme field rather than leaving the route. The picker shares Appearance's theme data, filtering, swatches, preview, and mouse/keyboard behavior.
- The inline picker has a visibly editable search field, spacing before the result list, and a distinct clickable/keyboard-focusable **Use global theme** action. Do not rely on an unadorned list row to express that reset action.

**Workspaces**

- Default agent for a new shell or worktree.
- Whether to create a shell automatically.
- Worktree naming and the cross-project activation scope.
- What the workspace sidebar displays.
- Worktree setup policy: copied environment files and optional setup hook, always described as potentially executing repository-provided code.
- Present Workspace settings as short, action-shaped controls in **New workspaces** and **Worktree defaults**. Use the shared fixed-label/input column and make selectors and toggles visibly interactive.
- **First version scope:** per-project worktree-setup configuration (environment-file copying and repository setup hooks) is out of scope for Configuration. Keep a concise, static explanation of the existing config: `copyEnvFiles` and `envFiles` copy startup files; `runHook` and `hookPath` run a post-creation script. Do not add a prompt-copy flow here.

**Agents**

- Which agent families appear in Sidecar’s creation choices.
- Default launch command per agent family.
- Agent-instructions status and repair action.
- Use compact agent rows with a distinct toggle, muted command metadata, and a separate action to inspect or repair instructions.
- Agent-instructions repair is a focused route within Agents. It identifies the affected project, offers review, copy, open, and recheck actions, and never overwrites an existing `AGENTS.md` without presenting the exact addition and receiving confirmation.
- The proposed `AGENTS.md` addition is intentionally one line: `For Sidecar capabilities, run sidecar agents.` The `sidecar agents` command is the canonical detailed reference, so the project instruction file must point to it rather than duplicate its contents.

**Terminal**

- Interactive mode exit, attach, copy, and paste shortcuts.
- Copy-on-select.
- Embedded terminal capture limit, under an expandable advanced area.
- Present owned terminal settings as aligned shortcut, toggle, selector, and drill-in controls; keep one muted sentence for advanced capture behavior.
- Under **Attach to tmux**, add aligned muted help: it opens the full tmux client instead of Sidecar's embedded terminal and should remain disabled unless the user relies on tmux's own interface and shortcuts.

**Panels & integrations**

- Enable or disable the Git, Files, td, Conversations, Notes, and Tasks surfaces where Sidecar actually supports that choice.
- Configure their small number of meaningful inputs, such as td database location, conversation source directory, refresh behavior, and Tasks placement.
- Explain when a panel is unavailable because its supporting tool is not configured.
- Make the entire panel row focusable/clickable and render its ON/OFF state as a distinct control at the right edge.
- **Notes** and **Tasks** are beta integrations. Before enabling either, check its required command on `PATH` and Homebrew availability. If the command is missing but Homebrew is available, offer an explicit, user-confirmed install; never install automatically. The same parameterized repair route should handle both integrations.

### Future external-system settings

Configuration is intentionally able to host setup and status for systems that make Sidecar substantially more useful, without claiming ownership of their underlying data or replacing their native interfaces. **TD** is the first expected example: Sidecar should eventually expose a TD section or integration route that detects whether TD is available and configured, explains its value to Sidecar's workflow, and hands the user into TD's own setup or repair journey as appropriate.

Do not design or implement that TD surface in this slice. Keep the information architecture, page model, focused-repair model, and integration boundaries flexible enough to add it later. Sidecar should present a clear guided path and status; TD remains the owner of TD configuration, validation, persistence, and non-interactive commands. This boundary prevents a partial duplicate TD setup experience while making TD discoverable from the same place users configure Sidecar.

### System

**Diagnostics**

- The detailed form of Setup checks, including verified state, explanation, and safe repair or handoff.
- Never hides a problem merely because Setup is not the current page.
- Include a visible Recheck action in the page header; problem rows lead to focused repairs, while healthy rows remain quiet confirmation.
- In Diagnostics, **Agent instructions** is always a navigable row that opens the Agent Instructions route. Healthy **Configuration** and **Projects** rows are informational; only an invalid configuration or an empty/misconfigured project state should turn those rows into focused repair actions.
- Show a dedicated Diagnostics problem-state mockup: invalid Configuration opens a safe config-recovery route (open file, copy details, recheck; never automatic rewriting); no Projects routes directly to Add Project with Location focused; missing Agent Instructions opens its repair route.

**Advanced**

- Feature previews and technical performance limits.
- Deliberately separated from ordinary settings, with restart/reload messaging where needed.
- Use the shared aligned controls and explicit ON/OFF states, preceded by one muted reminder that these settings are optional.
- Each feature preview has an immediately following, input-aligned muted explanation. Keep the preview control labels and right edges aligned across the group, including Performance controls.
- Clamp Terminal preview capture to a deliberately bounded accepted range before saving, for both typed and selected values. Keep the safe default when input is blank or invalid; exact bounds remain an implementation decision to document alongside validation.

**About**

- Version, installation provenance, update status, and support material.
- Treat update channel/status as aligned controls and make help actions visibly clickable rather than presenting them as bare shortcut prose.
- When a check finds an update, render an explicit **Open updater** action. It launches the existing Sidecar updater modal; Configuration must not duplicate updater confirmation, progress, or install behavior.

## Shortcut model

Configuration needs explicit shortcut contexts from the start:

- `config` — sidebar navigation and page-level actions.
- `config-edit` — an active editor receives ordinary typing without global shortcuts stealing it.
- `config-confirm` — a consequential change has an explicit confirmation/cancel path.

The footer, help, and command palette should be driven by those registered bindings. This keeps the visible affordances, keyboard behavior, and user customizations in agreement.

Required interaction rules:

- The gear and command palette open Configuration.
- Escape closes Configuration and restores the previous active context.
- When editing a value, normal typing is reserved for that editor.
- Any destructive or disruptive mutation is explicit and recoverable where practical.
- Configuration opens with sidebar navigation focused, not Search, so Up/Down works immediately. Slash focuses Search and begins filtering.
- A non-empty Search query uses a visibly active input background and a matching-result count. Down from Search moves to the first visible result; Up returns to Search.
- Escape first clears an active Search query and restores the full sidebar. Only a subsequent Escape closes Configuration.
- Focused repair routes use a visible top-row **Back to [parent]** control and matching Escape behavior. Terminal-color and tmux repair return to Sidecar Setup; Add Project returns to Projects. This back action preserves an in-progress child draft where applicable and never applies unsaved changes.

## Implementation fidelity contract

This brief and the TUI Studio mockup are the behavioral and visual contract for implementation. They are not a literal build prompt, but an implementation should be recognizably faithful to them rather than treating them as loose inspiration. Screen notes in the mockup carry the same weight as this document.

### Design principles

- **Keep Sidecar recognizably Sidecar.** Retain the normal header and footer, rounded gradient-bordered frame, active sidebar treatment, and the established mouse/focus language. Configuration replaces central content; it is not a generic modal.
- **Use calm hierarchy, not density.** Section titles sit at the top of their pane with breathing room before content. Use whitespace to separate groups; do not put labels inside horizontal divider lines. Muted help is visibly quieter than primary text, but still readable.
- **Make controls unmistakable.** Buttons, toggles, selectors, focusable rows, and editable fields must have distinct states for rest, focus, hover, and active/filtering conditions. A whole repair row can be actionable, but its action must remain legible.
- **Align the system.** Forms use one fixed label column and a shared input start and end. Dependent help, completions, and nested pickers align to the associated input. Keep shared chrome and page styles data-driven so a visual correction propagates across all configuration screens.
- **Progressive disclosure over a wall of settings.** Setup shows only actionable work. Advanced and scoped options appear only when a user has a reason to reach them. Reuse the shared Theme picker, updater, and repair surfaces rather than recreating their logic in each page.
- **Integrate without taking ownership.** Configuration may guide setup for a closely related system such as TD, but it must call that system's existing configuration and diagnostic boundary rather than reimplementing its model or persistence in Sidecar.
- **One interaction model for keyboard and mouse.** Every visible action has an equivalent registered key/focus path and a real mouse hit region. Text entry owns typed characters; footer help and the command palette derive from the active context.
- **Safe, explainable repair.** State what was detected, why it matters, and what an action will do. Ask before installs, config writes, deletions, or opening an external shell; never silently alter a system or repository configuration.
- **Treat ambiguity as a design question.** An implementer may make small layout adjustments required by terminal dimensions, but must ask before changing a documented journey, scope boundary, or data-mutating behavior.

### Implementation shape

- Keep configuration state, validation, persistence, diagnostics, and repair decisions outside renderer-only code. The in-app view is a client of those shared operations.
- Reuse Sidecar's existing contextual key registry, mouse hit-map handling, gradient panel renderer, project/theme/updater behaviors, and save boundary. Avoid a schema-driven settings framework in the first slice.
- Build or reuse a small set of configuration-specific presentation components for the persistent chrome, sidebar rows and search, section headers, aligned form rows, muted input-aligned help, settings controls, status/repair rows, and focused back routes. New pages must compose those shared pieces rather than copying their layout and interaction rules; a correction to an established pattern should propagate across Configuration instead of letting screens drift.
- Implement each route as an explicit state with parent-return behavior. Do not stack generic modals to simulate navigation.
- Preserve narrow-terminal behavior deliberately: the gear, project selector, sidebar, form columns, and visible help must remain usable rather than merely clipping.

### Delivery and review bar

Walk the real journeys represented by the mockup: open from the gear; navigate with arrows, search, mouse, and Escape; add a project with path completion and inline theme selection; make and cancel a focused repair; recover from an empty workspace; and hand an available update to the existing updater. Verify that input does not leak into global or terminal shortcuts, that no repair performs an unconfirmed mutation, and that empty states remain routes back into Setup after restart. Independently review visual fidelity at ordinary and narrow terminal sizes before calling the feature complete.

## Existing implementation seams

- Sidecar already has a single active shortcut context with contextual bindings taking precedence over global bindings.
- Existing text-entry and interactive terminal modes already protect typed input from global shortcuts. Configuration should use the same approach.
- The app frame already separates header, central content, and footer; Configuration can replace only the central content.
- The app already renders rounded gradient borders for normal panels.
- The generic modal package can fill a screen, but it is optimized for transient dialogs and uses a simpler border. It is not the preferred home for a settings sidebar.
- The header has a right-pinned project selector. A gear can live immediately beside it, with a narrow-terminal layout rule and a matching mouse hit region.
- Configuration currently has a concrete config model and established save paths. The first slice should use those directly through a small application boundary rather than introduce a schema-driven settings system.

## Deliberately deferred

- A generalized dynamic settings schema or plugin settings framework.
- A standalone interactive configuration TUI.
- A terminal-native fallback settings editor when the Sidecar app cannot launch; the initial fallback is recovery guidance and opt-in support information only.
- A broad migration of every existing setting into Configuration.
- A welcome overlay, “don’t show again” state, or a competing first-run flow.
- Keyboard shortcuts are out of scope. Do not add a Keyboard page or sidebar item until editable shortcut overrides are a deliberate, verified product commitment.

## Questions to resolve during design

1. Which settings are valuable enough to make the first live pages?
2. Should the configuration view preserve its last selected section between launches?
3. Which changes apply immediately, and which need a restart or explicit confirmation?
4. How should diagnostics appear: as an inline section, a handoff to a command, or both?
5. Does first-run automatically open Configuration, or only expose a clear entry point?

## Evidence for a future implementation

- Click the gear, navigate sections with keyboard and mouse, edit a real setting, and return to the prior Sidecar surface with Escape.
- Verify field editing does not trigger global or workspace/terminal shortcuts.
- Verify `sidecar setup` opens the normal Sidecar app directly on Setup without introducing a duplicate settings implementation.
- Induce representative pre-render startup failures in isolation and verify the terminal fallback is concise, actionable, nonzero, and never exposes or uploads private configuration automatically.
- Capture a screenshot at a normal terminal size showing the gradient frame and sidebar/detail layout.
- From an empty Workspaces surface, click the inline Configuration action and land on the relevant Setup or Workspaces page; the same journey must work after a restart.
