# Baseline: workspace sidebar before the redesign

**Captured:** 2026-08-15, on `workspace-panel-redesign` at `97aaead7` (fast-forwarded to `main`).
**Companion to:** [Workspace sidebar as one cross-project control surface](workspace-sidebar-redesign.md)

This is slice 0 of that plan, kept deliberately small. It exists to make the
current behaviour visible and diffable before anything changes, and to correct
the plan's current-state table where the code disagrees with it. It asserts
nothing about what the redesign should be.

## What was captured

`internal/workspacelist/baseline_test.go` renders both sidebar shapes from one
set of semantic records at the four responsive tiers (56, 40, 26, 18 columns)
and pins the result in `internal/workspacelist/testdata/baseline-sidebar.txt`.

Regenerate after an intended change:

```bash
go test ./internal/workspacelist -run TestSidebarBaselineFixture -update
```

The golden strips ANSI. It captures structure — field order, section wording,
control placement, width degradation — which is what the redesign moves. Colour
belongs to the styles package and would make the file unreadable in review.

A second test, `TestSidebarBaselineWidthInvariants`, pins the two things the
redesign must not break while it moves everything else: no line exceeds its
allocated width, and no panel renders more rows than it was given.

The existing `internal/plugins/workspace/sidebar_baseline_test.go` already
covers project-side navigation, selection reset, and heading/separator
placement. That was left alone; this adds the cross-surface comparison it
cannot make from inside the plugin.

Not captured, deliberately: a real-app tmux run. A correctly isolated run
(`scripts/tmux-drive.sh paths` confirms both the tmux socket and the state tree
resolve under `/private/tmp/sidecar-drive-501`) starts with no projects, shells,
or worktrees, so it would photograph an empty list. Real-app proof is worth
doing once a slice actually changes on-screen behaviour.

## Convergence is further along than the plan's table implies

The plan's current-state table reads as though the two surfaces are separately
built. They are not. Both render through `workspacelist.RenderSidebar`, and
both use `workspacelist.RenderRow` for row layout, marker placement, provider
treatment, ANSI-safe fitting, and selection. Global reaches it through
`workspacelist.Model.Render`; project composes `SidebarOptions` directly in
`internal/plugins/workspace/sidebar_shared.go`.

The drift is entirely in **what each caller hands the shared renderer**. That is
good news for the redesign: most of slices 1 and 2 are changes to two call
sites and one options struct, not a rewrite.

## Correction to the first capture

The first version of this document reported that "the project row has no age".
That was an artifact of the fixture, not the product: `renderWorktreeItemKind`
has always passed an `Age`, and the fixture did not. The fixture has since been
made faithful to `view_list.go`, and the real asymmetries it then exposed are
recorded below. The lesson is worth keeping — a fixture that paraphrases a
caller will confidently report the paraphrase's bugs as the caller's.

## Findings from the capture

### 1. The age column existed but was empty, and shells had none at all

Three separate problems wearing one coat:

- **Shell rows passed no `Age`.** Worktree rows did. The one list where the two
  kinds sit together answered "how long since anything happened here?" for half
  its rows.
- **Worktree rows passed an age that was always blank.** Nothing on the
  discovery path ever set `UpdatedAt`. `snapshotToWorktrees` builds every
  worktree the sidebar shows and sets neither timestamp, so
  `formatRelativeTime` was returning `""` for every row in every real session.
  The dead comment in `parseWorktreeList` — "Will be updated from file stat" —
  is a promise from a function that no longer has a caller.
- **Two age formatters.** The project sidebar's `formatRelativeTime` says "now"
  under a minute; the shared `RelativeAge` says "now" under five seconds and
  then counts seconds. Same column, two vocabularies.

**Fixed.** Shells report their agent's last output (recorded only when the
capture actually differed, so an idle session's age climbs instead of resetting
on every poll) and fall back to creation time. Worktrees report their agent's
last output and fall back to the worktree directory's modification time, which
`snapshotToWorktrees` was already stat-ing and discarding. Ages now render:
`19m`, `12h`, `30m` against a real repository.

The two formatters are still two formatters. Consolidating them is a one-line
change but it moves the sub-minute wording on both surfaces, which is a visible
decision rather than a fix — left for the slice that owns the row grammar.

### 1b. Top-level shells had no kind glyph, and a different indent

`renderShellEntryForSession` passed no `Kind`, so a top-level shell was the only
row in either sidebar with no kind marker — and, because the gutter is narrower
without one, its second line hung three columns in while every worktree's hung
five. Two grammars in one list, visible as a ragged left edge.

**Fixed.** Top-level shells carry `❯` like nested shells and like every shell in
the global list, and both sections now share one gutter.

### 2. A plain shell always costs two rows even when its second line is empty

`RenderRow` returns two physical lines for any row at or above `twoLineWidth`,
including when line two renders empty. The doc comment says line two "collapses
to nothing when there is none" — it collapses to empty *content*, not to zero
*rows*.

Visible in the golden at 56 columns: `◎ ❯ scratch` is followed by a blank line
that belongs to the row, then the section separator blank. On the global side
the same waste pushes the entire `No Session (1)` section off an 18-row panel
while two blank lines sit on screen above it.

It bites hardest in global, where the comment in `listItem` is explicit that a
plain shell "shows nothing" on line two — every plain shell in the global list
was spending a blank row. Project-side shells always carry a status word, so
they were never affected, which is why the waste was easy to miss.

**Fixed.** `RenderRow` returns one line when line two would be blank. In the
56-column capture the global list gains a row and the `Idle` section, which
previously had its second line cut off, now fits.

### 3. Narrow widths produce controls that cannot be read or reached

At 18 columns:

- the project `Workspaces` section's `+` truncates to a bare `…`, leaving a
  clickable target with no legible meaning;
- the section heading loses its count (`Needs Attention …`);
- the global sort control truncates to `Activi…`.

Nothing degraded in a defined order; the header was just clipped from the right.

**Fixed.** Chrome now degrades in a stated order. A control that cannot be drawn
beside the whole title is dropped entirely, and its hit region with it — a
control clipped to `Activi…`, or to a bare `…`, is a target whose meaning a
reader cannot recover but whose click still fires. A section heading drops its
action first, then its count, then truncates its name: the heading's job is
naming what the rows beneath it are, and the panel header already offers the
same create action the section `+` does.

At 18 columns the global sort control is now absent rather than mangled, and
`Needs Attention` keeps its name instead of becoming `Needs Attention …`.

### 4. The two header controls do not look like the same kind of thing

Project renders `New` as a button pill via `styles.Button`. Global rendered the
sort label as muted text via `styles.Muted` — same position, same region
plumbing, but it did not read as pressable, and nothing suggested `s` cycles it.

**Fixed.** One `renderControl` gives every sidebar control the same treatment:
flat pill at rest, accent pill on hover.

### 5. The project section heading collided with the panel title

The panel is titled `Workspaces` and its second section was also titled
`Workspaces (2)`. Two meanings of one word, four rows apart.

**Fixed.** The section is now `Worktrees`, which is what its rows are. This is
an interim wording: once grouping and sorting are user-controlled the sections
stop being fixed kinds, and the heading should be revisited then.

### 5b. The main checkout was a row that answered nothing

The main worktree rendered with the same marker, glyph, and two-line grammar as
its neighbours, but it is the project's primary working directory rather than a
workspace: nothing in the list creates, deletes, merges, or pushes it, and
selecting it replaced the preview with a static explainer instead of a terminal.
It read as one more workspace that happened to be inert.

**Fixed.** It is no longer offered as a row, is no longer selectable, and no
longer draws a chip header. Diffing the main checkout belongs to the Git plugin,
which owns it.

One exception is deliberate: a main checkout that is *hosting shells* keeps its
row. That happens when Sidecar is running from inside a worktree, so the main
checkout's shells nest under its row rather than appearing in the top Shells
section — hiding the parent would take live sessions off the surface entirely.
In the ordinary case (Sidecar running in the main checkout, its shells already
in the Shells section) the row is simply gone.

The default selection needed a matching change: `selectedIdx` starts at zero,
which *is* the main checkout, so the initial selection is now clamped to the
first visible row before any preview loads.

### 6. Global loses its scope cue exactly when it matters least — and most

At 26 columns and below, the project prefix drops out of global rows entirely
(`release …`, not `sidecar release …`). This matches the plan's stated tier
policy, but it means a narrow global sidebar and a narrow project sidebar are
visually indistinguishable. It strengthens the case for the scope selector in
the header (slice 4) being the durable cue rather than the row prefix.

### 7. Section `+` buttons are already conditional, and the reasoning is sound

The project sidebar suppresses the `Workspaces` section `+` when there are no
shells above it, because the heading would then sit directly under a header
whose `New` creates the same thing. That instinct is the plan's slice 5
conclusion, arrived at locally. Removing the section `+` buttons entirely is
consistent with a decision the code has already half-made.

## Still open

### The global list still shows main checkouts

Everything above fixes the *project* sidebar's main-checkout row. The global
list has the same row, from the same inventory (`listItem` gives it the `◉`
marker), and it is equally non-actionable there. It is usually invisible because
an idle main checkout lands in `No Session`, which the `showIdleWorktrees`
toggle hides by default — but that is a coincidence of the toggle, not a
decision.

It was left alone because the trade-off is genuinely different in global: a
project with no worktrees would disappear from the list entirely, and the global
list is also how you discover that a project exists. Worth a decision before the
scope selector lands.

### The two age formatters

`formatRelativeTime` and `RelativeAge` still disagree under a minute. See
finding 1.

## Open questions for the plan

These are the points where the plan is prescriptive about implementation and the
code suggests the decision is not yet forced:

- **Does the project sidebar need `workspacelist.Model`?** The plan says grouping
  and sorting "should not be independently reimplemented in the project plugin".
  But the project plugin's list is two fixed sections over live tmux/Git records
  it owns, with nested shells under worktrees — a tree, not a flat sorted list.
  `Model` is flat and stable-ID keyed. Adopting it may cost more than the
  duplication it removes.
- **Is `internal/workspaceops` worth extracting before global create is actually
  wanted?** Slice 5 is a large refactor whose only consumer is slice 6. If
  global create is not a confirmed goal, slices 0–4 and 7 stand alone.
- **Are field tokens (`project:sidecar type:shell`) wanted?** They are the one
  part of the plan that adds a mini-language to a surface whose stated goal is
  being easier to use. The View surface may cover the same need without it.
