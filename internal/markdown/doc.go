// Package markdown wraps Glamour for cached markdown-to-ANSI rendering with
// width-based revalidation, falling back to plain text wrapping for narrow
// terminals.
//
// RenderContent returns styled lines for existing document/issue/preview
// consumers. RenderMapped is an adjacent result: the same lines plus source
// anchors derived from goldmark block positions and wrap math, so a click or
// scroll offset can be mapped back to a source line without parsing ANSI.
package markdown
