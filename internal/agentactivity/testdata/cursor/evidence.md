# Cursor Agent CLI evidence and proof record

- Availability: `cursor-agent` is absent from the 2026-08-08 login-shell PATH. Cursor desktop 3.0.13 is installed but is not CLI availability. No CLI process/title capture exists.
- Authority: Herdr Cursor manifest 2026.08.03.1, process-gated approval, stop, background-task, and spinner rules; unmatched known-live fallback is debounced idle. Session hook identity is not activity.
- Explicitly unavailable: all real CLI states, overlays, exit, text capture, and PNG.
- False-positive boundary: approvals require their complete current UI vocabulary; stale prompts and desktop/process mismatches cannot win.
- Isolated proof: hardened paths were all under `/tmp/sidecar-drive-501`; compatibility observations exercise detector and shared surfaces only.
