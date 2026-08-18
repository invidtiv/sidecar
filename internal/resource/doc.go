// Package resource holds the presentation-neutral resource document a terminal
// resource provider returns, and the validation and sanitization Sidecar runs
// before any provider-supplied byte reaches view state.
//
// Nothing here knows about Jira, HTTP, subprocesses, or the protocol envelope.
// The wire shapes in wire.go are the JSON the protocol document specifies; the
// domain types are what the rest of Sidecar is allowed to see. Every path from
// wire to domain goes through Sanitize*, so a caller cannot accidentally hand
// unvalidated provider text to a renderer.
//
// See docs/reference/terminal-resource-provider-protocol.md.
package resource
