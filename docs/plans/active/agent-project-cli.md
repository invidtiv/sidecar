# Agent-facing project CLI

**Status:** proposed; decisions settled **Scope:** `internal/config` (already owns the mutations), a new `sidecar project` command group, a `uirequest` action that switches the running TUI, `sidecar agents` / CLI help / `docs/reference/cli.md`. **Created:** 2026-08-31

One sentence: **an agent should be able to see which Sidecar project it is in, list the configured projects, add or edit one, and put the user there immediately — without pretending a live shell can change projects.**

## Outcome and agent journey

A user tells an agent "add a new project called vacuum simulator." The agent works with the user to pick a path (`~/code/vacuum-simulator`), creates the directory with ordinary tools if it does not exist yet, then:

```bash
sidecar project add "vacuum-simulator" --path ~/code/vacuum-simulator --switch
```

Sidecar writes the project into `config.json`, the running TUI switches to it the same way `@` would, and the user is looking at that project's workspace. Adding a project does not initialize a Git repository or a td project; the agent offers those to the user after the add, and only does them if asked. The conversation that just did this is still in the original project's shell — shells do not move — so the agent creates a landing shell there only if the user wants one:

```bash
sidecar create shell --project vacuum-simulator --name "vacuum simulator"
```

`create shell --project` already exists. What does not exist is any way for the agent to read, add, edit, or switch projects.

## Why this belongs in Sidecar's CLI

Sidecar owns the project list. It lives in `~/.config/sidecar/config.json` under `projects.list`, with uniqueness, path, and name rules that Configuration and the `@` switcher already share. Uninstall Sidecar and that list, those names, the per-project theme and Open-in overrides, and the TUI's idea of "which project is on screen" all vanish. An agent that `mkdir`s a directory and `git init`s it has a repo, not a Sidecar project. That is the same ownership test that carried `sidecar create shell` and `sidecar open`.

This is not a second settings UI. Configuration remains the human surface. The CLI is the agent surface over the same `config.AddProject` / `UpdateProject` / `RemoveProject` / `ValidateProject` boundary those screens already use.

## Two meanings of "current"

This is the design problem the rest of the plan hangs on. They are not the same project.

| Sense | What it is | How you know today |
| --- | --- | --- |
| **This shell's project** | The project whose `shells.json` registered the calling tmux session. Fixed for the life of the shell. | Inferred from `SIDECAR_SHELL` + `shellstate.LookupOrigin`. There is no `SIDECAR_PROJECT`. |
| **The visible project** | The project the running Sidecar TUI is showing. Changes when a human hits `@`, or when a Sessions row is activated. | `uirequest.Instance.{ProjectKey,Project,WorkDir}`, rewritten on every `switchProject`. |

An agent that only knows cwd will often be right, and will be wrong the moment the user has switched away, or the moment the agent itself just switched the TUI. After `--switch`, those two senses diverge on purpose: the conversation stays, the user's eyes move.

`sidecar project current --json` should report both, and whether they match. Human output names the shell's project first (that is where the command is running) and mentions the visible one when it differs.

```text
sidecar  ~/code/sidecar
visible: vacuum-simulator  ~/code/vacuum-simulator
```

```json
{
  "shell":    {"name": "sidecar", "path": "/Users/marcus/code/sidecar", "key": "sidecar"},
  "visible":  {"name": "vacuum-simulator", "path": "/Users/marcus/code/vacuum-simulator", "key": "vacuum-simulator"},
  "aligned":  false
}
```

Do not invent a `SIDECAR_PROJECT` environment variable in the first slice. `sidecar shell name` already teaches that the env var is a cue and the command is authoritative; the same shape applies here, and older shells would not have the variable anyway.

## What already exists

Almost all of the domain work is done. The gap is a command group and one IPC action.

| Seam | Current behavior | Role in this plan |
| --- | --- | --- |
| `internal/config/projects.go` | State-free `ValidateProject` / `ValidateProjectName`; `AddProject` / `UpdateProject` / `RemoveProject` / `MoveProject` go Load→mutate→Save so a CLI write cannot clobber a concurrent Configuration save | **Reuse unchanged.** CLI is another caller, not a second writer. |
| `ProjectConfig` | `Name`, `Path`, `Theme`, `OpenIn`, `LastOpenInApp`, `WorktreeSetup` | Name and path are required. Theme and Open-in are optional first-slice fields. Worktree setup stays out (Configuration also deferred it). |
| Configuration → Projects | Add / edit / remove / reorder. Save does **not** switch the TUI; it selects the row on the Projects page. Path must exist and be a directory. Git is explained, not required; cwd can be `git init`'d from the form. | Human parity target for validation and fields. Switching is a new behavior the CLI adds, not something the form already does. |
| `@` project switcher | `Model.switchProject*` re-inits every plugin, restores the last worktree, announces the new instance, flashes "Switched to …" | The TUI half of `project switch`. Call this; do not reimplement it. |
| `uirequest.ActionConfigReload` | Used by `sidecar notify config set`. Reloads settings in every live instance. Does **not** change `WorkDir`. | Broadcast after every mutating `project` verb so `@` and Configuration see the new list. Not sufficient for a switch. |
| `uirequest.Instance` | One presence file per TUI process, with current `ProjectKey` / `Project` / `WorkDir` | How `project current` and `project list` learn the visible project. How `project switch` finds the process to talk to. |
| `--project NAME` on `open`, `create`, `shell`, `agent`, `layout` | Resolves slug, path, or basename against the configured list, including projects that have never been opened (`td-677dde`) | Same matcher for `project switch` / `set` / `remove` / `create shell --project`. Do not grow a second one. |
| `sidecar create shell --project NAME` | Creates a managed shell in that project, headless-capable, with an optional UI ack | The "drop a shell there" half of the journey, already shipped. |
| `activateTargetInOtherProject` | A `sidecar open` (or link) qualified with another project parks the jump, switches, then lands | Proof that TUI project switch from IPC is already a real path. A dedicated switch action is still cleaner than opening a dummy target. |
| Shell env | `SIDECAR_SHELL`, `SIDECAR_SHELL_NAME`, `SIDECAR_MANAGED_SHELL`, … | No project identity is published today. |

There is no `sidecar project` command. `sidecar agents` does not mention projects except as a `--project` flag on other verbs. An agent that wants to add a project today has to edit `config.json` by hand, which bypasses validation and does not reload or switch the TUI.

## Proposed command group

`sidecar project`, singular, matching `shell` / `host` / `session`. Every verb takes `--json`. Mutations are `Mutates: true` so isolated proof runs cannot touch the real config. Each verb that agents should discover declares `AgentDoc`.

### `sidecar project current`

The analog of `sidecar shell name`. No flags besides `--json`. Resolves the calling shell's project from origin; reads the visible project from the unique live instance when there is one. Exit 0 even when no TUI is running (`visible` is omitted). Exit 1 if this is not a managed shell and cwd is not a configured project.

### `sidecar project list`

Every entry in `projects.list`, in list order. Marks `shell` (this command's project) and `visible` (the TUI's). Does not require a managed shell: an agent outside Sidecar can still inspect the configured list. Human columns: name, path, markers. JSON is the array plus the same `shell` / `visible` objects `current` uses, so an agent that lists does not also have to call `current`.

### `sidecar project add NAME --path PATH [--theme NAME] [--open-in APP] [--switch]`

Validates through `config.ValidateProject`, writes through `config.AddProject`, broadcasts `ActionConfigReload`. `--path` is required, expanded with `~`, and must already be a directory — the agent `mkdir`s, the CLI does not. Git and td are not initialized; the add command's help and `AgentDoc` say so in one sentence so the agent infers it should offer both to the user after the project exists.

`--switch` is the steel-thread flag: after a successful write, perform `project switch` against the new name. Add never switches unless `--switch` is passed. If no TUI is running, add still succeeds and JSON reports `"switched": false` the same way `create shell` reports `"acked": false`. A landing shell is a separate `create shell --project` the agent runs only when the user asks for one.

### `sidecar project switch NAME`

The new IPC. Writes a `uirequest` action (working name `ActionSwitchProject`) naming the configured project. The running instance validates it still exists, calls the existing `switchProject*` path (including last-worktree restore), announces the new instance, flashes the same "Switched to …" a human `@` produces, and acks.

Targeting uses the unique live instance. No current-shell origin is required — the point is to change what the *user* is looking at, which may already not be this shell's project. Ambiguous multiple instances refuse with the same message `open` uses. No instance: exit 3, so the agent can say "the project is configured; open Sidecar to see it" rather than claiming the user is looking at it.

This is a deliberate focus change. `sidecar open` never moves selection; `project switch` exists to move it. Say that in the help so the two policies cannot be read as a contradiction.

### `sidecar project set NAME [--name NEW] [--path PATH] [--theme NAME] [--open-in APP] [--clear-theme]`

Edit. At least one flag required. Path changes re-validate uniqueness and directory existence. Theme / Open-in match the Configuration form. Broadcasts `ActionConfigReload`. Does not switch, even if NAME is the visible project — a rename or theme change is not a request to jump.

### `sidecar project remove NAME [--yes]`

`--yes` is required (non-interactive destructive). Does not delete the directory, the git repo, or `state.json` workdir keys — same as Configuration. If NAME is the visible project, refuse with a message naming `project switch` first. Do not switch as a side effect of remove.

### Discoverability

- Every verb in `sidecar agents`.
- `RenderAgents` intro grows one clause: project current/list/add/switch act on Sidecar's configured projects, not on the filesystem.
- The `add` command's `Long` and `AgentDoc` include this sentence, and nothing more on the subject: "Adding a project does not initialize a Git repository or a td project." That is enough for the agent to offer both to the user; do not teach `git init` or `td` usage here.
- Regenerated `docs/reference/cli.md` via `REGEN_CLI_DOC=1`.
- No AGENTS.md dump of the full command tree — `sidecar agents` stays the canonical pointer.

## The session constraint, stated as a rule

A Sidecar-managed shell is born in a project. Its tmux session, `shells.json` record, workDir, and env are that project's. There is no "move this shell to another project" operation, and this plan does not add one. Consequences:

1. `project switch` changes the TUI, not the calling process. After it, `project current` reports `aligned: false`.
2. Work in the new project happens in a new shell (`create shell --project`), a new worktree, or a human-created workspace row — the same as if the user had hit `@` themselves.
3. The agent that added the project should not claim it is now *in* that project. It should say the project exists, the user is looking at it (if the switch acked), and create a shell there only if the user asks.
4. `--split` create stays in the calling shell's pane tree. A landing shell in the new project is a workspace-shell create, not a split of the conversation the user just left.

"Drop them there" therefore means: switch the visible TUI when `--switch` is passed, then create a shell in the new project only if the user asks. It does not mean teleport this conversation.

## Steel thread

The smallest journey that is actually useful:

1. `sidecar project list --json` shows the configured list and the visible project.
2. `sidecar project add vacuum-simulator --path ~/code/vacuum-simulator --switch` writes the entry and the running TUI switches to it.
3. The user can see the new project in the header and the empty Workspaces list.
4. If the user wants a shell there, `sidecar create shell --project vacuum-simulator --name "vacuum simulator"` puts one there, using the existing create path. Add does not create it.
5. `sidecar project current --json` from the original conversation still names the original shell project and reports the new one as `visible`.

That is one new command group, one new `uirequest` action, and no new persistence. List and current without a TUI still work, because they read config and origin.

## Work sequence

### M0 — Read: `list` and `current`

No IPC. Load `projects.list`, resolve the calling origin the way `shell name` does, overlay instance presence for `visible`. Unit tables for matching, no-TUI, unmanaged-shell-in-a-configured-repo, and the aligned/unaligned JSON shape.

This is what makes the rest inspectable. Ship it first so agents can see the list the day add lands.

### M1 — Write: `add`, `set`, `remove`

Thin CLI over `config.*`. Broadcast `ActionConfigReload` after every successful mutation, same helper `notify config set` already uses. Validation messages stay the plain-language strings `ValidateProject` already returns (`"Path does not exist"`, `"Project name already exists"`), mapped to exit 5. Proof: isolated config file, add then list, duplicate name refused, remove requires `--yes`.

At the end of M1 the project exists and `@` would show it, but the user is not looking at it unless they switch by hand.

### M2 — Switch: `project switch` and `add --switch`

New `uirequest.Action` whose payload is the project name/path. Host handler: resolve via the same matcher, refuse unknown, call `switchProjectWithSelection`, ack. CLI polls acks with the `open`/`create` wait. `add --switch` is add-then-switch, one process, one JSON object carrying both outcomes.

**Proof:** isolated `tmux-drive.sh` run. Sidecar showing project A. Agent shell in A runs `sidecar project add B --path … --switch`. Snapshot of the header and Workspaces list shows B. A second proof with no Sidecar running: add exits 0, JSON `"switched": false`.

### M3 — Discoverability and the documented journey

`AgentDoc` lines, regenerated CLI reference, `sidecar agents` intro, a short example in the command's `Long` that names the session constraint out loud. The `add` help includes the one-sentence Git/td note so the agent offers those after creating the project. The landing-shell step stays a separate `create shell --project` example, not part of add.

## Deliberately out of scope

- Moving a live shell from one project to another.
- Creating directories, Git repositories, or td projects. The path must already exist; `mkdir`, `git init`, and td setup are the agent's, offered to the user after add. Sidecar validates the path exists and is a directory.
- Per-project worktree-setup overrides. Configuration deferred them; the CLI does too.
- Reorder. Humans drag/keyboard-move on the Projects page; agents have no current reason to care about list order beyond round-tripping it.
- Publishing `SIDECAR_PROJECT`. Revisit if `current` is not enough of a cue.
- A `sidecar://project/…` URL or a project target for `sidecar open`. Switch is its own verb.
- Changing what Configuration's Save does. The form still returns to the Projects page; only the CLI switch verb (and `add --switch`) jumps the TUI. If we later want Save to offer "Switch to this project," that is a human-surface follow-up, not a prerequisite.
- Remote hosts. A remote `project add` would write that host's config; it is a later composition with `sidecar host`, not part of the steel thread.

## Settled decisions

1. **One command group, `sidecar project`.** Not a flag on `create`, not a `sidecar config` grab-bag.
2. **Same validation and persistence as Configuration.** No parallel schema.
3. **Same `--project` matcher as the rest of the CLI.**
4. **Switch is explicit (`--switch` / `project switch`).** Add never switches unless `--switch` is passed. Registering a project is not permission to yank the user's view.
5. **Switch changes the TUI, not the calling shell.** JSON always reports both.
6. **A landing shell is `create shell --project`, only when the user asks.** Add does not create a shell. Agents compose; add stays one job.
7. **Path must already exist.** Add does not `mkdir`, `git init`, or initialize a td project. Help and `AgentDoc` say: "Adding a project does not initialize a Git repository or a td project." The agent offers both after the project exists.
8. **Removing the visible project is refused.** The message names `project switch` first. Remove never switches as a side effect.
9. **Works headless; better when Sidecar is running.** Same ack contract as `create shell`.

## Unresolved questions

1. **Where does a switch land inside the new project?** `@` restores the last worktree and the last active plugin. A brand-new project has neither, so it lands on the Workspaces plugin at the project root. That is the right empty state. Confirm we should not also force-focus a particular row or open Configuration.

2. **Multiple live TUI instances.** Rare (one process that switches projects is the normal shape). Recommendation: unique instance or refuse, identical to `open`. Do not fan a switch out to every instance.

## Acceptance evidence

- `sidecar project list --json` / `current --json` contract tests covering aligned, unaligned, no TUI, and unmanaged cwd.
- Add / set / remove go through `config.ValidateProject` and survive a concurrent Configuration save (reload-first).
- Duplicate name, missing path, unknown project: exit 5, plain-language message, no write.
- Isolated `tmux-drive.sh` proof of the steel thread (header and Workspaces show the new project after `--switch`). Add without `--switch` leaves the visible project unchanged.
- Isolated proof that add without a TUI still writes and reports `switched: false`.
- Remove of the visible project is refused; remove of another project succeeds with `--yes`.
- `sidecar agents` lists the verbs; `add`'s one-line Git/td note is in help and the generated CLI reference.
- A `create shell --project` after add still works without a TUI, as it does today.

## Relationship to other plans

- [agent-shell-create-cli.md](../implemented/agent-shell-create-cli.md) — template for registry + `uirequest` + ack, and the landing-shell verb this plan composes with.
- [agent-open-in-split-cli.md](../implemented/agent-open-in-split-cli.md) — the request bus and the "don't steal focus" policy this plan explicitly carves an exception to.
- [sidecar-configuration-design.md](../implemented/sidecar-configuration-design.md) / [sidecar-configuration-decisions.md](../implemented/sidecar-configuration-decisions.md) — human Projects surface, field list, and the Load→mutate→Save rule. CLI does not replace that surface and does not take worktree-setup ahead of it.
