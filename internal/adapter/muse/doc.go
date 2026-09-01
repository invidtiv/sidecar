// Package muse provides a Sidecar adapter for Muse Spark sessions.
//
// Muse Spark (Muse Code) stores sessions under
// ~/.local/share/muse/sessions/YYYY/MM/DD/<uuid>/session.jsonl and an
// index at ~/.local/share/muse/session-index.db (SQLite).
// This adapter reads the SQLite index for fast Sessions() and parses
// the JSONL log for Messages(), with a filesystem fallback when the
// index is unavailable (tests, older installs).
package muse
