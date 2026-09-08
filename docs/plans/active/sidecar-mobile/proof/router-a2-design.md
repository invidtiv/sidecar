# Mobile M1-A2 owning-host router design

**Task:** `td-762c04`. **Baseline:** integrated main `f92968a89e59ad3e59ce2f6eecb7071345952499`. **Status:** accepted implementation direction; every slice still requires focused independent code review and isolated proof before integration.

## Authority boundary

The hub routes a selected mobile protocol stream to `sidecar mobile serve --stdio` on the machine that owns the terminal. It starts that process through the registered host client's existing `SidecarCommand` SSH channel. The owner retains the durable managed-shell resolver, tmux capture and input transport, strict conditional geometry lease, live config-generation checks, presence timeout, history capture, and EOF cleanup. The hub does not construct a remote tmux manager, parse terminal frames, synthesize input or heartbeat, or fall back to a same-named local session.

The registry supplies one atomic route authority containing the current client pointer, process-local client incarnation, stable registration fingerprint, and current online mobile capability. The fingerprint covers the registered host ID, SSH target, remote binary, remote config, and ordered environment. A hub handle additionally binds the hub config generation and the exact owner `TargetIdentity` returned by the owner's mobile catalog or resolve path. Before starting the owner stream and before each forwarded target operation, the router requires the same registered client, runtime incarnation, fingerprint, online health, mobile capability, hub config generation, owner host identity, and target identity. Host removal, disablement, retargeting, client replacement, stale health, owner process restart, owner config change, server restart, pane reuse, or durable shell replacement refuses or closes the route.

The registry client incarnation prevents delayed work from an old client being accepted inside one hub process. The registration fingerprint is stable across hub processes and proves that a fresh hub still addresses the same configured owner. Neither value replaces terminal identity. A fresh owner mobile process re-resolves the client-supplied exact `TargetIdentity`; the owner remains the authority for its current config and terminal incarnation.

## Protocol and transport

The ordinary host `hello` gains an additive `mobileServeV0` verb capability. It only says that the registered Sidecar accepts the owner mobile command. The router still starts the actual owner stream and requires a valid mobile `hello` with the expected protocol and capabilities before forwarding a resolve or reconnect. A generic Sidecar version, a cached host snapshot, or a stale-permitting one-shot invocation is never treated as mobile authority.

The public native protocol remains unchanged. The hub's catalog composes its local owner catalog with current remote owner catalog snapshots and applies the shared A1 sorting, filtering, and grouping rules. Remote ready rows come from the owning mobile service, including the exact expected target identity; host observations alone remain descriptive. Unavailable, disabled, connecting, stale, incompatible, and failed owners stay explicit rows and never carry attach authority.

Once a resolve or reconnect selects an owner, the router binds random hub target and attachment handles to the corresponding owner handles. It translates only these opaque identifiers and hub correlation metadata. Request and response ordering, operation and output sequences, reset generations, geometry, modes, normalized VT, bounded history snapshots, errors, and retry facts pass through without reinterpretation. No owner handle is exposed where another owner stream could accept it.

The hub forwards only requests received from the phone. It never generates heartbeats to keep a lease alive. Phone EOF, owner EOF, SSH failure, registry removal or retargeting, stale-host detection, config mismatch, protocol failure, line overflow, or outbound backpressure cancels the owner process and closes its stdin. The owner's existing cleanup then releases only that attachment's exact lease. A headless registry tick calls `MarkStaleIfQuiet`; it does not manufacture host or terminal activity.

## Bounded implementation slices

The first review boundary adds the mobile capability plus the atomic registry route authority and validation API, with injected tests for unchanged, removed, retargeted, replaced, stale, and capability-missing clients. The second boundary starts and validates an owner mobile stream with bounded line and stderr handling, EOF cancellation, and no autonomous requests. The third composes authoritative owner catalogs and remaps handles across the full protocol, then proves a local owner and a separate loopback SSH owner with distinct configs, state trees, tmux sockets, Sidecar processes, and duplicate session/pane names. Every proof clears `TMUX` and `TMUX_PANE` and cleans only its owned resources.

The real `marcusbook` route remains a physical M1 gate while that registered host is offline and the user's global remote-host feature is disabled. Offline loopback proof can establish the architecture and refusal behavior without changing either global setting. Exact zero/one/multiple worktree and multi-pane candidate selection remains `td-ef9c30` and must complete before the M1-A parent closes.
