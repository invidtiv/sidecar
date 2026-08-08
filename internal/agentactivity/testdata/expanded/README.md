# Expanded provider compatibility provenance

These rules are compatibility fixtures, not captured provider screens. On
2026-08-08 none of the five CLIs resolved in Sidecar's login-shell PATH, so no
terminal UI was synthesized and no provider hooks were installed.

| Provider | Availability | Herdr manifest | State authority used here |
| --- | --- | --- | --- |
| Pi | inactive Pi 0.67.68 in an older mise Node tree | 2026.06.10.1 | process-gated current-bottom screen fallback |
| GitHub Copilot CLI | unavailable | 2026.07.07.1 | process-gated current-bottom screen |
| Cursor Agent CLI | unavailable; Cursor desktop 3.0.13 is not the CLI | 2026.08.03.1 | process-gated current-bottom screen |
| OpenCode | unavailable; plugin dependencies are not the CLI | 2026.06.10.1 | process-gated current-bottom screen; no lifecycle plugin installed |
| Amp | unavailable | 2026.07.09.1 | process-gated current-bottom screen and pane title |

Known-live unmatched output falls back to idle and therefore passes through the
shared debounce/unseen-done tracker. A process mismatch is unknown immediately.
Conversation files and session hooks are not activity authorities.
