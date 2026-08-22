# Sidecar Configuration — implementation seam map

Companion to `sidecar-configuration-design.md`. Produced by repo recon on 2026-08-15; verify line numbers before relying on them, but symbol names were confirmed.

---

## 1. App shell: header, content, footer, plugin views, scope takeover

**Frame composition** — `internal/app/view.go`
- `Model.View()` (view.go:52) wraps `viewContent()` into a `tea.View` (AltScreen, ReportFocus, MouseMode).
- `Model.viewContent()` (view.go:140) is the whole frame: header (`renderHeader()`), a blank spacer row, `renderContent(m.width, contentHeight)`, `renderFooter()`, then a `switch m.activeModal()` overlay chain (view.go:177-205).
- Layout constants: `headerHeight`, `footerHeight`, `minWidth`, `minHeight` (`internal/app/model.go` const block ~line 140). `contentHeight = m.height - headerHeight - footerHeight` (view.go:154).
- `renderContent` (view.go:946) is the single fork: `if m.inGlobalScope() { return m.renderGlobalContent(...) }` else `m.ActivePlugin().View(width, height)` clamped with `Width/Height/MaxHeight`.

**How a view takes over the central content area — copy the `ScopeGlobal` pattern.** `internal/app/scope.go`:
- `AppScope` (`ScopeProject`/`ScopeGlobal`), `GlobalTab`, `tabRef` (scope.go:18-116).
- `Model.inGlobalScope()` (scope.go:119) gates `renderContent`, `pluginCursor` (view.go:98), mouse routing (`globalMouse`, scope.go:434), key routing (`globalOverlayOwnsKeys`, scope.go:522), footer hints (view.go:1100), and `updateContext` (update.go:1645).
- `globalOverlayOwnsKeys()` (scope.go:522) is exactly the "a Sidecar-drawn view covers the plugin pane, so keys must not reach the hidden plugin" guard Configuration needs. `globalSurfaceWantsEsc()` (scope.go:472) is the model for "Esc first clears search, then closes".
- `enterOverview()` / `exitOverview()` / `leaveOverview(restoreProject bool)` (model.go:745-786) show how to enter and restore the previous surface and focus. Configuration's Escape-restores-prior-surface should mirror `leaveOverview`.
- `globalTasksHost` (scope.go:354-411) is the pattern for a host-owned surface that survives project switches (not in the plugin registry).

**Plugin interface** — `internal/plugin/plugin.go`: `Plugin` = `ID/Name/Icon/Init/Start/Stop/Update/View(width,height)/IsFocused/SetFocused/Commands/FocusContext`. Optional capabilities: `TextInputConsumer.ConsumesTextInput()` (line 24), `GlobalKeyBlocker.BlocksGlobalKeys()` (44), `KeyRouter.ClaimsKey/QuitKeyExits` (61), `FooterStatusProvider` (80), `CursorProvider`, `MouseModeProvider`, `WheelBoundaryConsumer`, `DiagnosticProvider` (137). Registration order/gating: `internal/plugins/assembly/assembly.go` — `Plan(cfg)` (line 66) and `Register(reg, cfg, logger)` (line 110). Registry: `internal/plugin/registry.go` (`Reinit` on project switch).

**Focus/switching**
- `FocusPluginByIDMsg{PluginID}` — `internal/app/commands.go:79`; helper `FocusPlugin(id) tea.Cmd` (commands.go:124); handled at `internal/app/update.go:432` → `Model.FocusPluginByID` (model.go:561). Note update.go:432 also exits global scope.
- `SetActivePlugin(idx)` (model.go:520), `NextPlugin`/`PrevPlugin` (539/548), `activateTab(tabRef)` (scope.go:207), `selectTabByNumber` (scope.go:337), `cycleTabs` (scope.go:317).

**Header rendering + right-pinned project selector + hit regions**
- `renderHeader()` (view.go:666) paints one physical row from `headerGeometry()` (view.go:702), the *sole* source of geometry and hit bounds. `headerLayout` struct (view.go:687) carries `left/right/rightStart/logoEnd/globalTabs/projectTabs/selectorStart/selectorEnd/restoreStart/restoreEnd`.
- Selector is pinned to the terminal's right edge: `renderSelector` closure (view.go:727), `selectorBudget := width - lipgloss.Width(left)` (view.go:793). Narrow-terminal rule: whole tabs are dropped from the right until the selector fits (view.go:766-779 global tabs, 840-853 project tabs). **A gear must be inserted into this budget math**, not appended after `right`.
- Bounds accessors: `getTabBounds()` (view.go:895), `getLogoBounds()` (910), `getProjectSelectorBounds()` (927), `getProjectRestoreBounds()` (937).
- Header click dispatch: `internal/app/update.go:285-317` — `if isClickPress && mi.Button == tea.MouseLeft && mi.Y < headerHeight` then logo → restore → selector → tab bounds, in that order. **This is where a gear hit region goes** (add `getGearBounds()` + a branch here).
- Content mouse events are offset by `offsetMouseY(msg, -headerHeight)` (update.go:50, used at 318/325).

**Mouse library** — `internal/mouse/mouse.go`: `Rect`, `Region`, `HitMap` (`Add`, `AddRect`, `Test`, `Clear`), `Handler` (`NewHandler`, `HandleClick`, `HandleMouse` → `MouseAction`, `StartDrag/DragDelta/EndDrag`), `ActionType`, `WheelScrollLines`. Every app modal owns a `*mouse.Handler` field (model.go:164-327); `findMouseRegion(h, id)` at update.go:2513.

---

## 2. Keymap / shortcut system

**Registry** — `internal/keymap/registry.go`
- `Command{ID,Name,Handler,Context}`, `Binding{Key,Command,Context}`, `Registry` with `commands`, `bindings map[context][]Binding`, `userOverrides`.
- API: `RegisterCommand`, `RegisterBinding` (idempotent, line 62), `RegisterPluginBinding(key,command,context)` (75), `SetUserOverride`, `UserOverride(keyMsg)`, `Handle(key, activeContext)` (112), `BindingsForContext(ctx)` (221), `CommandForContextKey(ctx,key)` (231), `AllContexts()` (243), multi-key sequences with 500ms `sequenceTimeout`.
- Precedence inside `findCommand` (143): **user override → active context → global**.

**Defaults / where to register a new context** — `internal/keymap/bindings.go` (`DefaultBindings()`), registered by `keymap.RegisterDefaults(km)` in `cmd/sidecar/main.go:186`. Contexts are string literals (~55 exist). **Adding `config`, `config-edit`, `config-confirm` = append `Binding{...}` entries in `DefaultBindings()`**. `internal/keymap/hostkeys.go` defines `HostReservedKeys`.

**Key precedence ladder** — `internal/app/update.go:673` `handleKeyMsg`:
0. Esc / modal switch (675-768).
1. Quit confirm (770), update modal (785), global Workspaces own keys (794).
2. Interactive/inline-edit contexts forward *all* keys (809-813: `workspace-interactive`, `file-browser-inline-edit`, `notes-inline-edit`).
3. `consumesTextInput() || pluginBlocksGlobalKeys()` → forward everything but ctrl+c (819-830). **This is the seam that protects typed input; `config-edit` should register in `isTextInputContext`.**
4. `pluginClaimsKey(key)` contextual binding (836).
5. app global switch, then `m.keymap.Handle(msg, m.activeContext)` (1621), then `forwardKeyToPlugin` (1632).

**Context resolution** — `Model.updateContext()` (update.go:1640): modal context (`modalFocusContext`, model.go:94) → global-scope tab context (`GlobalTab.context()`, scope.go:81) → active plugin `FocusContext()` → `"global"`. Helper classifiers to extend: `isRootContext` (update.go:1791), `isTextInputContext` (1815), `isGlobalRefreshContext` (1832), `Model.textInputFocused()` (1777).

**Footer hints derived from bindings** — `internal/app/view.go`: `renderFooter()` (1001), `footerHints()` (1100), `globalFooterHints()` (1140), `pluginFooterHints(p, ctx)` (1209), `commandFooterHints(commands, ctx)` (1216), `typingFooterHints()` (1082), `firstReachableKey`/`survivesTextInput` (1192/1205), `renderHintLineTruncated` (1271).

**Command palette** — `internal/palette/entries.go`: `BuildEntries(km, plugins, activeContext, pluginContext)` (57), `Layer`, `PaletteEntry`, `FilterEntriesForContext`, `GroupEntriesByCommand`; fuzzy in `internal/palette/fuzzy.go`. Opened at update.go:1526. Help modal: `ensureHelpModal`/`renderBindingSection`/`contextShadowsGlobalKey` (view.go:1297-1440).

---

## 3. Config model and save paths

**Model** — `internal/config/config.go` `Config{Projects ProjectsConfig; Plugins PluginsConfig; Keymap KeymapConfig; UI UIConfig; Features FeaturesConfig}` (line 14). `Default()` at 228, `Validate()` at 283 (clamps refresh intervals, clamps `TmuxCaptureMaxBytes<=0` → 2MB, coerces Tasks position). **`Validate` is the existing clamp seam for the Advanced capture-limit bounds.**

**Read** — `internal/config/loader.go`: `Load()` (163) / `LoadFrom(path)` (169), `mergeConfig` (222) merging `raw*` pointer-typed shadow structs (absent keys keep defaults), `applyEnvOverrides` (400), `ExpandPath` (451), `ConfigPath()` (463, `~/.config/sidecar/config.json`; `SetConfigPath`), `StateDir()` (480). Isolation guards in `internal/config/isolation.go`.

**Write (the save boundary to reuse)** — `internal/config/saver.go`
- `Save(cfg *Config) error` (129): reads existing JSON into `map[string]json.RawMessage`, re-marshals only `projects`/`plugins`/`keymap`/`ui` (+`features` when non-empty) via `toSaveConfig` (77), so **unmanaged keys are preserved**. Writes 0644.
- Targeted helpers: `SaveTheme` (176), `SaveThemeWithOverrides` (188), `SaveCommunityTheme` (201), `SaveProjectTheme(path, *ThemeConfig)` (213), `SaveGlobalTheme(ThemeConfig)` (228), `SaveLastOpenInApp(projectPath, appID)` (240). All `Load()`-then-`Save()` (read-modify-write against disk); `saveProjectAdd` (model.go:1255) does the same manually.
- **Gap:** `savePluginsConfig` (saver.go:26) has **no `Notes` field**, and `rawPluginsConfig` (loader.go:91) has no `notes` — `plugins.notes` is dead config today. Notes enablement is `features.flags["notes_plugin"]`.

### Setting inventory for the mockup pages

| Mockup control | JSON key | Go field | Runtime apply? |
|---|---|---|---|
| Theme (global) | `ui.theme{name,community,overrides}` | `UIConfig.Theme` (`ThemeConfig`, config.go:221) | **Live.** `theme.ApplyResolved` + `styles.ApplyTheme*` |
| Theme (per project) | `projects.list[].theme` | `ProjectConfig.Theme *ThemeConfig` | Live via `theme.ResolveTheme(cfg, workDir)` |
| Nerd Font icons | `ui.nerdFontsEnabled` | `UIConfig.NerdFontsEnabled` | Startup only — `styles.PillTabsEnabled` set once at `cmd/sidecar/main.go:184`; assigning at runtime works, nothing does it today |
| Header clock | `ui.showClock` | `UIConfig.ShowClock` → `Model.showClock` (model.go:169/375) | **No renderer exists** (view_test.go:300-322 asserts absence) |
| Terminal title | `ui.terminalTitle` (default `{project}{worktree}`) | `UIConfig.TerminalTitle` → `Model.titleTemplate` | Re-rendered each tick (`internal/app/title.go`, `titleResyncTicks=10`; `internal/termtitle`); field read at construction → needs model update on change |
| Last "Open in" app | `ui.lastOpenInApp`, `projects.list[].lastOpenInApp` | `UIConfig.LastOpenInApp`, `ProjectConfig.LastOpenInApp` | Live (`internal/app/open_in_modal.go:102-397`). *Last used*, not a preference |
| Project list | `projects.list[]` | `ProjectConfig{Name,Path}` | Live from `m.cfg.Projects.List`; switch does `registry.Reinit` |
| Per-project worktree setup | `projects.list[].worktreeSetup` | `ProjectConfig.WorktreeSetup`, `Config.WorktreeSetupForProject(path)` (config.go:45) | Read at worktree creation |
| Default agent | `plugins.workspace.defaultAgentType` (legacy `defaultAgent`) | `WorkspacePluginConfig.DefaultAgentType` | Read at create time |
| Enabled agent families | `plugins.workspace.agents` (ordered allowlist) | `.Agents []string` | Read at picker time |
| Agent launch commands | `plugins.workspace.agentStart` map | `.AgentStart map[string]string` | Read at launch |
| Auto-create shell | `plugins.workspace.autoCreateShell` | `.AutoCreateShell` | Read on tab focus |
| Worktree dir prefix | `plugins.workspace.dirPrefix` | `.DirPrefix` | Read at create |
| Overview location | `plugins.workspace.overviewWorktreeScope` (`project`\|`worktree`) | `.OverviewWorktreeScope` | Read at navigation |
| Sidebar display | `plugins.workspace.sidebarDisplay{hideRepoPrefix,hideAgent,hideTask,hideStats}` | `SidebarDisplayConfig` (config.go:184) | Read at render; plugin holds a copy |
| Interactive keys, copy-on-select | `plugins.workspace.interactive{Exit,Attach,Copy,Paste}Key`, `copyOnSelect` | `WorkspacePluginConfig` (config.go:149-160) | Resolved per terminal host by `app.TerminalConfig(cfg)` → `tty.Config` (`internal/app/terminal_config.go`); existing terminals need rebuild. `AttachKey` force-cleared unless `tmux_full_attach` |
| tmux capture limit | `plugins.workspace.tmuxCaptureMaxBytes` (default 2MB) | `.TmuxCaptureMaxBytes`, clamped in `Validate` | Read per capture |
| Worktree setup defaults | `plugins.workspace.worktreeSetup{copyEnvFiles,envFiles,runHook,hookPath,hookRequired}` | `WorktreeSetupConfig` (config.go:175) | Display-only in first slice per brief |
| Git panel | `plugins.git-status.enabled`, `.refreshInterval` | `GitStatusPluginConfig` | **Restart** (`assembly.Plan`) |
| Files panel | `plugins.file-browser.enabled` | `FileBrowserPluginConfig` | **Restart** |
| td panel | `plugins.td-monitor.enabled`, `.refreshInterval`, `.dbPath` | `TDMonitorPluginConfig` | **Restart** |
| Conversations panel | `plugins.conversations.enabled` + `.claudeDataDir` + flag `conversations_plugin` (`assembly.ConversationsWanted`, assembly.go:52) | `ConversationsPluginConfig` | **Restart** |
| Notes panel | flag `notes_plugin` only | `features.NotesPlugin` | **Restart** |
| Tasks | flag `tasks_plugin` only; `plugins.tasks.position` vestigial | `features.TasksPlugin` | **Restart** |
| Feature previews (Advanced) | `features.flags[<name>]` | `FeaturesConfig.Flags` | `features.IsEnabled` reads live config (`internal/features/features.go:164`); startup-snapshot consumers need restart |
| Keymap overrides | `keymap.overrides` | `KeymapConfig.Overrides` | Out of scope per brief |

**Feature flags** — `internal/features/features.go`: `Feature{Name,Default,Description}`, `allFeatures` (line 101), `Init(cfg)`, `SetOverride`, `IsEnabled(name)`, `List()`, `IsKnownFeature`. Flags: `tmux_interactive_input`(T), `tmux_full_attach`(F), `workspace_terminal_panel`(F), `tmux_inline_edit`(T), `files_auto_refresh`(T), `notes_plugin`(F), `tasks_plugin`(F), `conversations_plugin`(F), `workspace_doc_panes`(T), `cross_project_overview`(T). Advanced page maps: Cross-project Activity → `cross_project_overview`; Document panes → `workspace_doc_panes`; Full tmux attach → `tmux_full_attach`; Split workspace terminal → `workspace_terminal_panel`.

**Per-surface UI state (non-config)** — `internal/state/state.go` (`state.json`): pane widths, last global tab, active plugin per workdir, sorts, pins.

---

## 4. Theme system

- Built-ins: `internal/styles/themes.go` — `ListThemes()` (621), `GetTheme(name)` (596), `GetCurrentTheme/Name` (606/614), `RegisterTheme` (633), `ApplyTheme` (640), `ApplyThemeWithOverrides` (649), `ApplyThemeWithGenericOverrides` (673), `ApplyThemeColors` (893), `rebuildStyles()` (1015), `IsValidTheme` (588), `IsValidHexColor` (583).
- Community: `internal/community/schemes.go` — embedded index, `ListSchemes()`, `GetScheme(name)`, `SchemeCount()`. `internal/community/convert.go` — `Convert(scheme) styles.ColorPalette`, `PaletteToOverrides`, `FormatSchemeInfo`.
- **Four-color swatch data exists**: `internal/app/theme_switcher_modal.go:208-222` — built-in = `[Primary, Success, Secondary, Error]`; community = `[Red, Green, Blue, Purple]`.
- Unified list + filter + count: `themeEntry` (theme_switcher_modal.go:22), `buildUnifiedThemeList()` (31), `filterThemeEntries` (56), `themeSwitcherCountSection()` (113).
- **Live preview**: `Model.previewThemeEntry(entry)` (model.go:1492) → `applyThemeFromConfig` (1507) or `theme.ApplyResolved`. Restore-on-Escape: update.go:753.
- **Scope resolution**: `internal/theme/resolve.go` — `ResolveTheme(cfg, projectPath)`, `ApplyResolved(r)`. Applied at startup main.go:178-181 and `Model.previewProjectTheme()` (model.go:1013).
- **Save**: `Model.saveTheme(tc, scope)` (model.go:1084) → `config.SaveProjectTheme`/`SaveGlobalTheme`; `confirmThemeSelection` (model.go:1054) reloads config and toasts.
- Scope gating: `themeSwitcherHasProject()` (theme_switcher_modal.go:108) → `currentProjectConfig()` (model.go:1028, resolves worktree→main-repo).
- Existing inline picker inside Add Project: `renderProjectAddThemePickerOverlay` view.go:570, `initProjectAddThemePicker`/`previewProjectAddTheme`/`previewProjectAddCommunity` model.go:1164-1207, `handleProjectAddThemePickerKeys` update.go:2238, `handleProjectAddCommunityKeys` 2323.
- Gradient/panel primitives: `internal/styles/borders.go` — `RenderPanel(content,w,h,active)` (293), `RenderPanelWithGradient` (307), `RenderGradientBorder` (27), `GetActiveGradient/GetNormalGradient/GetFlashGradient/GetInteractiveGradient` (236-281); `internal/styles/gradient.go`.

---

## 5. Project management

- Config shape: `ProjectsConfig{Mode,Root,List []ProjectConfig}`; `ProjectConfig{Name,Path,Theme,LastOpenInApp,WorktreeSetup}` (config.go:28-43).
- **Add**: `Model.initProjectAdd()` (model.go:1128), `resetProjectAdd` (1153), `validateProjectAdd() string` (1211 — non-empty unique name, `os.Stat` IsDir, dup expanded path), `saveProjectAdd() tea.Cmd` (1255). UI: `internal/app/project_add_modal.go` (ids `project-add-*`).
  - **No filesystem path completion exists** — `internal/projectdir` and `internal/filefind` are the nearest helpers.
- **Rename / remove / reorder: none exist.** Route through `config.Load()` → mutate `cfg.Projects.List` → `config.Save(cfg)`. Rename/remove must consider workdir-scoped `state.json` keys (`internal/state/state.go`).
- **Project switcher modal** — `internal/app/view.go:228-568`; state in model.go: `initProjectSwitcher` (607), `projectSwitcherDestinations` (639), `filterProjects` (653), `activateProjectSwitcherDestination` (708), `switchProject` (704/804). Mouse: `handleProjectSwitcherMouse` (update.go:1869).
- **"No projects" empty state**: view.go:345-348 + 517-527 (`ctrl+a add`, `y copy prompt`); keys update.go:1000. `copyProjectSetupPrompt()` (model.go:1096). Condition: `len(m.cfg.Projects.List) == 0 && !m.globalScopeAvailable()`.
- **Empty Workspaces surface** — `internal/plugins/workspace/sidebar_shared.go:284` (`"No workspaces"` / `"Press 'n' to create one"`); global-scope placeholder `renderGlobalWorkspacesPlaceholder` (view.go:988). Both get the mockup's "Open Sidecar Setup" action.
- Worktree helpers: `app.GetMainWorktreePath`, `CheckCurrentWorktree` (internal/app/git.go), `internal/workspaceinventory`, `internal/app/worktree_switcher_modal.go`.

---

## 6. Updater

- `internal/version`: `Descriptor` (`product.go:26`), `SidecarDescriptor()` (78), `TdDescriptor()` (95), `TasksDescriptor()` (111), `DescriptorFor` (137), `InstallHint()` = `brew install <formula>` (152), `UnmanagedHint()` (163).
- **Provenance**: `InstallMethod` = `homebrew`|`go`|`binary` (`install.go`); `DetectInstallation(ctx, env, d, latest)` (`updater.go:116`), `ownedByHomebrew` (153). `Installation{Method, Managed, ManualCommand}`.
- **Check**: `CheckProductAsync(d, currentVersion, force) tea.Cmd` (`checker.go:26`) → `ProductStatusMsg{Target}`. Cache: `internal/version/cache.go`.
- **Install**: `Runner`/`ExecRunner`, `Environment`/`DefaultEnvironment`, `SelectPlan`, `RefreshPackageMetadata` (267), `RestartRequired`, `Result{Status}`.
- App wiring: `internal/app/update_targets.go` — `updateDescriptors()` (38), `productCheckCmds(force)` (51), `setProductStatus` (65), `productTarget(id)` (104), `hasUpdatesAvailable()` (115), `startUpdateBatch` (131), `runUpdateTarget` (179). Checks fire from `Model.Init()` (model.go:456).
- Modal: `internal/app/update_modal.go` — states in `commands.go:127`, `renderUpdateModalOverlay` (54), `ensureUpdatePreviewModal` (114). Keys update.go:1981; mouse 2130.
- **No update channels exist anywhere.**

---

## 7. Existing diagnostics-relevant checks

- **tmux availability**: `exec.LookPath("tmux")` at `internal/workspaceops/shell.go:146` and `internal/tty/editor_session.go:54`. Install copy: `getTmuxInstallInstructions()` at `internal/plugins/workspace/shell.go:113`.
- **tmux version**: none (`tmux -V` unparsed anywhere).
- **tmux socket/namespace**: `internal/tmuxenv/tmuxenv.go` — `SocketPath()`, `Namespace()` (pure, works when tmux absent).
- **Truecolor detection: does not exist.** No `COLORTERM` references in Go code.
- **AGENTS.md detection**: only td's, at `internal/plugins/tdmonitor/setup_modal.go:166-215` — `preferredAgentFile(baseDir)`, `hasTDInstructions(path)`, `installInstructions(path)`, `prependToFile` (skips YAML frontmatter). Parameterizable, but currently prepends without confirmation; the brief requires review-then-confirm.
- **`sidecar agents`**: `internal/cli/cli.go:34` (`agents`/`--agents`) → `cli.RenderAgents(RootCommand())` (`internal/cli/help.go:220`), walks `Command.Agent AgentDoc{Invocation,Summary}`.
- **Existing diagnostics modal** (`!`): `internal/app/diagnostics_modal.go` — `ensureDiagnosticsModal` (17); plugins self-report via `plugin.DiagnosticProvider.Diagnostics()`. Context `"diagnostics"`.
- td availability: `internal/plugins/tdmonitor/plugin.go:99`; not-installed screen `internal/plugins/tdmonitor/notinstalled.go:264`.

---

## 8. CLI entry and `sidecar setup`

- **Not cobra.** `internal/cli/command.go` (`Command{Name,Summary,Usage,Long,Flags,Args,ExitCodes,Examples,Targets,Agent,Sub,Run}`), `internal/cli/registry.go` (`RootCommand()`), `internal/cli/help.go`, `internal/cli/cli.go` (`Run(args, stdout, stderr) (handled bool, exitCode int)`).
- **Dispatch**: `cmd/sidecar/main.go:66-69` — `cli.Run(os.Args[1:], ...)` before `flag.Parse()`. `handled=false` for unknown first arg → falls through to flag parsing + TUI.
- **`sidecar open` path**: `internal/cli/open.go` + `open_target.go` write a request file into `$XDG_STATE_HOME/sidecar/requests`; app watches via `internal/uirequest/watcher.go`, started in `Model.Init()` (model.go:483), consumed via `listenForUIRequests` (model.go:436).
- **`sidecar setup` shape**: cleanest seam is `Run` returning `handled=false` after recording a startup destination (package-level var or env), then `main.go` passes it into `app.New(...)` (main.go:229, `New(reg, km, cfg, currentVersion, workDir, projectRoot, initialPluginID)`). Add the `Command` in `RootCommand()` with an `Agent AgentDoc`.
- **Startup failure paths before first render** (`cmd/sidecar/main.go`): isolation check exit 1 (79); config load failure (140); `filepath.Abs` failure (161); non-TTY stdout (233); `p.Run()` error (256). These are where the brief's plain-language recovery diagnosis attaches. `internal/startuptrace` instruments this path.

---

## 9. Rendering building blocks

- **Rounded gradient panel**: `internal/styles/borders.go` `RenderPanel` (293) / `RenderPanelWithGradient` (307). Refuses below 3×3; content begins one row / two cols inside.
- **Two-pane composition references**: `internal/plugins/filebrowser/view.go:255-310`, `internal/plugins/workspace/view_list.go:172-214`, `internal/plugins/conversations/view_layout.go:55-105`, `internal/plugins/notes/view.go:87-88`.
- **lipgloss v2** (`charm.land/lipgloss/v2`) + `ansi` (`ansi.StringWidth`, `ansi.Truncate`, `ansi.TruncateLeft`). Styles registry: `internal/styles/styles.go`.
- **Bubbles**: only `charm.land/bubbles/v2/textinput` used broadly. **No bubbles list/table** — lists are hand-rendered cursor + scroll offset (`internal/scroll`, `internal/ui/scrollbar.go`).
- **Modal library** — `internal/modal/`: sections `Text/Spacer/When/Custom/ScrollingCustom/Buttons/Checkbox/Input/List/Combo`; `RenderedSection{Content, Focusables, Overlay}`. Overlay compositing: `internal/ui/overlay.go` `OverlayModal` (120), `internal/ui/canvas.go`. Not the home for Configuration (single centered box, transient-dialog focus model), but its `Custom` + `FocusableInfo` + mouse-handler pattern is the right interaction model to reimplement inside Configuration panes.
- **Existing form pattern**: `internal/app/project_add_modal.go` (label-above-input; the mockup's fixed left label column is new), `internal/ui/confirm_dialog.go`.

---

## 10. Notes / Tasks beta integrations

- Enablement today is feature flags only (`notes_plugin`, `tasks_plugin`). `assembly.Plan` gates Notes (assembly.go:87); Tasks is the `globalTasksHost` global tab (`scope.go:367`).
- Notes is an in-repo plugin (`internal/plugins/notes/`) with **no external executable** — the mockup's "Notes command not found on PATH" has no backing model.
- **Tasks has an external suite**: `version.TasksDescriptor()` declares executables `tasks`, `tasks-tui`, `tasks-api`, formula `marcus/tap/tasks`. PATH check: `env.LookPath(d.Executable)` (checker.go:43). Homebrew availability: `env.LookPath("brew")` (updater.go:158). **No `brew install` execution path exists** (only `brew upgrade` in the updater); `Descriptor.InstallHint()` returns the string only.
- td repair prototype: `internal/plugins/tdmonitor/setup_modal.go` `SetupModel`.

---

## 11. Testing conventions

- Plain `go test ./...`; table-driven, colocated. No teatest/golden-frame harness. `screenmodel` is a terminal emulator model, not a TUI test harness.
- App-level tests construct `app.Model` directly and assert on `View()`/`renderHeader()`/`Update()` strings:
  - `internal/app/view_test.go` — header/clock/selector assertions.
  - `internal/app/key_precedence_test.go` — **canonical example for proving typed input does not leak to global shortcuts; mirror for `config-edit`.**
  - `internal/app/scope_test.go` + `scope_baseline_test.go` — scope takeover, tab ownership, context resolution.
  - `global_frame_test.go`, `view_mouse_test.go`, `plugin_footer_test.go`, `modal_focus_test.go`, `global_navigation_test.go`.
- Config tests: `internal/config/loader_test.go`, `saver_test.go` with `config.SetTestConfigPath`.
- Feature-flag tests use `features.Init(config.Default())` in-memory (`internal/plugins/assembly/assembly_test.go:22`).
- **Visual proof**: `scripts/tmux-drive.sh` (`paths` first; always `stop`). See AGENTS.md.

---

## 12. Mockup elements with NO existing backing capability (arbiter decisions in the decisions doc)

1. **Update channel selector** — no channel concept anywhere; implies release-infra change.
2. **Notes-command-on-PATH + Homebrew install (08a)** — Notes has no external executable; Tasks does. No `brew install` execution path exists.
3. **Truecolor detection + terminal-colors repair (01b)** — zero existing code.
4. **tmux version detection ("3.0+")** — only LookPath exists.
5. **Header clock** — config exists, renderer does not (view_test asserts absence).
6. **Per-project "Open in" preference** — only last-used memory exists.
7. **Copy-on-select** — backed end-to-end; changes need terminal-host rebuild.
8. **Project rename/remove/reorder** — only add exists; watch state.json workdir keys.
9. **Path completion in Add Project** — none exists.
10. **Sidebar settings search** — needs a small hand-written static index.
11. **`plugins.notes` dead config** — loader/saver lack the field.
12. **Panel ON/OFF restart-scoped** — `assembly.Plan` runs once at startup.
13. **Nerd Font toggle startup-scoped** — assign `styles.PillTabsEnabled` at runtime on save.
14. **About doc links** — no URL-open helper in app; check `internal/terminallink`.

---

## Quick "start here" list

| Task | File |
|---|---|
| Configuration surface in the frame | `internal/app/scope.go` (copy `ScopeGlobal` shape), `internal/app/view.go:946` `renderContent` |
| Gear in the header | `internal/app/view.go:702` `headerGeometry` + `internal/app/update.go:285` header click switch |
| Keymap contexts | `internal/keymap/bindings.go` `DefaultBindings()`; `internal/app/update.go:1640` `updateContext`, `:1815` `isTextInputContext` |
| Persist a setting | `internal/config/saver.go` `Save()` / targeted `Save*` helpers (always `Load()`-then-`Save()`) |
| Theme page + inline picker | `internal/app/theme_switcher_modal.go`, `internal/theme/resolve.go` |
| Projects page | `internal/app/model.go:1128-1312`, `internal/app/project_add_modal.go` |
| Panel frame | `internal/styles/borders.go` `RenderPanel` / `RenderPanelWithGradient` |
| Updater handoff | `internal/app/update_targets.go` + `internal/app/update_modal.go` |
| `sidecar setup` | `internal/cli/registry.go` `RootCommand()`, `internal/cli/cli.go` `Run()`, `cmd/sidecar/main.go:66` and `:229` |
| Proof | `scripts/tmux-drive.sh`; tests modeled on `key_precedence_test.go` and `scope_test.go` |
