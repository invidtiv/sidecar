# Tasks-aware bundled auto-update plan (td-393e81)

## Outcome

When the effective `tasks_plugin` feature flag is enabled, Sidecar's existing user-confirmed update journey also discovers and updates the installed Tasks command suite. Before the user confirms, the modal names every product that will change, its current and target versions, and the install method Sidecar will use. Afterward it reports the actual outcome of each target and verifies the exact binary versions it changed.

This is an extension of the current `!` diagnostics / `u` update journey, not a background updater. Sidecar must never modify Tasks merely because the plugin is enabled; installation still begins only after the user chooses **Update**.

## Product decisions

1. **Effective enablement is the gate.** Include Tasks only when `features.IsEnabled(features.TasksPlugin.Name)` is true, so config and CLI overrides behave exactly like plugin assembly. Do not infer enablement from a config block, tab position, task-store configuration, or binary presence.
2. **Update the installed Tasks distribution, not the embedded library at runtime.** Sidecar embeds `github.com/marcus/tasks/pkg/tui` at build time; only a new Sidecar binary can replace that copy. The Tasks update target is the separately installed `tasks`, `tasks-tui`, and `tasks-api` suite. The preview should explain this distinction when useful: updating Sidecar refreshes its embedded plugin; updating Tasks refreshes the standalone commands.
3. **Do not silently install Tasks.** If the plugin is enabled but no `tasks` executable is installed, Sidecar updates its own embedded plugin and shows Tasks as `not installed` in diagnostics, with the existing supported install command (`brew install marcus/tap/tasks`). Turning an update confirmation into a new product installation is a separate decision.
4. **Each product owns its install provenance.** Do not reuse Sidecar's detected method for td or Tasks. A Homebrew Sidecar may coexist with a Go-installed td or Tasks development selector. Detect and display a method per target; refuse ambiguous or unmanaged targets with an actionable manual command rather than overwriting them.
5. **One confirmation, truthful per-target outcomes.** Run selected targets in a deterministic order (`sidecar`, `td`, `tasks`), but retain success/failure for every attempted target. A later failure must not erase an earlier successful upgrade. Retry only failed or still-outdated targets.
6. **No automatic relaunch and no tmux lifecycle changes.** A Sidecar binary update still ends with an explicit quit/restart choice. Never stop, restart, kill, or replace the default tmux server.

## Current journey and debt found

The current path spans `internal/version`, `internal/app/model.go`, `internal/app/update.go`, `internal/app/update_modal.go`, and `internal/app/diagnostics_modal.go`:

1. `Model.Init` checks Sidecar and td independently.
2. diagnostics stores those results in two product-specific fields;
3. the preview is split into “Sidecar” and “td-only” branches;
4. `runInstallPhase` captures those fields, runs one best-effort `brew update`, then stops on the first command error;
5. `runVerifyPhase` verifies Sidecar's expected version, but only checks that td executes; and
6. completion reconstructs its text from mutable update-check fields rather than the install result.

Directly relevant debt to address while adding Tasks:

- `UpdateSuccessMsg`, `UpdateInstallDoneMsg`, model fields, rendering, and error strings are fixed to exactly Sidecar + td. Adding another parallel set of booleans would deepen the coupling.
- `updateInstallMethod` is Sidecar's install method but is also used to choose td's updater. That can run the wrong command on mixed installations.
- Homebrew detection asks whether a formula exists, not whether the executable the user resolves belongs to that formula. This can overwrite or falsely verify an active local development selector.
- update checking and GitHub release/cache code is duplicated for Sidecar and td and has no Tasks-shaped seam.
- progress shows generic phases, not which product is being changed, and says `Esc: cancel` even though the running subprocess is not cancellable.
- the first failure aborts the batch without a durable partial-success result; the error modal offers only a Sidecar-specific manual fix.
- td verification does not compare the installed version to the requested one.
- complete-modal content reads state that the success handler has already mutated, so bundled-result copy can become incomplete or misleading.
- subprocess execution is embedded in Bubble Tea model methods, making command, output, mixed-provenance, partial-failure, and retry behavior unnecessarily hard to test without invoking real package managers.

The refactor below is intentionally narrow: three real targets justify one small data model and one command-runner seam, not a general package-management framework.

## Proposed design

### 1. Product-neutral update target and result

Add a focused updater module under `internal/version` (split files if clearer) with plain structs similar to:

```go
type ProductID string // sidecar, td, tasks

type Target struct {
    Product        ProductID
    DisplayName    string
    Installed      bool
    Enabled        bool
    CurrentVersion string
    LatestVersion  string
    HasUpdate      bool
    Install        Installation
}

type Installation struct {
    Method         InstallMethod
    ExecutablePath string
    Managed        bool
    ManualCommand  string
}

type Result struct {
    Target Target
    Status ResultStatus // updated, up-to-date, skipped, failed
    Output string
    Err    error
}
```

Keep target selection as a state-free function over discovered products and effective feature flags. Keep process execution behind one small injected runner (`Run(ctx, name, args...)`) so unit tests can assert exact commands and outcomes. Do not expose this internal updater as a new Sidecar CLI: update policy is presentation behavior over tools Sidecar does not own, and each tool already has its own non-interactive install path.

### 2. Generic release discovery with product descriptors

Replace Sidecar/td-specific check plumbing with one descriptor per product: repository, executable, version arguments/parser, cache key, Homebrew formula, Go module, and whether it is eligible for automatic installation. Preserve the existing cache duration and background startup behavior.

- Sidecar: `marcus/sidecar`, executable `sidecar`, current in-process version.
- td: `marcus/td`, executable `td`, `version --short`.
- Tasks: `marcus/tasks`, executable `tasks`, `--version` (or `version --json` if the pinned release contract supports it reliably).

Only schedule the Tasks network check when the feature is effectively enabled and the standalone executable is installed. A disabled plugin adds no startup process or network work. Preserve the startup rule: checks remain asynchronous `tea.Cmd`s after the first-frame path, never work in plugin `Init` or synchronous model construction.

Use distinct cache files/keys per product so similarly numbered releases cannot collide. A failed check is `unknown`, not “up to date”; diagnostics should render that difference without an intrusive toast.

### 3. Provenance-aware installation plans

Resolve installation per executable immediately before presenting or executing the plan:

- **Homebrew:** the executable's evaluated path belongs to the active formula or to the repository's managed Homebrew selector. Use the fully qualified formula names (`marcus/tap/sidecar`, `marcus/tap/td`, `marcus/tap/tasks`) in commands and tests. Refresh Homebrew metadata once per confirmed batch, then upgrade only Homebrew targets.
- **Go install:** the resolved executable is in the active `GOBIN` / `GOPATH/bin` location. Use the product's module and exact version.
- **Binary/unmanaged/development selector:** do not guess. Mark it manual and show the command or repository-specific activation guidance. In particular, do not replace an intentionally active local Tasks or Sidecar development build.

This replaces the current global `DetectInstallMethod` assumption. Keep installation detection and command construction pure where possible, with file resolution and process calls at the adapter edge.

For Tasks Homebrew, one `brew upgrade marcus/tap/tasks` updates all three commands. Verification must check `tasks`, `tasks-tui`, and `tasks-api` resolve to the same released version, because that is the distribution contract. The Go path must likewise install all three commands or be declared manual; installing only `cmd/tasks` would leave the suite split.

### 4. Explicit batch state machine

Replace the three generic phase booleans with one immutable confirmed plan and a result per target. Bubble Tea messages should carry the target ID plus a full result; model mutation remains in `Update`.

Suggested states:

```
checking -> preview -> refreshing-package-metadata -> installing target N
         -> verifying target N -> complete | complete-with-errors
```

Run sequentially to avoid concurrent Homebrew locks and confusing PATH changes. Continue after a target failure when it is safe to do so, recording the failure. Before each command, revalidate that the target still resolves to the provenance the user confirmed. Verify exact normalized versions afterward; never infer success from exit status or Homebrew wording alone.

Use a real `context.CancelFunc` only if subprocess cancellation is implemented with `exec.CommandContext` and tested. Otherwise remove the false `Esc: cancel` hint and say `Update in progress`; closing the modal must not pretend to stop a package manager.

### 5. Preview, progress, completion, and recovery UX

Refactor the modal to project the confirmed target/result list:

- **Diagnostics:** list Sidecar, td, and (only when enabled) Tasks with current version, availability, and provenance. If enabled but standalone Tasks is absent, show `embedded only · standalone not installed` plus the install hint.
- **Preview:** title it `Update available` and show rows such as `Sidecar 0.90.0 -> 0.91.0 · Homebrew`, not only Sidecar release notes. State clearly that the single confirmation updates the listed products. Keep Sidecar release notes/changelog available without implying they describe td or Tasks releases.
- **Progress:** show the current product and action, plus settled rows for prior targets. Avoid a spinner-only three-phase display that hides where time is going.
- **Completion:** render from the immutable results, distinguishing `updated`, `already current`, `skipped/manual`, and `failed`. Say restart is required only when Sidecar itself changed; standalone td/Tasks-only success does not require quitting Sidecar.
- **Recovery:** give a manual command specific to every failed target. Retry failed/outdated targets only, without re-running successful upgrades.
- **Toasts:** summarize (`3 updates available`, `2 updated; Tasks failed`) and leave detail in the modal. Avoid sequential toasts overwriting one another as async checks arrive.

Keyboard and mouse actions must remain equivalent. Modal output must stay height-constrained and must not add a plugin-owned footer.

## Implementation slices

### Slice A — characterize and extract the existing two-product journey

1. Add regression tests for current Sidecar + td selection, mixed installation provenance, no-op Homebrew output, exact td verification, partial failure, completion rendering, and retry selection.
2. Introduce `Target`, `Installation`, `Result`, descriptor-based release checks, and the injected command runner under `internal/version`.
3. Move Sidecar and td onto the new structures without adding Tasks yet.
4. Replace the model's parallel product fields and generic phase map with a confirmed plan and per-target results.
5. Preserve current visible behavior where it is accurate; fix the false cancel, mixed-provenance, partial-result, and completion-copy defects as part of this slice.

Acceptance proof: the existing two-product flow passes focused unit tests, an isolated Homebrew/Go command-runner integration harness, and a real Sidecar preview/progress/completion capture without touching live state.

### Slice B — Tasks discovery, selection, install, and verification

1. Add the Tasks descriptor and version parser against the pinned/released Tasks CLI contract.
2. Gate discovery and target selection on effective `tasks_plugin` enablement.
3. Add provenance-aware Homebrew and Go-suite installation plans. If a safe three-binary Go plan cannot be expressed against the released module, mark it manual rather than partially updating Tasks.
4. Verify exact versions and resolution for `tasks`, `tasks-tui`, and `tasks-api` after an automated update.
5. Cover disabled, CLI-disabled override, enabled-but-not-installed, enabled-current, enabled-outdated, mixed method, unmanaged dev selector, and failing-one-of-three verification cases.

Acceptance proof: a fake package-manager harness shows that disabled Tasks is never checked or executed, enabled Homebrew Tasks produces one suite upgrade, and no partial suite is reported as success.

### Slice C — multi-product UX and real-app proof

1. Convert diagnostics, preview, progress, completion, error, retry, mouse, and toast rendering to the target/result list.
2. Add narrow-terminal and height-constraint tests for zero, one, two, and three visible products.
3. Add state-machine tests proving stale async check/results cannot mutate a new plan or a later retry.
4. Update `PRIVACY.md` and user documentation to name Tasks and the exact user-initiated network/process behavior.
5. Run the isolated real-app journeys below and retain text/PNG evidence.

Acceptance proof: the user can see in advance that Tasks will update, observe which product is active, and distinguish complete, partial, and manual outcomes using keyboard or mouse.

## Verification matrix

### Focused automated checks

- `go test ./internal/version ./internal/app ./internal/features ./internal/plugins/assembly`
- race-test updater/app packages with fake runners and isolated cache/config directories;
- command-construction tables for every product x supported method;
- version parser tables including `v` prefixes, development metadata, malformed output, and exact mismatch;
- target selection for feature config, environment/CLI override, installed and absent binaries;
- sequential execution, one Homebrew refresh, partial failure continuation, immutable completion results, and retry-only-failures;
- modal keyboard/mouse parity and constrained rendering at small dimensions.

### Integrated repository gates

- `GOWORK=off go test ./...`
- `GOWORK=off go test -race ./...`
- `GOWORK=off go vet ./...`
- `gofmt` check, `git diff --check`, and `go build ./...`;
- if the pinned Tasks API changes, first prove the released Tasks package from an external consumer, then update `go.mod`/`go.sum` deliberately.

### Real consumer proof

Use `./scripts/tmux-drive.sh paths` first and confirm both tmux and Sidecar state are isolated. Never use the default tmux server or real Sidecar config/state. Drive and capture these journeys with a test config and fake `brew`/`go`/product binaries ahead of real PATH entries:

1. Tasks disabled: diagnostics and preview omit Tasks; no Tasks check/command is logged.
2. Tasks enabled + all three products outdated: preview names all products and methods; progress advances product by product; completion reports all exact versions and requests restart because Sidecar changed.
3. Tasks enabled + Tasks current: it appears in diagnostics but is absent from the confirmed change list.
4. Tasks enabled + standalone absent: no silent install; embedded/standalone explanation and install hint are visible.
5. Mixed provenance: each target uses its own command; an unmanaged target is manual and is not overwritten.
6. Tasks fails after Sidecar/td succeed: completion-with-errors retains both successes, offers the Tasks-specific manual command, and retry selects Tasks only.
7. td/Tasks-only update: completion does not claim Sidecar needs a restart.

For final release confidence, use a disposable Homebrew prefix or equivalent fixture to prove formula ownership and three-binary Tasks verification. Do not mutate the machine's active development selectors merely to test the updater.

## Documentation and release notes

- Update `PRIVACY.md` self-update disclosure from Sidecar-only wording to the confirmed product list and package-manager commands.
- Update website diagnostics/update documentation with Tasks gating, per-product provenance, no-silent-install behavior, and recovery commands.
- Add a changelog entry that describes the user journey, including the related mixed-install and truthful partial-result fixes.
- If implementation requires a newer Tasks embedding API, release Tasks first, verify the public module from an external consumer, then pin that release in Sidecar before releasing Sidecar.

## Review and completion gate

Implementation is not complete on green tests alone. Require an independent review focused on:

- feature-gate truth (including overrides);
- package-manager command safety and executable provenance;
- no accidental Tasks installation or partial three-binary success;
- state-machine ordering, stale-message rejection, cancellation claims, partial results, and retry idempotence;
- exact-version verification rather than command exit status;
- keyboard/mouse and narrow-terminal UX;
- startup latency and absence of disabled-feature work; and
- isolated proof that neither the default tmux server nor live Sidecar/Tasks state was touched.

Close `td-393e81` only after findings are remediated, integrated gates pass, and the isolated real-app evidence demonstrates the complete user journey.
