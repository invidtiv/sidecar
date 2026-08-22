# Theme-aware Markdown rendering

**Status:** ready to implement

**Scope:** every Sidecar-owned Glamour Markdown surface, including Files, Notes, Conversations, project/global workspace panes, provider resources, and release notes.

## Outcome

Markdown should look native to the active Sidecar theme. Switching a built-in, community, project, or overridden theme must immediately restyle Markdown that is already on screen, without reopening a file, resizing a pane, or discarding the user's scroll, selection, tab, or search state.

One shared renderer owns the mapping from Sidecar's normalized palette to Glamour. Fenced code blocks use the same Chroma style as raw file previews and git syntax highlighting. Consumers may cache layout or rendered content, but they must all use the shared renderer's theme identity as a cache dependency; no plugin gets its own Markdown color table.

## Current state

Theme support exists but does not provide palette parity:

- `internal/markdown.Renderer` is already the common Glamour wrapper used by most Sidecar surfaces.
- It passes `styles.GetMarkdownTheme()` to `glamour.WithStylePath`. All 21 curated dark themes currently select Glamour's generic `dark` preset, so their Markdown headings, links, inline code, rules, and fenced-code tokens do not come from the active Sidecar palette.
- The renderer is recreated only when width changes. Its content and mapped render cache keys include content and width, but not the active theme. An existing renderer can therefore retain the previous theme indefinitely.
- Several consumers add a second cache above the shared renderer. Files caches `markdownRendered`; Notes caches `viewSurface`; Conversations caches rendered messages; `docview`, `issueview`, and `resourceview` cache their own rows or bodies. Fixing only the renderer would leave some already-visible content stale after live theme preview or project switching.
- `msg.ThemeChangedMsg` is synchronously broadcast to project plugins, but the global Sessions/Workspaces browser is not a plugin. Cache correctness cannot rely only on that message.
- Sidecar's embedded td monitor uses td's separate public palette adapter and already consumes live theme changes. Keep that ownership boundary and verify it alongside this work; do not make td import Sidecar's Markdown renderer.

### Rendering inventory

| User-visible surface | Rendering path | Extra cache to make theme-aware |
| --- | --- | --- |
| Files rendered Markdown preview | `internal/plugins/filebrowser` → `internal/markdown` | `markdownRendered` and search lines derived from it |
| Notes view mode | `internal/plugins/notes` → `RenderMapped` | `viewSurface` plus source/width/mode key |
| Conversation message bodies | `internal/plugins/conversations` → `RenderContent` | message `renderCache` |
| Project Workspace document panes | `internal/plugins/workspace` → `docview` | document render/layout cache |
| Global Sessions/Workspaces document panes | `internal/overview` → `docview` | same shared document cache; no plugin theme event |
| Project/global issue panes | workspace/overview → `issueview` | built row cache for description and acceptance Markdown |
| Project/global provider-resource panes | workspace/overview → `resourceview` | rendered body cache |
| Update release-notes modal | `internal/app/update_modal.go` → `internal/markdown` | ephemeral; render under the current snapshot |
| Embedded td descriptions | `internal/plugins/tdmonitor` → td renderer | td-owned; verify existing adapter/live update only |

`internal/resource.RenderSafeMarkdown` remains the sanitizing wrapper for provider text before it enters the same shared renderer. `RenderMapped` remains the Notes source-anchor layer over `RenderContent`; neither should introduce a second visual style.

## Design

### 1. Derive one Glamour style from the normalized Sidecar palette

Add the theme-to-Glamour builder inside `internal/markdown` (for example, `theme.go`). It accepts an immutable `styles.Theme`/`ColorPalette` snapshot and returns both an `ansi.StyleConfig` and a stable style key. `Renderer` is the only production caller that turns this mapping into a `glamour.TermRenderer`.

When `MarkdownTheme` is `dark` or `light`, start from that Glamour preset only for structural choices—margins, prefixes, list glyphs, and task markers—then overwrite every color-bearing Markdown role from the normalized Sidecar palette:

| Markdown role | Sidecar source |
| --- | --- |
| document and paragraph text | `TextPrimary` |
| secondary heading levels and image/definition metadata | `TextSecondary` / `TextMuted` |
| primary heading and list/task markers | `Primary` |
| heading accents and inline code | `Accent` |
| links and link text | `Link` |
| block-quote marker/text | `Secondary` / `TextMuted` |
| rules and table separators | `BorderMuted` / `BorderNormal` |
| inline-code background | `BgSecondary` |
| code-block syntax | `SyntaxTheme` |

Avoid a background on the whole Markdown document so pane chrome and selection can continue to own the canvas. Use backgrounds only where the content conveys one: inline code and the Chroma style's code-block background.

For fenced code, clear the base Glamour preset's embedded `CodeBlock.Chroma` table and set `CodeBlock.Theme` to the active `SyntaxTheme`. Glamour gives its inline Chroma table precedence over `Theme`; failing to clear it would retain the generic dark/light code palette even after the field is set. Validate that the selected Chroma style exists and fall back deterministically to `github` for light palettes or `monokai` for dark palettes when an invalid user override is encountered.

Preserve explicit nonstandard `markdownTheme` style files/presets as an advanced full-style override rather than silently recoloring them. Document that `dark` and `light` are palette-derived Sidecar modes, while another value opts into an externally owned Glamour style. This retains current custom-config compatibility without allowing the default path to drift.

### 2. Keep the palette schema small

Do not add Markdown-specific color fields to `ColorPalette` in the first implementation. The normalized palette already has semantic roles for text, accent, links, borders, surfaces, and syntax. Reusing them makes all curated themes, community conversions, project themes, and ordinary color overrides work without maintaining 21 parallel Markdown palettes.

Audit every curated theme and community conversion to ensure these existing inputs are populated and valid:

- `TextPrimary`, `TextSecondary`, `TextMuted`
- `Primary`, `Secondary`, `Accent`, `Link`
- `BgPrimary`, `BgSecondary`, `BorderNormal`, `BorderMuted`
- `SyntaxTheme` and the dark/light structural `MarkdownTheme`

Update a theme only when that audit finds a missing/invalid semantic value or unknown Chroma style. Do not add a new slot merely to tune one theme. If visual proof demonstrates that an existing semantic role cannot express a necessary Markdown distinction, add the smallest role to `ColorPalette`, normalization, override parsing, community conversion, documentation, and every curated theme in one change; that is an evidence-triggered follow-up, not the default design.

### 3. Make renderer caching theme-correct

The style key must cover every effective input to the generated Glamour style, including normalized palette values, `SyntaxTheme`, `MarkdownTheme`, and the resolved contents of any explicit full-style file. Do not use only the theme name or override path: project/user overrides can change colors while retaining the same name, and a style file can change at the same path.

Change `internal/markdown.Renderer` so each render captures one current theme snapshot and:

1. includes its style key in both `RenderContent` and `RenderMapped` cache keys;
2. recreates the underlying Glamour renderer when width **or style key** changes;
3. clears both internal caches when the renderer changes;
4. exposes the current style key/revision through a small read-only method for consumers that maintain outer caches.

Snapshot the theme once per operation so a concurrent preview cannot mix one palette's cache key with another palette's style. Keep the existing renderer mutex and bounded-cache behavior. Narrow-width plain wrapping has no visual theme state, but using the same key is acceptable and keeps the contract simple.

### 4. Invalidate every outer cache without duplicating style logic

Each outer cache records only the shared renderer style key; it does not inspect palette fields or build styles.

- **Files:** on `ThemeChangedMsg`, clear/re-render `markdownRendered` when the current file is in rendered Markdown mode, then recompute active content search matches because rendered row indices can change. Preserve preview scroll and selection where rows remain valid; clamp them if rendering changes row count.
- **Notes:** add the renderer style key to `ensureViewSurface`'s cache dependencies. A live theme change rebuilds `RenderMapped` while preserving source-anchored cursor, scroll, and selection through the existing mapping restoration path.
- **Conversations:** add style key to `renderCacheKey`, or clear the message render cache on `ThemeChangedMsg`. Prefer the key because it is also correct outside the project-plugin broadcast. Do not reload or reparse sessions.
- **Document, issue, and resource viewers:** add style key to their existing render/layout/body cache dependencies. This automatically covers both the project Workspace and global Sessions/Workspaces projections, including inactive tabs when they next render.
- **Release notes:** no persistent outer cache is present; assert that a newly opened modal uses the current theme snapshot.

Do not recreate plugins, pane models, tabs, watchers, polling loops, or resource resolvers on a theme change. This is presentation invalidation only.

## Implementation sequence

### Phase 1 — Shared style steel thread in Files

- Add the palette-derived Glamour style builder and style-key calculation in `internal/markdown`.
- Teach `Renderer` to recreate and invalidate on width or theme change.
- Set fenced-code `CodeBlock.Theme` from `SyntaxTheme` after removing the base preset's inline Chroma table.
- Update Files' rendered-preview cache for live theme changes. Prove a Markdown fixture containing headings, links, inline code, block quotes, lists, a table, and fenced code changes from one unmistakable theme to another without reopening or resizing the file.

This is the affected-journey steel thread: `#` theme preview → already-open Files Markdown preview → immediate palette-correct repaint.

### Phase 2 — Complete shared-consumer cache parity

- Add the shared style key to Notes and Conversations cache dependencies.
- Add it to `docview`, `issueview`, and `resourceview` render dependencies so both project and global workspace panes inherit the behavior.
- Cover `RenderMapped` and sanitized provider Markdown through their existing shared paths; do not add plugin-specific renderers.
- Verify the update modal uses the new shared style automatically.
- Audit for direct Glamour construction or Markdown-to-ANSI code outside `internal/markdown`; migrate Sidecar-owned rendering or document why an external component owns it.

### Phase 3 — Theme audit, documentation, and real-app proof

- Validate all curated themes' semantic inputs and Chroma style names.
- Test representative dark, light community, project-scoped, and overridden palettes. Update only themes with missing or invalid existing inputs.
- Update the active theme documentation and palette reference to explain that Markdown colors derive from semantic palette roles, fenced code uses `syntaxTheme`, and `markdownTheme` controls structural preset/explicit override behavior.
- Verify the embedded td tab still responds to live theme preview through its own adapter and does not acquire a dependency on `internal/markdown`.
- Run focused tests, full Go gates, independent review, and the isolated real Sidecar journey below.

## Verification

### Focused automated tests

- Builder tests assert every color-bearing Glamour role comes from an unmistakable normalized test palette, and that `CodeBlock.Chroma == nil` and `CodeBlock.Theme == SyntaxTheme`.
- Renderer tests render the same content and width under theme A then theme B using the same `Renderer`; output and style key must change without a resize. Switching back must not return theme B's cached ANSI.
- Override tests prove two palettes with the same theme name but different overrides produce different keys and output.
- Chroma tests cover a known theme, Sidecar Modern's registered custom theme, and invalid dark/light overrides with deterministic fallback.
- Concurrency tests race rendering with theme snapshots and assert each result is internally from one palette, with no stale cache reuse.
- Consumer tests cover Files search-row refresh, Notes mapped-view retention, Conversations message cache invalidation, and document/issue/resource cache invalidation in both project and global hosts.
- The all-theme audit fails on missing semantic inputs, unknown syntax themes, or invalid dark/light structural modes for curated themes.

Run at minimum:

```bash
go test ./internal/markdown ./internal/plugins/filebrowser ./internal/plugins/notes ./internal/plugins/conversations
go test ./internal/docview ./internal/issueview ./internal/resourceview ./internal/plugins/workspace ./internal/overview ./internal/app
go test ./internal/styles ./internal/community
go test -race ./internal/markdown
go test ./...
go vet ./...
go build ./...
```

### Real Sidecar journey

Use `scripts/tmux-drive.sh` only after `paths` confirms both the tmux server and Sidecar state/config tree are isolated. Never touch the default tmux server.

1. Open a checked-in Markdown fixture in Files and enable rendered mode.
2. Capture ANSI-aware/PNG evidence under Sidecar Modern.
3. Open `#`, preview a visually distinct curated theme, a light community theme, and a same-name theme with color overrides. The already-open document must repaint immediately each time.
4. Confirm fenced code matches raw syntax highlighting for the same language, while headings, links, inline code, quotes, rules, and tables use the active Sidecar palette.
5. Cancel preview and confirm the original theme returns without reopening, resizing, or losing scroll/search/selection state.
6. Repeat the live switch with an already-open Note, Conversation, project document/issue/resource pane, global Sessions document/issue/resource pane, update release notes, and embedded td description.
7. Switch between projects with different themes and confirm inactive panes render with the new effective project theme when revisited.
8. Stop the isolated driver on success or error.

Plain `capture-pane` text is insufficient color evidence; retain a PNG or ANSI capture for the review.

## Acceptance criteria

- Every Sidecar-owned Markdown surface uses the one `internal/markdown` palette-to-Glamour implementation.
- Markdown body, headings, links, inline code, quotes, rules, and tables derive from the effective normalized Sidecar palette.
- Fenced code uses the same effective Chroma style as other Sidecar syntax highlighting.
- Live preview, cancel, confirm, project switch, community theme, and same-name overrides cannot return stale cached Markdown ANSI.
- Existing user state—scroll, selection, search, active pane/tab, expanded conversation messages, watchers, and polling—survives presentation changes.
- All curated themes and community conversion paths supply valid existing inputs; any necessary theme edits are covered by all-theme validation.
- No new Markdown-specific palette family is introduced without visual evidence that existing semantic roles are insufficient.
- Focused tests, race coverage, full Go gates, isolated real-app proof, and independent review pass.

## Out of scope

- Redesigning Markdown layout, glyphs, wrapping, source mapping, or selection behavior beyond what theme-triggered rerendering requires.
- Making Sidecar the owner of td's renderer or other embedded dependencies.
- Creating bespoke Markdown palettes for each theme when existing semantic colors express the required roles.
