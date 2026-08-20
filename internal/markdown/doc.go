// Package markdown wraps Glamour for cached markdown-to-ANSI rendering with
// width-based revalidation, falling back to plain text wrapping for narrow
// terminals.
//
// RenderContent returns styled lines for existing document/issue/preview
// consumers. RenderMapped is an adjacent result: the same lines plus source
// anchors derived from goldmark block positions and wrap math, so a click or
// scroll offset can be mapped back to a source line without parsing ANSI.
//
// This package is the single owner of the Sidecar-palette-to-Glamour mapping
// (see theme.go). Markdown colors are derived from the normalized semantic
// palette, fenced code uses the palette's SyntaxTheme, and markdownTheme
// selects the structural dark/light preset — or, for any other value, an
// explicit externally owned full-style override. Consumers that keep their own
// render or layout caches must include Renderer.StyleKey in their cache key;
// they must not read palette fields or build Markdown styles themselves.
package markdown
