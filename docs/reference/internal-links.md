# Sidecar internal links v1

Sidecar internal links identify application-owned navigation intents in text.
Version 1 defines one URI scheme and one registered namespace:

```text
sidecar://note/nt-4jdj4e
```

The URI identifies a stable note ID. It does not encode a title, plugin tab,
pane placement, filesystem path, shell command, or project switch. Changing a
note title does not change existing links.

## Authoring links

Use the canonical URI directly when the destination should be visible:

```text
See sidecar://note/nt-4jdj4e
```

Use an ordinary Markdown link when a human label reads better:

```markdown
[Release checklist](sidecar://note/nt-4jdj4e)
```

Renderers may represent the same labeled link temporarily with OSC 8 inside a
plugin-local frame:

```text
OSC 8 ; ; sidecar://note/nt-4jdj4e ST
Release checklist
OSC 8 ; ; ST
```

`OSC` is ESC `]` and `ST` is ESC `\`. Authors should normally write Markdown,
not terminal control bytes.

The plain URI, Markdown label, and valid OSC label all resolve to the same
identity: namespace `note`, ID `nt-4jdj4e`.

## Canonical syntax

Version 1 follows this form:

```text
sidecar://<namespace>/<percent-encoded-id>
```

Generic bounds and syntax:

- The scheme is exactly lowercase `sidecar`.
- The namespace is singular lowercase ASCII, starts with a letter, may contain
  lowercase letters, digits, and interior hyphens, and is at most 32 bytes.
- There is exactly one non-empty path segment. Percent encoding is decoded
  once before namespace validation.
- A URI is at most 2,048 bytes; its decoded ID is at most 512 Unicode code
  points before the namespace applies its narrower rules.
- Credentials, ports, fragments, controls, invalid UTF-8, encoded separators,
  absolute paths, backslashes, and additional path segments are rejected.
- Query parameters are rejected unless a future namespace explicitly
  allowlists bounded keys. The `note` namespace accepts none in v1.

The `note` namespace accepts `nt-` followed by 1–64 lowercase ASCII letters or
digits. Sidecar validates that shape before decoration and checks the current
project's note store before moving focus.

## Activation behavior

Activating a valid note link is a two-step, read-only check:

1. The app registry validates the URI and broadcasts the stable note ID with
   the current project root.
2. Notes verifies that the note exists and is not deleted, reloads the active
   or archived list that owns it, selects the ID, and only then focuses Notes.

Version 1 navigates the existing Notes surface. It does not create a Note pane
in the surrounding content deck. A future viewer may change presentation
without changing stored URIs.

If the note is missing, deleted, belongs to another project, or disappears
between verification and list reload, Sidecar leaves the current surface and
focus unchanged and reports a bounded refusal. Stale results from a previous
project or a superseded click are ignored.

## Rendering and security

Internal destinations are never delegated to the host terminal, operating
system, shell, browser, or an arbitrary plugin callback.

Only plugin-declared read-only text rectangles are scanned. For Notes this is
the exact rendered preview body. Built-in editing, search, task/delete/info
modals, inline tmux editing and its exit confirmation, loading, and error states
return no link surface.

Sidecar validates explicit OSC 8 destinations, converts accepted labels to its
own generation-scoped hit regions, and removes the OSC open/close controls from
the composed frame before terminal output. Malformed, unterminated, unknown,
oversize, or unregistered destinations remain visible text but inert. An
explicit valid label wins over automatic matching inside the same cells.

HTTP and HTTPS links continue through their existing validation and browser
action. Other schemes are not internal intents.

## Registry and compatibility

Namespaces are assembled in a static in-process registry. Duplicate or invalid
namespace registration fails assembly tests instead of selecting a handler by
registration order. Unknown namespaces are not decorated and cannot activate.

Version 1 compatibility is identity-based:

- Display labels and note titles are not part of identity.
- Stored canonical links remain valid across title and layout changes.
- The scheme and namespace are case-sensitive; noncanonical casing is inert.
- Unsupported query options and fragments are inert rather than interpreted
  by a newer or unrelated surface.
- Adding a namespace requires a Sidecar-owned parser, validator, activation
  message, documentation, and refusal tests. It is not a dynamic plugin API.
