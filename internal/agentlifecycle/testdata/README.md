# Agent lifecycle Phase A evidence

## What is in here

`capabilities.json` is the machine-readable capability matrix: one entry per
provider integration, carrying the tier Phase A evidence justifies, which
transitions were actually covered, and how the evidence was obtained.
`capability_test.go` loads it and re-derives each tier from the recorded
coverage, so the file cannot claim an authority its own evidence does not
support.

`traces/<provider>/*.tsv` are sanitized real event traces. They are the
difference between a documented contract and a proved one, and the tier in
`capabilities.json` is only allowed to say `real-trace` for a provider that has
files here.

The prose companion, including the per-provider gap analysis and the
steel-thread decision, is `docs/reference/agent-lifecycle-capability-matrix.md`.

## Trace provenance

Captured 2026-08-30 on darwin/arm64.

| Trace | Provider | Version | Model | Outcome |
| --- | --- | --- | --- | --- |
| `traces/opencode/tool-turn-with-permission.tsv` | opencode | 1.18.23 | openai/gpt-4o-mini | success |
| `traces/opencode/session-error-turn.tsv` | opencode | 1.18.23 | google/gemini-2.5-flash | provider auth error |

The error trace is kept deliberately. A failed turn is a real lifecycle path,
and it is the one that shows `session.error` resolving to `session.idle` rather
than latching the pane on `working` — which is exactly the failure mode the
resolver has to survive.

## How they were captured, and what was not touched

A trace plugin was installed into a **temporary** `XDG_CONFIG_HOME` created
under the run's scratch directory. The user's real `~/.config/opencode` — which
contains their own plugins — was never read, written, copied, or moved. No
provider configuration outside the temporary tree was created or modified.
`~/.local/share/opencode` was left alone as well; it supplied credentials to the
provider as it normally would, and nothing was written back to it by this
harness.

The provider was driven with `opencode run`, one short turn per trace, with the
cheapest available model. The permission event pair was produced by setting
`"permission": {"bash": "ask"}` in the temporary config and asking for a single
`echo`.

## Sanitization

Sanitization is by construction rather than by redaction, which is the only
version worth trusting: the trace plugin never had the content in the first
place. It recorded event and hook **names**, the `session.status` discriminator,
the tool name, and booleans for whether an identity field was present. It never
recorded prompt text, response text, tool arguments, tool results, file paths,
session identifiers, credentials, or environment values.

The checked-in `.tsv` additionally replaces wall-clock time with a millisecond
offset from the first event, so the fixture is stable across runs, and drops
startup catalog chatter (`plugin.added`, `catalog.updated`, `integration.updated`,
`reference.updated`) that says nothing about lifecycle.

Columns are: `offset_ms`, `kind` (`bus` or `hook`), `type`, `status`, `tool`,
`session-id present`, `parent-id present`.

## Re-capturing

There is no `-update` flag. These are hand-captured real traces against a paid
provider, and regenerating one should be a deliberate act with a recorded
version, not a side effect of running the suite. To add a provider or refresh a
version, repeat the procedure above and update both the table here and
`capabilities.json`.
