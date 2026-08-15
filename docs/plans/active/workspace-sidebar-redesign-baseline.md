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

## Findings from the capture

### 1. The project row has no age, and the global row has no manual position

Project rows render `marker · kind · name` on line one with nothing
right-aligned. Global rows render `marker · kind · project · name · age`. Age is
the plan's field 6 and it simply does not exist project-side. This is the
largest single row-grammar gap, and it is additive rather than contested.

### 2. A plain shell always costs two rows even when its second line is empty

`RenderRow` returns two physical lines for any row at or above `twoLineWidth`,
including when line two renders empty. The doc comment says line two "collapses
to nothing when there is none" — it collapses to empty *content*, not to zero
*rows*.

Visible in the golden at 56 columns: `◎ ❯ scratch` is followed by a blank line
that belongs to the row, then the section separator blank. On the global side
the same waste pushes the entire `No Session (1)` section off an 18-row panel
while two blank lines sit on screen above it.

This is a real density cost in the surface whose whole job is finding the right
workspace, and the plan already asks for the correct behaviour ("Plain shells
omit the empty second line unless another fact exists"). It is a good early win
in slice 2.

### 3. Narrow widths produce controls that cannot be read or reached

At 18 columns:

- the project `Workspaces` section's `+` truncates to a bare `…`, leaving a
  clickable target with no legible meaning;
- the section heading loses its count (`Needs Attention …`);
- the global sort control truncates to `Activi…`.

Nothing degrades in a defined order today; the header just gets clipped from the
right. The plan's fixed elision priority should apply to chrome as well as rows.

### 4. The two header controls do not look like the same kind of thing

Project renders `New` as a button pill via `styles.Button`. Global renders the
sort label as muted text via `styles.Muted` — same position, same region
plumbing, but it does not read as pressable, and there is nothing to suggest `s`
cycles it. The plan's "one shared style: flat at rest, accent on hover" is a
small change with a large legibility payoff.

### 5. The project section heading collides with the panel title

The panel is titled `Workspaces` and its second section is also titled
`Workspaces (2)`. Two different meanings of the same word, four rows apart. The
plan's move to `Group by: Kind` grouping is an opportunity to word this once.

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

## Suggested reordering of the plan's slices

Findings 2, 3, and 4 are small, self-contained, independently reviewable, and
improve the surface for every user immediately — no new concepts, no lifecycle
risk, no persistence schema. They currently sit inside slices 1 and 2 alongside
much larger structural work.

Worth considering as a first shippable increment before the plan's larger
structure lands:

1. collapse the empty second line;
2. give header/section chrome a defined degradation order;
3. give both header controls one button style;
4. add age to the project row.

That is a visible improvement to both sidebars with none of the plan's
architectural commitments, and it would let the bigger slices be judged against
an already-tidier baseline.

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
