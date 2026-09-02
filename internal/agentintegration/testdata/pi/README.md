# Pi fixtures

These are **not traces**. They are event fixtures translated from Herdr's own
`herdr-agent-state.test.ts` (vendored at `internal/agentintegration/upstream/`),
which drives Pi's extension handlers directly with a fake `pi` object rather
than recording a real session.

Captured traces live in `internal/agentlifecycle/testdata/traces/<provider>/`,
they are sanitized recordings of a real provider at a recorded version, and they
are the only thing that may back an `evidence: real-trace` claim in
`capabilities.json`. Pi has none, which is why its capability entry says
`docs-only` and claims no more than `session-identity`. Do not move these files
there to make a tier look earned.

What they are for is equivalence: `TestBundledPiAssetBehavesLikeTheHandler`
replays each one through the shipped JavaScript under `node` and through
`PiHandler` in Go, and requires an identical ordered argv list. That is the test
that has caught real drift between an asset and its Go mirror before.

## Format

Tab separated, `#` comments ignored, nine columns:

```
offset_ms  event  reason  mode  idle  session_path  session_id  blocked_active  blocked_label
```

`-` means the field was absent. For `idle` that is a tri-state and it is
load-bearing: Pi's `ctx.isIdle?.()` can be missing, and an unknown idleness must
neither start a turn (`=== false`) nor complete one (`!== true`).

## What each fixture pins

| Fixture | Upstream case | What it asserts |
| --- | --- | --- |
| `reload-preserves-working.tsv` | "reload preserves working state when the agent is active" (:186) | A reload with `isIdle() === false` reports `working` first, not `idle`. A reload replaces the extension mid-run with no second `agent_start`. |
| `idle-only-after-settle.tsv` | "reports idle only after the agent settles" (:243) | `agent_settled` while the run is still active emits nothing; only the settlement that observes `isIdle() === true` closes the turn. |
| `rpc-session-is-ignored.tsv` | "ignores RPC sessions even when UI APIs are available" (:273) | An RPC session produces no reports at all, which is why the gate is on `mode` and not on `hasUI`. |
| `blocked-outranks-settle.tsv` | "settlement preserves explicit blocked-state precedence" (:292) | A settlement arriving while a block is outstanding does not report idle; the idle lands when the block clears. This is the only fixture that drives the blocked branch, which no released Pi can reach. |
| `session-replacement-binds-then-reports.tsv` | "reports the session replacement source" (:318) and "waits for a replacement session report before publishing state" (:353) | The binding is emitted before the first state report, and Pi's `reason` reaches Sidecar as `session_change`. |
| `agent-start-rebinds-the-session.tsv` | the `agent_start` half of :243 | Every turn re-asserts the binding, which is what recovers it after a Sidecar restart mid-session. |
| `windows-session-path-is-bound.tsv` | "OMP accepts POSIX and Windows session paths" (:232) | The upstream Pi bug this port fixes: a `C:\...` path is bound rather than discarded. |
| `session-id-only-binds-by-id.tsv` | — | With no session file, the binding falls back to Pi's session id. |
| `relative-session-path-is-discarded.tsv` | :240 | A relative path is not a session reference and is not sent. |

The upstream socket-retry case (:540) is deliberately not translated: its whole
subject is Herdr's two-attempt socket write, and Sidecar replaces that transport
with a bounded subprocess.
