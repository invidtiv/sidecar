# Mobile protocol v0 production corpus

`ssh-fresh-reconnect.jsonl` is the actual JSONL exchange emitted by the production Go service during the private `ssh -T aerie.local` M0-C proof. The pane and input were synthetic. The transcript includes a colored blank, a Unicode combining sequence, a Unicode wide cell, resize/reset, heartbeat, a disconnected first service process, exact-identity reconnect through a second service process, release, and close. Random opaque handles and process identities are pinned as fixture data; consumers must treat them as opaque.

Provenance: `scripts/mobile-service-proof.sh run ssh /tmp/sidecar-mobile-m0c-ssh-pinned3` from the uncommitted `codex/mobile-service` candidate based on `85a3364a74842ffcadb21a772b6d6651b1ddca75`, using tmux 3.7c. `docs/plans/active/sidecar-mobile/proof/mobile-service-m0c.json` records the result. The tmux 3.4/current capability and marker transaction probes are in `mobile-service-tmux-compat.json`.
