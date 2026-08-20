# Plan: One Create Workspace modal

**Status:** Implemented on `worktree-modals` (td-7dc867), 2026-08-20
**Created:** 2026-08-20
**Scope:** Replace every project and global workspace-creation **form** with one modal. Add auto-approve on both Shell and Worktree. Add base-branch selection for worktrees on the global surface. Delete the old type-selector and worktree-only form paths. Instant-create shortcuts stay instant.
**Out of scope:** the visual-language rebuild in [modal-redesign.md](../active/modal-redesign.md); Fetch PR (`F`); agent-config / resume / rename / delete; changing `ctrl+n`, Shells `[+]`, or `autoCreateShell`.
**Related:** [create-worktree-modal.md](create-worktree-modal.md), [workspaces-cross-project-completion.md](workspaces-cross-project-completion.md), [workspace-create-extraction.md](workspace-create-extraction.md)
**Shipped:** `6c0fbbae`..`1863f2f1`. Isolated proof td-185db8.

---

## 0. The one-paragraph answer

Creating a shell or a worktree from a form is one job with a kind toggle. Today
that job is three forms, split across project Workspaces and global Sessions,
with auto-approve and base branch living on only some of the paths. The target
is a single **Create Workspace** modal — Shell | Worktree, then the fields that
kind needs — opened by every **modal** create action on both surfaces.
Instant-create shortcuts (`ctrl+n`, Shells `[+]`, `autoCreateShell`) are
unchanged. Worktree confirm / setup-recovery stays the second step after
submit. `internal/workspaceops` keeps doing the mutation.

The modal's speed path is last-used agent and that agent's last auto-approve
already filled, focus on Name: type a name, Enter.

---

## 1. Journey

A user who opens a create **form**, on either surface, always lands here:

```
Create Workspace

 [ Shell ] | [ Worktree ]

Project                         ← global only; hidden in the project plugin
[ marcus-skills               ]

Name
[ feature name                ]
  git: feature-name             ← worktree only, when slug ≠ display

Base Branch                     ← worktree only
[ main                        ]

Agent
[ Codex CLI                   ]

 [x] Auto-approve all actions   ← when the selected agent has a skip flag
      (Adds --dangerously-bypass-approvals-and-sandbox)

 [ Create ]  [ Cancel ]
 Tab to switch · Enter to confirm · Esc to cancel
```

- Kind is a segmented control, not a stacked list.
- Switching kind keeps Project / Agent / Name / Auto-approve; it shows or hides
  Base Branch, changes the name placeholder, and reorders the agent list
  (None first for shells, None last for worktrees). The selected agent is kept
  when it is still in the new list.
- Shell name is optional (placeholder is the next "Shell N"). Worktree name is
  required; git branch and directory still come from `SlugifyWorktreeName`.
- Agent and auto-approve are prefilled from last successful **modal** create
  (see §3.7). The focused field on open is Name, so the usual keystroke is
  type-name, Enter.
- Enter from a text field submits (Combo overlay commits first). Esc closes.
- Worktree submit still opens the existing confirm / setup-recovery step. That
  step is not this form.

Instant create does not go through this form. `ctrl+n` and Shells `[+]` keep
their current skip-the-form behaviour on the surfaces that already have it.

---

## 2. Current state (verified 2026-08-20)

Three live forms, two hosts, eight entry points.

### Forms to remove

| Form | Host | File | What it is |
| --- | --- | --- | --- |
| **Create New** (Shell / Workspace list) | project plugin | `internal/plugins/workspace/view_modals.go` `ensureTypeSelectorModal` | `ViewModeTypeSelector`. Stacked list. Shell expands into name + agent list + auto-approve. Worktree confirm then opens the third form. |
| **Create New** (expanded shell options) | same | same | Same modal, Shell selected. This is the only project **form** that can start a named shell with an agent and auto-approve. |
| **Create New Worktree** | project plugin | `internal/plugins/workspace/create_modal.go` `ensureCreateModal` | Name, Base Branch combo, Agent combo, auto-approve. No kind toggle. No project picker. |

### Form to keep and extend

| Form | Host | File | Missing vs target |
| --- | --- | --- | --- |
| **Create Workspace** | global Sessions | `internal/overview/global_create.go` `buildCreateModal` | Kind toggle + Project + Agent + Name already exist. No auto-approve (shell launch hardcodes `skipPerms=false`; worktree never sets `plan.SkipPerms`). No base branch (planner is called with `"HEAD"`). |

### Entry points today, and what happens to each

Modal entries are unified. Instant entries are left alone.

| Entry | Project plugin | Global Sessions |
| --- | --- | --- |
| `n` / footer **New** | **modal** — type selector, Worktree default → unified form, Worktree, focus Name | **modal** — already unified, Worktree → same form, focus Name |
| Header `[+]` | **modal** — worktree-only form → unified form, Worktree, **kind focused** | **modal** — Shell, kind focused → Worktree, kind focused |
| Worktrees section `[+]` | **modal** — worktree-only form → unified form, Worktree, focus Name | n/a |
| Section `[+]` | n/a | **modal** — Shell, kind focused, project prefilled → Worktree, kind focused, project prefilled |
| Palette `new-worktree` | **modal** (same as `n`) | **modal** (same as `n`) |
| `OpenCreateModalWithTaskMsg` (td monitor) | **modal** — worktree-only, name prefilled → unified form, Worktree, name prefilled, focus Name | n/a |
| Palette `new-shell` / `ctrl+n` | **instant** — `createDefaultShell` (default agent, auto-approve off). Unchanged. | **modal** — already `OpenCreateShell`. Becomes the unified form, Shell, focus Name. |
| Shells section `[+]` | **instant** — `createNewShell("")` (plain shell, no agent). Unchanged. | n/a |
| `autoCreateShell` | **instant** (not a shortcut). Unchanged. | n/a |

Global `ctrl+n` is already a modal, not instant create. Do not turn it into
instant create to match the project surface; do not turn project `ctrl+n` into
a modal to match global. Each entry keeps its current class.

### Parity bugs this is already paying for

- Auto-approve is a project-only checkbox. Global create cannot pass it.
- Base branch is a project-only combo. Global create always uses `HEAD`.
- Header `[+]` on the project surface skips the kind choice and opens worktree.
- Agent lists are duplicated: `internal/overview/create_agents.go` vs
  `internal/plugins/workspace/agents_config.go` (both should be
  `internal/agentcatalog`).
- Kind language is mixed: type selector says **Workspace**, the keep form says
  **Worktree**. Target language is **Worktree**.

Worktree confirm / setup-recovery already exists on both hosts
(`ensureCreateOperationModal` / `ensureCreatePlanModal`) and stays.

---

## 3. Decisions

1. **One form, two hosts, shared code.** Extract the form into
   `internal/workspacecreate` the way pane chrome lives in `paneframe`. Project
   and global bind it; they do not each own a copy. A field that lands on one
   surface and not the other is a bug.
2. **Replace modal workflows only.** Instant create stays instant wherever it
   already is: project `ctrl+n` (`createDefaultShell`), project Shells `[+]`
   (`createNewShell("")`), and `autoCreateShell`. Do not route those through
   the form. Do not invent new instant paths.
3. **Kind is preselected from the modal entry, not re-chosen in a first modal.**
   | Entry | Kind | Initial focus |
   | --- | --- | --- |
   | `n`, palette New, Worktrees `[+]`, task-create | Worktree | Name |
   | Global `ctrl+n` / palette Shell (already a modal) | Shell | Name |
   | Header `[+]`, global section `[+]` | Worktree | Kind toggle (project prefilled when the section supplies it) |
4. **Project combo is global-only.** The project plugin is already inside a
   project; hiding the field is the same form with `ShowProject=false`.
   Creating into another project from the project plugin is not in scope.
5. **Field order is Name-first.** Kind, Project (global), Name, Base Branch
   (worktree), Agent, Auto-approve, Create/Cancel. Agent sits below Name so
   Enter-to-create does not require tabbing past Agent. Base Branch is under
   Name when Worktree is selected.
6. **None (attach only) stays.** Shells: None first. Worktrees: None last.
7. **Last-used agent and auto-approve are the speed path.** On open, prefills
   are:
   - Agent: `state.LastCreateAgent` if it is still in this kind's list,
     otherwise the existing fallback (`.sidecar-agent` / `defaultAgentType` /
     first real agent for worktrees / None for shells).
   - Auto-approve: `state.AgentAutoApprove` for that agent, loaded again
     whenever the agent changes. Persist immediately on toggle. Persist last
     agent on successful **modal** create.
   Instant-create paths do not read or write these prefs (they already don't).
   The result is: most of the time the user names the shell or worktree and
   presses Enter. Last project on the global surface stays
   `saveLastGlobalCreateProject`.
8. **Base branch is a Combo** prefilled with the selected project's current
   branch (value, not placeholder), same as the project form today. Changing
   project reloads that project's branches and resets the base to its current
   branch unless the typed value is still a branch there. Empty base still
   means `HEAD` inside `workspaceops.ResolveWorktreePlan`.
9. **Title, buttons, hints.** Title `Create Workspace`. Buttons `Create` /
   `Cancel`. Modal hints on. Kind labels `Shell` and `Worktree` (not
   "Workspace").
10. **Confirm / recovery is not this form.** Worktree submit still plans, then
    shows the existing confirm modal, then setup-recovery. Shell submit from
    the form still creates immediately (no confirm step).
11. **This is not `modal-redesign.md`.** Keep Input / Combo / Checkbox / Buttons
    chrome. Do not restyle the library.

---

## 4. Shared package

`internal/workspacecreate` owns presentation and form state. It does not talk
to git or tmux.

```go
type Kind int
const (
    KindShell Kind = iota
    KindWorktree
)

type OpenOpts struct {
    Kind        Kind
    FocusKind   bool
    ShowProject bool
    ProjectKey  string          // ignored when ShowProject is false
    Name        string          // optional prefill (task-create)
    Projects    []ProjectItem   // global host
    Agents      []string        // config allowlist
    Branches    []string
    NextShell   string          // placeholder
}

type Form struct { /* inputs, indexes, skip, error, modal cache */ }

func Open(opts OpenOpts) *Form
func (f *Form) Build(width int) *modal.Modal
func (f *Form) SetBranches(branches []string, current string)
func (f *Form) AgentItems() []modal.DropdownItem
```

`Open` loads last-used agent and that agent's auto-approve before the first
render so Name is the focused field and Enter submits with the remembered
choices.

The kind toggle currently inlined in `overview.createKindToggle` moves here.
Agent labels and None-first / None-last order come from `internal/agentcatalog`
plus one `ResolvePicker(allowlist, shellMode)` helper so
`overview/create_agents.go` and `workspace/agents_config.go` stop diverging.

Hosts stay responsible for:

- listing projects (global) or supplying the current project (plugin)
- `loadBranches` / current-branch (plugin already has this; extract a
  `workspaceops.ListLocalBranches` / `CurrentBranch` so global can call it on
  the selected project path without instantiating the plugin)
- submit: `workspaceops.CreateManagedShell` / `ResolveWorktreePlan` + confirm
- overlay, mouse handler, busy/error, pending-created selection

`ViewModeCreate` on the plugin hosts both the form and the confirm/recovery
modals (as it does today for worktree). `ViewModeTypeSelector` is deleted.

---

## 5. Host wiring

### Project plugin (`internal/plugins/workspace`)

- `n`, header `[+]`, Worktrees `[+]`, `OpenCreateModalWithTaskMsg` →
  `openCreate(...)` on the shared form (see the table in §2).
- `ctrl+n` stays `createDefaultShell`. Shells `[+]` stays `createNewShell("")`.
- Submit Shell (from the form, after toggling kind) → current
  `createShell(shellCreateOpts{...})` using form name / agent / skip. Delete
  `createShellWithAgent` (it only reads type-selector fields).
- Submit Worktree → current `validateAndCreateWorktree` → confirm.
- Delete `ViewModeTypeSelector` and every `typeSelector*` field, ID, ensure,
  render, key, mouse, and command-context branch.

### Global Sessions (`internal/overview`)

- Replace `buildCreateModal`'s hand-rolled sections with `workspacecreate.Form`.
- Keep `OpenCreate` / `OpenCreateShell` / `OpenCreateWorktree`; they become
  different `OpenOpts`. `OpenCreate` (header / section `+`) uses Worktree +
  `FocusKind`. `OpenCreateShell` (`ctrl+n`) uses Shell + focus Name.
- Set `plan.SkipPerms` from the checkbox; pass it to shell
  `ResolveAgentCommand` (today the `false` is hardcoded in `submitCreateShell`).
- Pass the selected base branch into `ResolveWorktreePlan` instead of `"HEAD"`.
- On project change, reload that project's branches and current branch.

### Keymap / footer / palette

Bindings stay `n` = new-worktree and `ctrl+n` = new-shell. On the project
surface, **Shell** still means instant create. On global Sessions, **Shell**
still opens the form (as it does today). Do not add a third "open chooser"
command. Docs keep describing `ctrl+n` as immediate on the project surface.

---

## 6. Implementation slices

Steel thread: project `n` opens the shared form in Worktree, last agent and
auto-approve already set, type a name, Enter → existing confirm → create.
Then Shell on the same form (kind toggle), then global, then delete the old
paths. Instant-create tests (`TestHandleListKeys_CtrlNCreatesShell`, Shells
`[+]` mouse tests, `autoCreateShell`) must keep passing unchanged.

### Slice 1 — Shared form

**Files:** new `internal/workspacecreate` (form, kind toggle, agent items,
skip-perms visibility, last-agent / auto-approve load); `internal/agentcatalog`
picker helper; tests for kind switch, field visibility, None order, skip
checkbox, name-required vs optional, last-agent prefill.

**Depends on:** nothing.

**Gate:** `go test ./internal/workspacecreate/ ./internal/agentcatalog/`

### Slice 2 — Project plugin cutover

**Files:** `create_modal.go`, `keys.go`, `mouse.go`, `plugin.go`, `commands.go`,
`view_list.go`, `view_modals.go`, `shell.go` (`createShellWithAgent` deletion),
`types.go` (`ViewModeTypeSelector` deletion), create/mouse/wheel tests.

**Depends on:** 1.

**Gate:** `go test ./internal/plugins/workspace/` — `n` opens the shared form;
`ctrl+n` still creates a shell without a modal; Shells `[+]` still creates
without a modal; type-selector identifiers have zero references.

### Slice 3 — Global cutover

**Files:** `internal/overview/global_create.go` (and tests), extract
`workspaceops.ListLocalBranches` / `CurrentBranch` from the plugin git helpers,
delete `overview/create_agents.go` once catalog is the source.

**Depends on:** 1. Can overlap 2 after the form API is stable.

**Gate:** `go test ./internal/overview/ ./internal/workspaceops/` — auto-approve
reaches both shell start and `WorktreePlan.SkipPerms`; base is not hardcoded
`HEAD`; project change reloads branches; `ctrl+n` still opens the form (not
instant create).

### Slice 4 — Remove leftovers and docs

**Delete / strip:**

- `ViewModeTypeSelector` and all `typeSelector*` symbols
- `workspace-type-selector` keymap context if present
- root `plan.md` (obsolete type-selector design notes)
- website `workspaces-plugin.md` type-selector copy (keep `ctrl+n` as immediate
  on the project surface); keyboard-shortcuts skill table for the type selector
- CHANGELOG: one form on both surfaces; auto-approve and base branch on global;
  no change to instant-create shortcuts

**Gate:** `rg -n 'typeSelector|ViewModeTypeSelector|Create New Worktree|ensureTypeSelector' --glob '*.go'` is empty. `go test ./internal/plugins/workspace/ ./internal/overview/ ./internal/workspacecreate/ ./internal/agentcatalog/` and `go build ./...`.

---

## 7. Verification

- Package tests per slice. After slice 4, the four packages above plus
  `go build ./...`.
- Isolated `scripts/tmux-drive.sh` proof (`paths` first; nothing under
  `~/.local/state/sidecar`):
  1. Project Workspaces: `n` → Worktree selected, Agent already last-used,
     type name, snap; Tab to Shell, auto-approve appears for Codex, snap;
     Enter creates.
  2. Project: `ctrl+n` still spawns a shell **without** opening a modal.
  3. Global Sessions: header `+` opens the form with Project + kind focused
     on Worktree; switch project; Worktree shows Base Branch; Create.
  4. Global: `ctrl+n` opens the form with Shell selected (existing behaviour).
- Manual contract (headless cannot see the native cursor or easily click):
  mouse on kind toggle, combo overlay, checkbox, section `[+]`; last-agent and
  auto-approve survive reopen; global auto-approve actually lands on the agent
  command; Shells `[+]` still creates immediately.

---

## 8. Risks

| Risk | Mitigation |
| --- | --- |
| Global branch list is a git spawn per open / project change | Same as the project form (`loadBranches` in a `tea.Cmd`). Do not load in `Init()`. |
| Form rebuild on kind/project change drops in-progress input | Same cache/focus restore pattern `rebuildCreateChooser` already uses; Combo must not overwrite a focused filter query. |
| Confirm/recovery still duplicated across hosts | Accepted. This plan unifies the **chooser**, not the worktree lifecycle UI. |
| Agent catalog vs `workspaceops` skip-flag maps | Form asks `workspaceops` (or a single helper) whether the agent has a skip flag; do not add a third map. |
| Last-agent on a worktree open is None (last modal create was a plain shell) | Fallback chain in §3.7; worktree still prefers a real agent when last-used is empty or missing from the list. |

---

## 9. Non-goals

- Fetch-PR as a third kind on this form
- Linking a td task from the create form (task-create still prefills Name only)
- Unifying the worktree confirm / setup-recovery modals across hosts
- Making project `ctrl+n` / Shells `[+]` open the form, or making global
  `ctrl+n` skip the form
- A sidecar CLI for create (sidecar does not own git or the user's shells)
- Restyling `internal/modal`

---

## Changelog

- Instant create stays on project `ctrl+n`, Shells `[+]`, and `autoCreateShell`.
  Only existing modal workflows move to the shared form.
- Header / section `[+]` defaults to Worktree with the kind toggle focused.
- Field order is Name, then Base Branch (worktree), then Agent, then Auto-approve.
- Project combo is hidden in the project plugin.
- Last-used agent and per-agent auto-approve prefill the form so the usual
  action is name + Enter.
