# Terminal resource provider protocol

**Status:** draft (not frozen) — see [the plan](../plans/active/terminal-resource-providers.md)
**Protocol identifier:** `sidecar.terminal-resource/v1`

A terminal resource provider is an explicitly configured local executable that
teaches Sidecar to recognize a resource key in terminal output and to turn that
key into a typed, read-only document. Sidecar owns matching, link safety, the
pane, and rendering. The provider owns service-specific rules, authentication,
and network access.

This document is the contract. It is language-agnostic: any executable that can
read one JSON object from stdin and write one JSON object to stdout can be a
provider.

## Invocation model

Sidecar runs the configured argv directly — no shell, no `PATH` interpolation of
arguments — and:

1. writes exactly one JSON request object to the child's stdin, then closes it;
2. reads stdout to completion, expecting exactly one JSON object;
3. drains stderr concurrently into a bounded sink that is counted and discarded;
4. waits for the child and reaps it.

A valid typed success **or** typed failure response exits `0`. Any of the
following is a *transport* failure attributed to the provider, not to the
service:

- non-zero exit;
- malformed JSON, no JSON, or more than one top-level JSON value on stdout;
- stdout exceeding the response byte limit;
- exceeding the configured timeout;
- a missing or mismatched `protocol` field.

Every invocation runs in its own process group. On timeout or cancellation
Sidecar kills the group — so forked descendants die with it — drains the
remaining bounded output, and waits. Sidecar never signals a process outside the
group it created.

Providers are short-lived in v1. There is no handshake, no framing, no
long-running server, and no request multiplexing.

### Execution environment

- **Working directory:** a neutral Sidecar config directory. Never the selected
  repository.
- **Environment:** only `PATH`, `HOME`, `TMPDIR`, locale variables (`LANG`,
  `LC_*`), XDG config/cache/state variables, and the documented proxy/CA
  variables `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY`, `SSL_CERT_FILE`,
  `SSL_CERT_DIR`, `GIT_SSL_CAINFO` when present in Sidecar's own environment,
  plus any variables named in the instance's `passEnv`. Nothing else is
  inherited. Sidecar never accepts inline secret values in configuration and
  never logs or renders a passed value.
- **stdin:** exactly one JSON object, then EOF.
- **stdout:** exactly one JSON object. Nothing else — not a banner, not a
  progress line, not a trailing log.
- **stderr:** free-form. Sidecar drains it boundedly and records only a byte
  count. It is never surfaced in a pane, a toast, a log, a diagnostic, or a
  crash report. Provider authors reproduce failures by running the provider CLI
  or `sidecar terminal-links check` deliberately.

### Methods

| Method | When it runs | May do network I/O |
| --- | --- | --- |
| `describe` | asynchronously after Sidecar's first ready frame, and whenever provider configuration changes or the user rechecks | no |
| `resolve` | on click, explicit refresh, `sidecar open --provider`, or `terminal-links check --resolve` | yes |

Matching itself never starts a process and never performs I/O.

## `describe`

Reports what the provider is and what resource keys it can recognize.
`describe` must be local, fast, and non-interactive. It may read the provider's
own configuration to build instance-specific patterns. It must not prompt for a
credential or contact the network.

### Request

```json
{
  "protocol": "sidecar.terminal-resource/v1",
  "method": "describe",
  "instance": "jira-work",
  "host": {"name": "sidecar", "version": "0.0.0"}
}
```

`instance` is the ID from Sidecar configuration. It is the authoritative
identity of this provider instance; the provider cannot rename itself.

### Response

```json
{
  "protocol": "sidecar.terminal-resource/v1",
  "provider": {
    "kind": "jira",
    "name": "Jira",
    "version": "1.2.0",
    "docsUrl": "https://example.test/sidecar-jira/setup"
  },
  "matchers": [
    {
      "id": "issue-key",
      "pattern": "\\b(?:CASH|GRES|AVATAXUI)-[1-9][0-9]*\\b",
      "priority": 100
    }
  ]
}
```

- `provider.kind` and `provider.name` are informational display strings. They
  cannot rename or collide with a configured instance ID.
- `provider.docsUrl` is optional. It must pass the same `http`/`https`
  validation as `sourceUrl` and is the only executable-declared Setup action
  Sidecar will follow — and only after user confirmation.
- `matchers[].id` is stable and unique within the provider. It is stored in
  persisted resource references, so changing it orphans saved tabs.
- `matchers[].pattern` is a Go/RE2 regular expression. The **whole match** is
  the locator; there are no capture-group templates and no provider code runs in
  the scanner.
- `matchers[].priority` is optional (default `0`). Higher runs earlier within a
  provider.

`describe` may return an `error` object instead (see below) — for example
`invalid_config` when the provider has not been set up yet.

### Matcher rules enforced by Sidecar

- RE2 syntax only, guaranteeing linear-time matching. A pattern that fails to
  compile is rejected and the whole `describe` result is refused.
- Matching is case-sensitive unless the pattern opts into RE2 flags.
- Built-in matchers (URL, file, td issue, git diff) keep precedence. External
  matchers run afterward in ascending configured-provider order, then descending
  priority, then matcher ID.
- Overlaps are resolved first-wins through the same visual-column overlap
  function the existing scanner uses.
- Pattern count, pattern length, matches per line, locator length, and total
  provider count are all bounded. See "Limits".

Sidecar validates and compiles the whole matcher set before publishing a new
immutable snapshot. A failed replacement keeps the last valid snapshot for the
remainder of the process and reports the new failure; relaunch starts clean.

## `resolve`

Turns one locator into one document.

### Request

```json
{
  "protocol": "sidecar.terminal-resource/v1",
  "method": "resolve",
  "instance": "jira-work",
  "params": {
    "matcher": "issue-key",
    "locator": "CASH-1245"
  }
}
```

The request deliberately carries no terminal line, no scrollback, no tmux
target, no environment, and no repository path. Widening it requires a named
capability and an explicit per-instance permission, not a silent field addition.

### Success response

```json
{
  "protocol": "sidecar.terminal-resource/v1",
  "resource": {
    "identity": "CASH-1245",
    "title": "Refund totals differ after partial capture",
    "subtitle": "Bug",
    "status": {"label": "IN PROGRESS", "tone": "info"},
    "fields": [
      {"label": "Assignee", "value": "Marcus"},
      {"label": "Priority", "value": "High"},
      {"label": "Updated", "value": "2026-08-17T17:31:00Z"}
    ],
    "body": {"format": "markdown", "text": "Ticket description..."},
    "sourceUrl": "https://jira.example.test/browse/CASH-1245",
    "updatedAt": "2026-08-17T17:31:00Z",
    "freshForSeconds": 60
  }
}
```

| Field | Required | Meaning |
| --- | --- | --- |
| `identity` | yes | Provider-stable canonical ID. If it differs from the locator, Sidecar re-keys the tab and merges it with an already-open canonical tab. |
| `title` | yes | Primary display line. |
| `subtitle` | no | Secondary line, e.g. resource type. |
| `status` | no | `{label, tone}`; `tone` is one of `neutral`, `info`, `success`, `warning`, `danger`. Unknown tones coerce to `neutral`. |
| `fields` | no | Ordered `{label, value}` pairs rendered as a bounded grid. |
| `body` | no | `{format, text}`; `format` is `markdown` or `text`. Unknown formats coerce to `text`. |
| `sourceUrl` | no | `http`/`https` only. The single action that can open a URL in v1. |
| `updatedAt` | no | RFC 3339. Unparseable values are dropped, not an error. |
| `freshForSeconds` | no | Provider's freshness hint. Sidecar clamps it. |

A response never changes its own provider instance.

### Typed failure response

```json
{
  "protocol": "sidecar.terminal-resource/v1",
  "error": {
    "code": "unauthorized",
    "message": "Jira credentials are missing or expired",
    "retryable": false,
    "setupHint": "Run sidecar-jira configure --profile work"
  }
}
```

Stable v1 codes:

| Code | Meaning | Typical `retryable` |
| --- | --- | --- |
| `not_found` | The locator does not exist or is not visible | `false` |
| `unauthorized` | Missing, expired, or rejected credentials | `false` |
| `forbidden` | Authenticated but not permitted | `false` |
| `rate_limited` | Throttled upstream | `true` |
| `invalid_config` | The provider is not configured correctly | `false` |
| `unavailable` | Network or upstream service failure | `true` |
| `internal` | Anything else | `true` |

Unknown codes map to `internal`. `message` is display text, not control flow.
`setupHint` is displayed as **copyable text only** — Sidecar never executes it.

## Limits

Sidecar enforces these before any provider data reaches view state. They are
defaults; the exact numbers live in `internal/resource` and may be tuned, but a
provider must not assume anything larger.

| Bound | Default |
| --- | --- |
| Response bytes (stdout) | 256 KiB |
| Body text bytes | 64 KiB |
| Field count | 24 |
| Field label / value length | 64 / 512 chars |
| Title / subtitle length | 300 / 120 chars |
| Identity / locator length | 200 chars |
| URL length | 2048 chars |
| Matchers per provider | 32 |
| Pattern length | 512 chars |
| Matches per terminal line | 32 |
| Configured providers | 16 |
| `describe` timeout | 5s |
| `resolve` timeout | 10s (configurable, clamped to 60s) |
| stderr drained before discard | 8 KiB |

All strings must be valid UTF-8. Invalid sequences are replaced. C0/C1 control
characters other than `\n` and `\t` in body text are stripped; all control
characters are stripped from single-line fields.

Unknown JSON fields are ignored for forward compatibility. Unknown *methods* in
a request must return an `internal` error rather than crashing.

## Safety posture

- Provider text never becomes ANSI. Sidecar strips OSC from provider strings the
  same way it strips OSC from terminal text.
- The shared Markdown renderer is **not** a trust boundary: parsed Markdown
  links can synthesize OSC-8 hyperlinks even when the input bytes contain no
  escapes. Before rendering, a resource-specific sanitizer drops raw HTML,
  reduces images to inert alt text, and rewrites links and autolinks to plain
  visible text with no destination. After rendering, all OSC is stripped again
  as defense in depth.
- The separately typed and validated `sourceUrl` is the only resource action
  that can open a URL in v1.
- A process boundary is crash isolation, **not a sandbox**. Enabling a provider
  trusts that executable with the user's full OS privileges. Sidecar never
  discovers, installs, auto-enables, or upgrades a provider, and a repository
  can never cause a provider to run merely by being opened.
- Debug logs record provider instance, method, duration, outcome code, and byte
  counts. Never the locator, title, body, URL, credentials, stdout, or stderr.

## Configuration

Providers are configured explicitly in Sidecar's app-level config:

```json
{
  "terminalResources": {
    "providers": [
      {
        "id": "jira-work",
        "command": ["sidecar-jira", "sidecar-provider", "--profile", "work"],
        "passEnv": ["JIRA_API_TOKEN"],
        "enabled": true,
        "timeout": "10s"
      }
    ]
  }
}
```

- `id` is unique and stable; it is the persisted provider key.
- `command` is an argv array executed without a shell. The first element may be
  an absolute path or resolve through `PATH`.
- `passEnv` names variables whose *current values* are inherited. Inline secret
  values are not supported.
- Array order is matcher precedence.

## Headless verification

```bash
sidecar terminal-links list --json
sidecar terminal-links check jira-work --json
sidecar terminal-links check jira-work --resolve CASH-1245 --json
```

`list` and the default `check` perform configuration, command-resolution, and
`describe` checks only. `--resolve` is explicit because it can perform network
access and print private resource data. These commands dispatch before any TUI,
tmux, state, or log setup.

## Fixtures

Canonical request/response JSON lives in
`internal/resourceprovider/testdata/protocol/`. The reference fixture executable
is `internal/resourceprovider/testdata/fixtureprovider`, which describes
`CASH|GRES|AVATAXUI` and resolves deterministic synthetic documents with no
network access and no credentials.
