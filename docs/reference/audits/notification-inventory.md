# Notification / Toast Inventory & Audit

Date: 2026-08-19
Scope: `notification-center` branch, current state of `internal/notify` and all producer call sites.

## Audit criteria

Now that notifications are far more visible (toast overlay + header indicator + persistent right-panel
centre), a notification earns its place only if it's:

- **plausible confusion** — without it, the user would reasonably wonder "did that work?"
- **an action to take** — the user needs to do something in response
- **something the user would be glad to see** — a genuine win worth surfacing (not routine success)
- **something that definitely needs to be fixed** — a real error/failure

Everything else — confirmation of an action the user just took and can already see the result of
(clipboard copy, sidebar toggle, "opened X", routine "Saved") — is noise once it's sitting in a
persistent centre instead of a footer line that fades on its own.

**Verdict counts: 24 keep, 15 consider, 46 remove** (of 85 call sites; row numbers below refer to the
original inventory, preserved at the bottom of this doc for line references).

## KEEP

Real errors, blocked actions with a reason, or state changes the user must know about.

| # | Message / trigger | Why keep |
|---|---|---|
| 1, 3 | update check summary | actionable — user may want to update |
| 2 | async update op errors | real error |
| 6, 7 | config save/action fails | real error |
| 11, 19 | "Worktree no longer exists" | surprising state change, explains a blocked action |
| 12 | "Overview item is stale: err" | real error |
| 15 | "Failed to open "+appName | real error |
| 20 | "Theme applied (save failed)" | partial failure — silently losing the save would confuse later |
| 23, 24, 25 | project-add: state missing / config load fails / save fails | real errors, one is a partial failure |
| 30 | "Reveal failed: err" | real error |
| 32 | "Reviewed source changed; review the updated diff" | action to take — stale review is a correctness risk |
| 33 | "Agent failed, using commit summary" | silent fallback the user needs to know happened |
| 34 | "Merge aborted; target restored" | important state change, prevents confusion about repo state |
| 35 | "Terminal: err" | real error |
| 36, 39 | action blocked with dynamic `reason` | prevents confusion about why a keypress did nothing |
| 55 | conversations error notice | real error |
| 56, 58, 59 | export/resume/session-not-found failures | real errors |
| 64 | "Invalid line number" | actionable — user needs to retry |
| 66 (fail half) | "Move failed: err" | real error |
| 72 | "Git write already in progress" | prevents confusion about a dropped keypress |
| 79 | "workspace is no longer in the catalog" | surprising state change |
| 81 | pane layout too small, "layout left unchanged" | explains why an action silently no-oped |
| 85 | external `sidecar notify post` (agent/waiting/session/td/tasks sources) | this is the intended rich channel — keep and prioritize building producers that use it, see Recommendations |

## CONSIDER

Not obviously wrong, but worth a second look — either the message is dynamic/unaudited (could go
either way depending on content) or it's borderline routine.

| # | Message / trigger | Question to resolve |
|---|---|---|
| 5, 9 | generic config-surface toast helpers (dynamic `notice`/`message`) | keep only if the specific notices passed in are errors/surprises, not routine confirmations |
| 13, 18 | constructed multi-field toasts in update.go/model.go | audit call sites individually — likely a mix |
| 14 | "No worktrees found" | mild — arguably useful once, but could be inferred from an empty list in the UI |
| 21 | theme toast (dynamic) | keep if it's the "save failed" branch, drop if routine "theme applied" |
| 26 | "Added project: name" | user-glad-to-see, but redundant with the visible new project row — lean remove |
| 28, 29, 38, 40, 77 (diff-disabled), 82 | "diff panes disabled" feature-flag notices | this is static config state, not an event — better as a persistent UI affordance (dimmed control, help text) than a toast that fires repeatedly |
| 41 | "terminal history exhausted" | genuine boundary the user might not expect — keep if it fires rarely, drop if it fires every scroll-to-top |
| 42, 46, 49 | dynamic task-modal / note toasts | audit actual strings — likely mostly routine confirmations to cut |
| 53 | "No categorized sessions" | mild confusion-prevention, borderline routine |
| 57 | generic status msg (isError variable) | keep the error branch, drop the info branch |
| 65 (non-fail half), 68 | drag/move & inline-edit success confirmations | the file move/edit is already visible in the browser — likely remove, but check if any of these are the *only* signal for an off-screen destination |
| 70 (non-fail half) | "Opening in GitHub..." | routine, but very short-lived — low cost either way |
| 71 | git op results (watch/stage/discard/refresh/diff/commit/init) | split — real failures keep, routine successes (stage/refresh/diff succeeded) remove |
| 75 | td task actions (dynamic) | audit strings — likely mixed |
| 76 (non-error rows) | "Owning project is no longer configured" | borderline — surprising but low-stakes; lean keep |
| 78 | interactive session lifecycle ("Session ended", typing-mode hint, history exhausted) | "Session ended" may be glad-to-see if long-running; typing-mode hint is probably routine — split |

## REMOVE

Routine confirmation of an action the user just took, where the result is already visible on screen.
These are the spam risk once notifications live in a persistent, visible centre.

| Pattern | Rows | Why remove |
|---|---|---|
| "Already on this project/worktree" (no-op selection) | 4, 10, 17 | user is already looking at it — telling them nothing changed adds nothing |
| "Sidebar hidden (\ to restore)" | 37, 51 (×2), 62, 69 (×2), 80 | fires from ~6 different plugins for the same trivial toggle the user just performed and can see |
| Clipboard/yank copy confirmations | 22, 48, 50, 60, 61, 63, 67, 73, 74, 83, 84 | copying is a low-stakes, instant, self-evident action; a toast per copy is the textbook spam case |
| "Opened "+path / "Opened in "+appName | 8, 16 | the app opening *is* the confirmation |
| "Saved" (notes) | 45 | routine, high-frequency, self-evident (content is visibly persisted/state indicator exists) |
| "Nothing to undo" / "No title to copy" / "No content to copy" | 43, 44, 47 | telling the user an action they took had nothing to act on — low value, easily inferred |
| Filter/view-state confirmations | 52, 54 | "Showing all sessions" / "Showing X sessions" — the filtered list itself is the confirmation |
| "Nothing to commit — workspace was already clean" | 31 | no-op, self-evident from an unchanged git status view |
| Default theme notice | 27 | one-time cosmetic notice, not actionable |
| "Moved "+name+" → "+dir (success half of 66) | 66 | file browser already shows the move |

## Recommendations

1. **Cut the ~46 REMOVE rows first** — they're the majority of the ~85 call sites and are pure
   confirmation noise now that toasts also populate a persistent centre. Most collapse to deleting
   the `ShowToast`/`ToastMsg` call at the site; the action itself (copy, move, toggle) still happens
   and is still visible in the UI.
2. **Route the ~24 KEEP rows through severity properly.** All of them currently arrive as generic
   `Source: system` with only `info`/`error` — none use `warning`, and none carry `Body` or
   `Targets`. Since the model already supports `waiting`/`agent`/`session`/`td`/`tasks` sources with
   distinct hues/priorities, wiring at least the blocked-action and merge/commit-lifecycle KEEP rows
   (32-35, 72, 79) to a more specific source would let the centre visually separate "you need to act"
   from "FYI" without adding new UI.
3. **`app.PostNotification`/`notify.PostMsg` has zero in-repo callers.** It's the only path that can
   set `Body`/`Targets`/non-`system` sources, and it's currently reachable only via the external CLI.
   If any KEEP items deserve deep-links (e.g. "Reviewed source changed" → jump to the diff), this is
   the primitive to use instead of adding new toast-string plumbing.
4. **Resolve CONSIDER rows by reading the actual dynamic strings** at each site (several pass a
   `reason`/`message`/`text` variable whose content wasn't enumerated here) — instructions above
   should make the keep/cut call obvious once the string is in hand.

---

## Appendix: full original inventory (85 call sites)

| # | File:Line | Trigger | Severity | Message | Subsystem |
|---|---|---|---|---|---|
| 1 | internal/app/update_targets.go:274 | update check completes | info | `version.Summarize(results)` | app shell / updater |
| 2 | internal/app/update.go:459 | async update op errors | error | `"Error: "+err` | app shell / updater |
| 3 | internal/app/update.go:646 | update summary ready | info | `summary` | app shell / updater |
| 4 | internal/app/model.go:842 | project switch to same project | info | `"Already on this project"` | app shell / project switcher |
| 5 | internal/app/config_surface.go:296 | config change applied | info | `r.Message(notice)` | app shell / config surface |
| 6 | internal/app/config_surface.go:353 | config save fails | error | `"Save failed: "+err` | app shell / config surface |
| 7 | internal/app/config_surface.go:420 | config action fails | error | `message` | app shell / config surface |
| 8 | internal/app/config_surface.go:422 | file opened from config | info | `"Opened "+path` | app shell / config surface |
| 9 | internal/app/config_surface.go:470 | generic config toast helper | info | `message` | app shell / config surface |
| 10 | internal/app/worktree_switcher_modal.go:401 | select current worktree | info | `"Already on this worktree"` | app shell / worktree switcher |
| 11 | internal/app/worktree_switcher_modal.go:408 | worktree vanished | error | `"Worktree no longer exists"` | app shell / worktree switcher |
| 12 | internal/app/update.go:549 | stale overview item error | error | `"Overview item is stale: "+err` | app shell |
| 13 | internal/app/update.go:571 | constructed toast, multi-field | varies | see call site | app shell |
| 14 | internal/app/update.go:1750 | worktree list empty | info | `"No worktrees found"` | app shell |
| 15 | internal/app/open_in_modal.go:388 | "open in app" fails | error | `"Failed to open "+appName` | app shell / open-in |
| 16 | internal/app/open_in_modal.go:409 | "open in app" succeeds | info | `"Opened in "+appName` | app shell / open-in |
| 17 | internal/app/model.go:929 | duplicate project switch | info | `"Already on this project"` | app shell |
| 18 | internal/app/model.go:1069 | constructed toast | varies | see call site | app shell |
| 19 | internal/app/model.go:1088 | worktree vanished | error | `"Worktree no longer exists"` | app shell |
| 20 | internal/app/model.go:1191 | theme applied but save failed | error | `"Theme applied (save failed)"` | app shell / theme |
| 21 | internal/app/model.go:1210 | theme toast | info | `toastMsg` | app shell / theme |
| 22 | internal/app/model.go:1249 | copy LLM setup prompt | info | `r.Message("Copied LLM setup prompt")` | app shell |
| 23 | internal/app/model.go:1344 | project add state missing | error | `"Project add state missing"` | app shell / project add |
| 24 | internal/app/model.go:1368 | config load fails during add | error | `"Failed to load config: "+err` | app shell / project add |
| 25 | internal/app/model.go:1378 | project add save fails | error | `"Added project (save failed: "+err+")"` | app shell / project add |
| 26 | internal/app/model.go:1389 | project added | info | `"Added project: "+name` | app shell / project add |
| 27 | internal/app/theme_notice.go:42 | default theme notice | info | `theme.DefaultThemeNotice` | app shell / theme |
| 28 | internal/plugins/workspace/diff_panes.go:50 | diff panes disabled by flag | info | `features.WorkspaceDocPanesDisabledDiff` | workspace |
| 29 | internal/plugins/workspace/diff_panes.go:66 | same | info | same | workspace |
| 30 | internal/plugins/workspace/update.go:80 | file reveal fails | info | `"Reveal failed: "+err` | workspace |
| 31 | internal/plugins/workspace/update.go:1613 | commit with clean tree | info | `"Nothing to commit — workspace was already clean"` | workspace / git |
| 32 | internal/plugins/workspace/update.go:1628 | reviewed source changed mid-merge | info | `"Reviewed source changed; review the updated diff before pushing"` | workspace / merge |
| 33 | internal/plugins/workspace/update.go:1702 | commit-summary agent fails | info | `"Agent failed, using commit summary"` | workspace / commit |
| 34 | internal/plugins/workspace/update.go:1787 | merge aborted | info | `"Merge aborted; target restored"` | workspace / merge |
| 35 | internal/plugins/workspace/update.go:2017 | terminal error | info | `"Terminal: "+err` | workspace |
| 36 | internal/plugins/workspace/keys.go:687,705,971 | action blocked, reason given | info | `reason` (dynamic) | workspace |
| 37 | internal/plugins/workspace/keys.go:1017 | sidebar toggled | info | `"Sidebar hidden (\ to restore)"` | workspace |
| 38 | internal/plugins/workspace/mouse.go:51 | diff panes disabled (mouse path) | info | `features.WorkspaceDocPanesDisabledDiff` | workspace |
| 39 | internal/plugins/workspace/plugin.go:711 | pending overview action blocked | info | `reason` | workspace |
| 40 | internal/plugins/workspace/ui_requests.go:185 | diff panes disabled (UI request) | info | `features.WorkspaceDocPanesDisabledDiff` | workspace |
| 41 | internal/plugins/workspace/terminal_history.go:150 | terminal history exhausted | info | `tty.HistoryExhaustedNotice` | workspace / terminal |
| 42 | internal/plugins/notes/task_modal.go:299,308,326 | task modal actions | info | `text` (dynamic) | notes |
| 43 | internal/plugins/notes/plugin.go:2686 | copy with no title | info | `"No title to copy"` | notes |
| 44 | internal/plugins/notes/plugin.go:2698 | copy with no content | info | `"No content to copy"` | notes |
| 45 | internal/plugins/notes/plugin.go:2958 | note saved | info | `"Saved"` | notes |
| 46 | internal/plugins/notes/plugin.go:2994 | dynamic note toast | info | `text` | notes |
| 47 | internal/plugins/notes/plugin.go:3036 | undo unavailable | info | `"Nothing to undo"` | notes |
| 48 | internal/plugins/notes/plugin.go:1241,2665,2690,2702,2967 | clipboard/save actions | info | various copy confirmations | notes |
| 49 | internal/plugins/notes/inline_edit.go:102 | inline edit event | info | (dynamic) | notes |
| 50 | internal/plugins/notes/mouse.go:705 | copy via mouse | info | `r.Message("Copied to clipboard")` | notes |
| 51 | internal/plugins/conversations/plugin_input.go:128,494 | sidebar toggled | info | `"Sidebar hidden (\ to restore)"` | conversations |
| 52 | internal/plugins/conversations/plugin_input.go:221 | filter cleared | info | `"Showing all sessions"` | conversations |
| 53 | internal/plugins/conversations/plugin_input.go:233 | no categorized sessions | info | `"No categorized sessions"` | conversations |
| 54 | internal/plugins/conversations/plugin_input.go:249 | category filter applied | info | `"Showing "+label+" sessions"` | conversations |
| 55 | internal/plugins/conversations/plugin.go:715,1084 | error notice (dynamic `n`) | error | `n` | conversations |
| 56 | internal/plugins/conversations/plugin.go:1425,1430,1444,1446 | export session | info/error | export status messages | conversations |
| 57 | internal/plugins/conversations/plugin.go:1570,1590 | generic status msg | info/error (isError var) | `msg` | conversations |
| 58 | internal/plugins/conversations/resume_modal.go:249,257,312 | resume session fails | error | "No session selected" / "Resume not supported for "+adapter | conversations |
| 59 | internal/plugins/conversations/content_search_input.go:246 | session not found | error | `"Session not found"` | conversations |
| 60 | internal/plugins/conversations/clipboard.go:23,36,70 | copy actions | info | yank confirmations | conversations |
| 61 | internal/plugins/filebrowser/handlers.go:333 | mark file for copy | info | `"Marked for copy: "+path` | filebrowser |
| 62 | internal/plugins/filebrowser/handlers.go:421,660 | sidebar toggled | info | `"Sidebar hidden (\ to restore)"` | filebrowser |
| 63 | internal/plugins/filebrowser/handlers.go:1218 | copy hash/info | info | `info` (dynamic) | filebrowser |
| 64 | internal/plugins/filebrowser/handlers.go:1249 | bad line number entered | info | `"Invalid line number"` | filebrowser |
| 65 | internal/plugins/filebrowser/mouse.go:904,933,946,954 | drag/move via mouse | info | copy/move status incl. failures | filebrowser |
| 66 | internal/plugins/filebrowser/plugin.go:1135,1139 | drag-drop move result | info | `"Move failed: "+err` / `"Moved "+name+" → "+dir` | filebrowser |
| 67 | internal/plugins/filebrowser/operations.go:941,955,960 | copy operations | info | copy status | filebrowser |
| 68 | internal/plugins/filebrowser/inline_edit.go:85,138,182,197 | inline edit copy/save | info | edit status | filebrowser |
| 69 | internal/plugins/gitstatus/update_handlers.go:157,617 | sidebar toggled | info | `"Sidebar hidden (\ to restore)"` | gitstatus |
| 70 | internal/plugins/gitstatus/github.go:80,83,98,103,109 | GitHub open action | info/error | remote/platform errors, "Opening in GitHub..." | gitstatus |
| 71 | internal/plugins/gitstatus/plugin.go:466,490,503,525,555,809,874,890,1021,1031,1044 | git op results (watch/stage/discard/refresh/diff/commit/init) | info/error | operation-specific | gitstatus |
| 72 | internal/plugins/gitstatus/write_operation.go:120 | concurrent git write | error | `"Git write already in progress"` | gitstatus |
| 73 | internal/plugins/gitstatus/error_modal.go:161 | copy error output | info | `r.Message("Yanked error output")` | gitstatus |
| 74 | internal/plugins/gitstatus/clipboard.go:22,35 | copy actions | info | yank confirmations | gitstatus |
| 75 | internal/plugins/tdmonitor/plugin.go:302,367,417 | td task actions | info/error | constructed, dynamic | tdmonitor |
| 76 | internal/overview/global_actions.go:123,233,276,280 | global workspace action results | info/error | reason / "Owning project is no longer configured" / "Delete failed: "+err / joined warnings | overview |
| 77 | internal/overview/preview_diff.go:51,469 | diff panes disabled / yank | info | feature notice / "Yanked: "+ident | overview |
| 78 | internal/overview/interactive.go:201,281,312,646 | interactive session lifecycle | info | "Session ended" / reason / typing-mode hint / history exhausted | overview |
| 79 | internal/overview/model.go:1197 | workspace no longer in catalog | info | `name+" is no longer in the catalog"` | overview |
| 80 | internal/overview/workspaces.go:722 | sidebar toggled | info | `"Sidebar hidden (\ to restore)"` | overview |
| 81 | internal/overview/preview_links.go:579 | pane layout too small | info | `name+" pane needs a "+dimension+" window; layout left unchanged"` | overview |
| 82 | internal/overview/preview_tabs.go:61 | diff panes disabled | info | feature notice | overview |
| 83 | internal/issueview/clipboard.go:32 | copy action | info | `r.Message(ok)` | issueview |
| 84 | internal/docview/path.go:91,100,114 | copy path/content | info | copy confirmations | docview |
| 85 | internal/cli/notify.go (runNotifyPost) | external `sidecar notify post <title>` | caller-specified (default info) | caller-specified title/body | CLI → external agent/hook/script |

### Data model reference

`notify.Notification` (`internal/notify/notify.go`):

```
Notification{ID, Source, Severity, Title, Body, Targets, CreatedAt, ReadAt, DismissedAt, ExpiresAt, Origin, Sticky}
```

- **Severities**: `info | warning | error` (default `info`).
- **Sources** (glyph/hue/priority/default toast expiry defined in `internal/notify/notify.go:90-97`):
  `waiting` (warn, 60, sticky) · `agent` (primary, 50, 8s) · `session` (success, 40, 8s) ·
  `td` (secondary, 30, 8s) · `tasks` (info, 20, 8s) · `system` (muted, 10, 5s).
- `Origin` (tmux session/project/workdir/pid) enables self-dismiss.
- `Targets` supports deep-link kinds (issue/task/commit/file/session/url) but is stored-only today.

### Surfaces

- Toast overlay — `internal/app/toast_view.go`
- Header indicator
- Right-panel notification centre — `internal/app/notification_centre.go`

All three read the same store via `internal/app/notifications.go`.

### Producer paths

1. **Legacy toast funnel** — `ToastMsg`/`msg.ShowToast` → `Model.showToastWithSeverity`
   (`internal/app/model.go:687-706`) → `postNotification`. All 84 in-app call sites land as
   `Source: system`, severity `info`/`error` only, no `Body`/`Targets`.
2. **Direct post** — `app.PostNotification`/`notify.PostMsg` (`internal/app/notifications.go:23-26`)
   supports full fields but has **zero in-repo callers**.
3. **External CLI** — `sidecar notify post <title> [--body] [--source] [--expiry]`
   (`internal/cli/notify.go:runNotifyPost`) is the only path that populates the richer sources; meant
   for external agents/hooks, not invoked from within this repo.
