# Plan: Create New Worktree modal — type name, hit Enter, done

**Status:** ready to implement
**Branch:** `worktree-modal`
**Scope:** create-worktree journey, reusable modal dropdown, prompt removal, worktree display-name rename
**Out of scope:** the visual-language rebuild in `docs/plans/active/modal-redesign.md` (flat tokens, Header/Choice/Actions). This plan changes behaviour and layout stability. Do not wait on that redesign, and do not restyle every modal.

## Journey

Today the modal is a tall, jumping form: name must be a git-legal branch, base-branch and task lists expand in-flow, agent is a static list, prompt is a whole extra picker, and Enter advances fields instead of creating.

Target: type a natural name, press Enter, done. Optional fields stay one line. Lists float over the form instead of stretching it.

```
Create New Worktree

Name:         [auth refresh          ]
              git: auth-refresh
Base Branch:  [worktree-modal        ]   ← real value, not a placeholder
Link Task:    [Search tasks…         ]
Agent:        [Claude Code           ]
              [ ] Auto-approve all actions

[ Create ]  [ Cancel ]
```

Focusing Base / Task / Agent opens a floating filtered list over the fields below. Modal height does not change.

## Current code

| Area | Where |
| --- | --- |
| Form | `internal/plugins/workspace/create_modal.go` |
| Keys / Enter / focus | `internal/plugins/workspace/keys.go` (`handleCreateKeys`, `validateAndCreateWorktree`) |
| Mouse | `internal/plugins/workspace/mouse.go` |
| Init / defaults | `internal/plugins/workspace/plugin.go` (`initCreateModalBase`, `getDefaultCreateAgentType`) |
| Git plan | `internal/plugins/workspace/create_operation.go` (`resolveCreateOperation`, `CreateOperationPlan`) |
| Name validation | `internal/plugins/workspace/worktree.go` (`ValidateBranchName`, `SanitizeBranchName`) |
| In-flow task list | `internal/plugins/workspace/task_picker.go` |
| Prompts | `prompts.go`, `prompt_picker.go`, `prompt_picker_modal.go`, `agent_config_modal.go`, `.claude/skills/create-prompt/` |
| Shell rename (exists) | workspace `R` on a shell; overview `R` on a shell. `R` on a worktree is ignored |
| Worktree list name | `snapshotToWorktrees` / `parseWorktreeList` derive `wt.Name` from the directory |

`CreateOperationPlan` already has `DisplayName` and `Branch`, but `resolveCreateOperation` feeds the same string to `git check-ref-format`, the destination path, and `DisplayName`. Spaces fail validation.

Base branch is stored as a placeholder (`"main"`), not a value, so it looks like a hint even after `loadBranches` returns.

## Key decisions

1. **Two names from one field.** The typed string is the display / shell name. Git branch and sibling directory use a slug. No second input.
2. **Slug is silent.** No red X / “contains space” errors. A single dim line under Name (`git: auth-refresh`) when the slug differs. Submit fails only if the slug is empty or the branch/path already exists.
3. **Display name persists** in the worktree state dir (`projectdir.WorktreeDir` + file `display-name`), same pattern as `base` and `agent`. Refresh overlays it onto `wt.Name`. Existing worktrees with no file keep the path-derived name.
4. **Floating dropdown is a modal primitive**, not a workspace custom section. Create modal is the first consumer; agent-config agent picker adopts it when prompts die.
5. **Enter from any text field submits.** If a dropdown is open, commit the highlighted item into the field first, then create. Tab / click-away commits the highlight without submitting. Esc closes an open dropdown; a second Esc (or Esc with no dropdown) closes the modal.
6. **Last-used agent is global** (`state.json`), and wins over `.sidecar-agent` and `defaultAgentType` when opening the create modal. Written on successful create and on Start from the agent-config modal.
7. **Auto-approve is per-agent and global.** Default unchecked until the user has toggled that agent on. Persist immediately on toggle. Changing the agent reloads that agent’s stored value.
8. **Prompts are deleted**, not hidden. Skill, picker, create-modal section, agent-config section, `CreateOperationPlan.Prompt`, `StartAgentWithOptions`’s prompt argument. Task-linked agent launch still injects task context via the existing no-prompt path in `buildAgentCommand`. Do not strip unmanaged `prompts` keys from user `config.json`.
9. **Worktree rename is display-name only.** Do not rename the git branch or directory. Shell rename already works in both views; extend `R` to a selected worktree.
10. **This is not `modal-redesign.md`.** Keep existing Input/Buttons/Checkbox chrome. The new Combo sits beside them.

## Slug rules

Pure function, no I/O. Extend `SanitizeBranchName` or add `SlugifyWorktreeName` next to it:

- trim
- lowercase
- spaces → `-`
- drop `~^:?*[\\` and `@{`
- drop control characters
- collapse consecutive `-` / `/` / `.`
- strip leading/trailing `-` `.` `/`
- refuse `@`, `.lock` suffix, empty result
- truncate to **63 runes** on a dash boundary when possible

`resolveCreateOperation(ctx, workDir, projectRoot, rawName, base, …)`:

- `display := strings.TrimSpace(rawName)` — this is `plan.DisplayName`
- `slug := SlugifyWorktreeName(display)` — this is `plan.Branch` and the destination directory component
- `dirPrefix` prefixes the **slug**, not the display name
- `check-ref-format` and “branch already exists” run on the slug
- `createdWorktree` sets `wt.Name = plan.DisplayName`
- after add/setup, write `display-name`

When a shell / agent session is created for this worktree, its `shells.json` `displayName` is the raw display name (then `shellstate.NormalizeName`, which already allows spaces, max 50 bytes). Tmux session names keep using `sanitizeName`.

Name field `CharLimit` can stay 100. If the display name is longer than `shellstate.MaxNameBytes` when creating a shell, normalize/truncate at that seam only.

## Floating dropdown

New primitive in `internal/modal`:

```go
type DropdownItem struct {
    ID    string
    Label string
    Value string // written into the input on commit; default Label
    Desc  string
    Data  any
}

func Combo(id string, input *textinput.Model, items []DropdownItem, selected *int, opts ...ComboOption) Section
```

Options: max visible (default 8), open-on-focus (default true), custom filter, submit-on-enter (default true).

**Layout (the whole point):** `RenderedSection` gains an optional overlay that does **not** count toward section height. `buildLayout` measures and scrolls the closed form only, then composites the overlay onto the viewport over later fields/buttons and registers overlay hit regions last.

If the overlay would clip off the bottom of the modal viewport, flip it above the field. Do not grow the modal.

**Keys (focused Combo):**

| Key | Behaviour |
| --- | --- |
| typing | filter; select index 0 (top match) |
| up / down / ctrl+p / ctrl+n | move highlight |
| Enter | commit highlight into the input; if submit-on-enter, return the modal primary action |
| Tab / Shift+Tab | commit highlight, close overlay, let the modal cycle focus |
| Esc | close overlay and consume the key (do **not** cancel the modal) |

`Modal.HandleKey` must route Esc to the focused section **before** returning `"cancel"`. Today Esc is intercepted first (`modal.go`); that has to change or Combo cannot be reused.

**Mouse:** click item commits (and does not submit). Click another field / backdrop follows existing modal rules.

**Tests (package `modal`):** overlay does not change measured height; filter selects index 0; Esc is consumed; Tab commits; Enter returns primary action; mouse hit regions sit on overlay rows, not on the covered buttons.

## Create modal after the rewrite

Fixed sections, top to bottom:

1. Name label + input (`WithSubmitOnEnter(true)`)
2. Dim slug line when slug ≠ display (zero height when equal or empty)
3. Base Branch label + Combo (pre-filled with `getCurrentBranch`; `textinput` **Value**, not Placeholder; value styled as `TextPrimary` / normal input text)
4. Link Task Combo (compact; overlay only while focused; Backspace clears a committed task)
5. Agent Combo (items from `selectableAgentTypes()`)
6. Auto-approve checkbox + existing flag hint, only when the selected agent supports it
7. Error line (zero height when empty)
8. Create / Cancel

Remove `createPromptFieldID`, `create-agent-list`, in-flow `createBranchDropdownSection`, and the tall `taskPickerSection` usage from this modal. `taskPickerSection` can remain for the standalone “link task to existing worktree” modal until that is migrated; do not block on it.

`createFocus` / `% 8` / prompt-skipping in `normalizeCreateFocus` goes away. Drive focus from modal focus IDs.

`handleCreateKeys` should stop special-casing Enter to advance fields. Combo + `WithSubmitOnEnter(true)` plus `WithPrimaryAction(createSubmitID)` is the submit path. `validateAndCreateWorktree` validates the **slug**, not the raw string via `ValidateBranchName`.

Prefill: `initCreateModalBase` / `loadBranches` set the base input **value** to the current branch as soon as it is known. Do not leave the field empty with a `"main"` placeholder.

Agent-config modal: delete the Prompt section; replace the inline agent `List` with the same Combo; load persisted auto-approve for the selected agent.

## Persistence

`internal/state.State` (global, not per-workdir):

```go
LastCreateAgent     string          `json:"lastCreateAgent,omitempty"`
AgentAutoApprove    map[string]bool `json:"agentAutoApprove,omitempty"`
```

Accessors: `LastCreateAgent()`, `SetLastCreateAgent(AgentType)`, `AgentAutoApprove(agent) bool`, `SetAgentAutoApprove(agent, on bool)`.

`getDefaultCreateAgentType` order:

1. persisted last-create agent, if still in the selectable list
2. `.sidecar-agent` in the current workspace
3. `config.defaultAgentType`
4. Claude

## Prompt removal checklist

Delete:

- `.claude/skills/create-prompt/` (SKILL.md and any references)
- `internal/plugins/workspace/prompts.go`, `prompts_test.go`
- `prompt_picker.go`, `prompt_picker_test.go`, `prompt_picker_modal.go`
- `ViewModePromptPicker` and all switch cases (keys, mouse, commands, view)
- Prompt sections/state on create + agent-config
- `CreateOperationPlan.Prompt`, `CreateDoneMsg.Prompt`
- `StartAgentWithOptions(..., prompt *Prompt)` → drop the argument
- `ExpandPromptTemplate` / ticketMode if nothing else calls them (`template.go`)
- Docs that teach the feature: `.claude/skills/create-prompt`, `docs/guides/deprecated/creating-prompts.md` (already deprecated). Mention in CHANGELOG.

Keep:

- `config.Save` unmanaged-key preservation (the `"prompts"` fixture can stay as “unknown key” coverage)
- `SystemPromptAppendFlags` / shell rename instruction (not this feature)
- Permission-prompt approve/reject (`y` / `N`) — different meaning of “prompt”

Update `create-modal` / `ui-features` skills only if they cite the prompt picker. Do not add a replacement skill.

## Worktree rename

Project workspace (`R`):

- Selected shell → existing rename-shell modal
- Selected worktree → new rename-worktree modal (same shape as rename-shell: current name, input, Rename / Cancel)
- Validate with `shellstate.NormalizeName` (spaces OK)
- Write `display-name`; update in-memory `wt.Name`
- Footer command “Rename” for both

Global Workspaces (`internal/overview`):

- `R` on a shell is already implemented
- `R` on a worktree currently no-ops (`overview/navigate_test.go`); open the same display-name modal
- Persist via `projectdir.WorktreeDir` for the owning project + `display-name`
- Advertise Rename in `Commands()` for worktrees

Do not rewrite `shells.json` when renaming a worktree. Nested shells keep their own names.

## Steel thread

One real path, first:

1. Modal Combo exists and is unit-tested (height stable).
2. `resolveCreateOperation("Auth Refresh", currentBranch)` plans branch `auth-refresh`, display `Auth Refresh`.
3. Create modal: type `Auth Refresh`, Enter → confirm plan shows those two names → create.
4. Sidebar / refresh shows `Auth Refresh`. `git` branch and directory are `auth-refresh`.

Then layer: dropdowns, last-agent, auto-approve map, prompt deletion, rename.

## Implementation slices

### Slice 1 — Floating Combo (`internal/modal` only)

**Files:** `section.go` (overlay on `RenderedSection`), `layout.go` (composite + hit regions), `modal.go` (Esc routing), new `combo.go` + `combo_test.go`.

**Depends on:** nothing.

**Gate:** `go test ./internal/modal/...`

### Slice 2 — Names + global prefs

**Files:** `worktree.go` (slug + display-name save/load), `create_operation.go` + tests, `repo_snapshot.go` or refresh apply-display-name, `internal/state/state.go` + tests.

**Depends on:** nothing (parallel with slice 1).

**Gate:** `go test ./internal/plugins/workspace/ ./internal/state/` — existing create-operation tests still pass for already-legal names; new cases cover `"Auth Refresh"` → branch `auth-refresh`, persist + reload.

### Slice 3 — Modal rewrite + prompt removal

**Files:** `create_modal.go`, `keys.go`, `mouse.go`, `plugin.go`, `update.go`, `commands.go`, `agent_config_modal.go`, `agent.go` (`StartAgentWithOptions` / `buildAgentCommand` signatures), create/agent-config tests, delete prompt files and the skill.

**Depends on:** 1 and 2.

**Gate:** `go test ./internal/plugins/workspace/ ./internal/modal/`; `go build ./...`

### Slice 4 — Worktree rename (project + global)

**Files:** workspace `keys.go` / `view_modals.go` / `commands.go` / tests; `internal/overview/rename.go` / `commands.go` / `navigate_test.go` / `rename_test.go`.

**Depends on:** 2 (display-name file). Serialize after 3 — `keys.go` and `view_modals.go` overlap.

**Gate:** workspace + overview rename tests.

## Verification

- Focused package tests per slice (implementer-owned).
- Once on the integrated branch: `go test ./internal/modal/ ./internal/state/ ./internal/plugins/workspace/ ./internal/overview/` and `go build ./...`.
- Proof: isolated `scripts/tmux-drive.sh` — open create modal, type a spaced name, snap; confirm height does not jump when focusing Base/Task/Agent; Enter creates. Confirm `./scripts/tmux-drive.sh paths` is isolated (`SIDECAR_ISOLATED_STATE=1`, no writes under `~/.local/state/sidecar`).
- Manual contract: mouse click on overlay items, checkbox, buttons; last-agent survives reopen; auto-approve remembered per agent; prompt UI and skill gone.

## Risks

| Risk | Mitigation |
| --- | --- |
| Overlay Y/hit-region math disagrees with `fillBackground` / scroll | Combo tests render a short modal with buttons under the field and click the overlay IDs |
| Esc change breaks every other modal | Only consume Esc when the focused section returns a dismiss-overlay action; default stays cancel |
| `StartAgentWithOptions` signature churn | One compile pass; grep the identifier |
| Display name lost on refresh | Write in `runCreateSetup` (same place as `saveAgentType`); apply in `snapshotToWorktrees` |
| Prompt tests / mouse view-mode tables rot | Delete or rewrite in slice 3; `ViewModePromptPicker` must have zero references |
| Slug collision (`Auth Refresh` vs `auth-refresh`) | Existing “branch already exists” / destination-exists errors |

## Non-goals

- Renaming the git branch or moving the worktree directory
- A sidecar CLI for create/rename (sidecar does not own git or the user’s shell names; `git worktree` and `sidecar shell rename` already exist)
- Migrating every picker in the app onto Combo in this pass
- Wiping `prompts` from existing user config files
