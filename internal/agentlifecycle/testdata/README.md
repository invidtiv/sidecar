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

### Traces whose value is an absence

Some traces are checked in to record that a provider emitted **nothing** — the
two Claude cancellation captures are the current examples, and that absence is
the single fact capping Claude below full lifecycle authority.

An absence is only as strong as the window it was watched over. "Nothing fired"
over one second and over eighteen are very different claims, and a reader cannot
tell them apart from a trace that simply stops. So a trace making an absence
claim must carry a trailing comment row naming its window:

```
# capture-window: 18s
```

`hooktrace_test.go` requires it for those traces and rejects a value
`time.ParseDuration` cannot read, so the evidence stays attached to the claim
rather than living in a session log nobody will find. Traces that only record
what did happen need no such row.

Note what these traces do **not** do. A test reading a static fixture cannot
notice that a provider's behavior changed; it fails only once a human has
re-traced and edited the file. They are fixture-integrity guards. Discovering
that a gap has closed is the requalification procedure's job, on its stated
cadence.

## Trace provenance

Captured 2026-08-30 on darwin/arm64.

| Trace | Provider | Version | Model | Outcome | Captured |
| --- | --- | --- | --- | --- | --- |
| `traces/opencode/tool-turn-with-permission.tsv` | opencode | 1.18.23 | openai/gpt-4o-mini | success | Phase A |
| `traces/opencode/session-error-turn.tsv` | opencode | 1.18.23 | google/gemini-2.5-flash | provider auth error | Phase A |
| `traces/opencode/cancelled-turn.tsv` | opencode | 1.18.25 | openai/gpt-4o-mini | user cancellation | Phase B |
| `traces/opencode/provider-error-named.tsv` | opencode | 1.18.25 | google/gemini-2.5-flash | provider auth error | Phase B |

The error trace is kept deliberately. A failed turn is a real lifecycle path,
and it is the one that shows `session.error` resolving to `session.idle` rather
than latching the pane on `working` — which is exactly the failure mode the
resolver has to survive.

The Phase B pair exists because cancellation and failure are the *same shape* on
the OpenCode bus. `cancelled-turn.tsv` is a turn interrupted by the user;
`provider-error-named.tsv` is the same harness with no credentials for the
selected model. They differ in exactly one recorded value — the bounded error
class name, `MessageAbortedError` against `ProviderAuthError` — and that is the
whole reason the second one is checked in. Without it, "an aborted message means
the user cancelled" would be an assumption rather than a measurement.

Two things about capturing a cancellation, both learned the hard way. The
OpenCode TUI needs **two** Escape presses to interrupt: the first only arms the
confirmation and changes the footer from `esc interrupt` to `esc again to
interrupt`. And the abort reaches the event bus a few seconds after the screen
stops showing the busy indicator, so tearing the session down as soon as the
screen settles records a truncated trace that looks like no events fired.

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

Cancellation cannot be produced by `opencode run`, because there is no
interactive session to interrupt. The Phase B cancellation trace therefore drove
the real TUI inside a **private tmux server** (`tmux -S` on a dedicated socket,
never the machine's default server), sent one prompt, waited for
`session.status {"type":"busy"}`, and then sent Escape twice. The tmux server
was killed at the end of the run and nothing outside the temporary tree was
touched.

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
`session-id present`, `parent-id present`, and — on Phase B traces only — the
bounded `error class name`. The Phase A files predate error-name capture and
have seven columns; readers must tolerate both widths, which `readTrace` in
`capability_test.go` does.

The error column records a class name such as `MessageAbortedError`, truncated
to 64 bytes. It never carries the error message, stack, or any provider payload
text. A class name is a closed vocabulary chosen by the provider's own source,
which is what makes it safe to record where a message would not be.

Phase B traces additionally drop `message.part.delta`. The TUI streams one of
those per token, so a single cancelled turn produced 1604 of them; they carry no
lifecycle information and would bury the fixture.

## Re-capturing

There is no `-update` flag. These are hand-captured real traces against a paid
provider, and regenerating one should be a deliberate act with a recorded
version, not a side effect of running the suite. To add a provider or refresh a
version, repeat the procedure above and update both the table here and
`capabilities.json`.
