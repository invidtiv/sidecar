# Sidecar overlays

An overlay is a file in the same manifest grammar as `../upstream/<agent>.toml`, named `<agent>.toml`, carrying only what Sidecar believes it does better than upstream. It is merged over the vendored manifest by rule id: `disable = true` removes the upstream rule with that id, a rule whose id matches an upstream id replaces it, and any other rule is appended. Overlay rule ids are prefixed `sidecar.` so an upstream rule can never collide with a Sidecar addition by accident. The merged result is validated with exactly the same limits as a plain manifest. This is why the vendored files are never edited: a re-sync is then a clean file replacement, and an overlay's diff against upstream is the exact list of things we believe we do better.

This directory is empty on purpose. Overlays arrive with the engine in Phase 1 and the per-provider cutover in Phase 2; see `docs/plans/active/herdr-detection-parity.md`.
