#!/usr/bin/env bash
#
# Differential harness: does Sidecar's Go manifest engine reach the same verdict
# as Herdr's Rust engine, on real screens, against the same manifest bytes?
#
# The ported conformance tests cover the grammar. They do not cover how the two
# engines count non-empty lines on a screen with trailing whitespace, where each
# finds the Codex prompt marker or the Claude prompt box on a wrapped screen, or
# how a horizontal rule made of a slightly different glyph is classified. Those
# are exactly the details that produce a wrong badge in a pane, and the only
# oracle for them is Herdr itself.
#
#   scripts/herdr-diff.sh                # every fixture
#   scripts/herdr-diff.sh claude codex   # only these agents
#   HERDR_BIN=/path/to/herdr scripts/herdr-diff.sh
#
# Exit status is 0 when every fixture agrees and 1 when any disagrees.
#
# ---------------------------------------------------------------------------
# Two limitations of `herdr agent explain --file`, both worked around here.
#
# 1. It takes no title. `explain_for_label` (src/cli/agent.rs:130 at e2b85c7)
#    calls `explain(agent, content)`, which passes osc_title = "" and
#    osc_progress = "". So the comparison is screen-only: the fixture header is
#    stripped before either engine sees the file, and both are given the same
#    headerless screen. Every osc_title rule therefore evaluates to no-match on
#    both sides, which is correct and equal but means this harness proves
#    nothing about title rules. The ported conformance suite covers those.
#
# 2. It applies no read window. Herdr's live loop hands its engine the tail of
#    the buffer, N rows deep; `--file` hands it the file verbatim. So this
#    script feeds Herdr the window Sidecar computed
#    (`sidecar agent explain --file ... --print-window`), which isolates region
#    and gate semantics — the thing being compared — from the windowing, which
#    is measured separately in docs/reference/herdr-detection-parity.md.
#
# Herdr reads the manifest for an agent from its local override directory before
# anything else, so both engines are pointed at the *same vendored bytes*: the
# script copies internal/agentactivity/manifests/upstream/<file> into a throwaway
# XDG_CONFIG_HOME. The user's real ~/.config/herdr is never read or written.
#
# Sidecar's side additionally merges its own overlay, which today carries only
# RE2 rewrites of the four upstream `\p{Alphabetic}` patterns. Those rules are
# live on Herdr's side too, so agreement on them is a real result: it says the
# rewrite matches the same screens the original does.

set -euo pipefail

cd "$(dirname "$0")/.."

HERDR_BIN=${HERDR_BIN:-$HOME/.local/bin/herdr}
FIXTURES=internal/agentactivity/testdata
UPSTREAM=internal/agentactivity/manifests/upstream

if [ ! -x "$HERDR_BIN" ]; then
  echo "herdr binary not found at $HERDR_BIN; set HERDR_BIN" >&2
  exit 2
fi

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
override="$work/config/herdr/agent-detection"
mkdir -p "$override" "$work/state"

# Sidecar family -> (vendored file base, herdr --agent label). The two mappings
# that are not the identity are copilot (Herdr's file is github-copilot.toml but
# its label is "copilot") and antigravity (file antigravity.toml, label "agy").
manifest_file() {
  case "$1" in
    copilot) echo github-copilot ;;
    *) echo "$1" ;;
  esac
}
herdr_label() {
  case "$1" in
    antigravity) echo agy ;;
    *) echo "$1" ;;
  esac
}
# Herdr looks the override up by agent_label(agent), not by our file name.
override_name() {
  herdr_label "$1"
}

sidecar_bin="$work/sidecar"
echo "building sidecar..." >&2
go build -o "$sidecar_bin" ./cmd/sidecar

wanted=("$@")
selected() {
  [ ${#wanted[@]} -eq 0 ] && return 0
  for name in "${wanted[@]}"; do [ "$name" = "$1" ] && return 0; done
  return 1
}

printf '%-12s %-38s %-9s %-30s %-9s %-30s %s\n' \
  AGENT FIXTURE SIDECAR "SIDECAR RULE" HERDR "HERDR RULE" AGREE

total=0
agree=0
disagree=0
skipped=0

for dir in "$FIXTURES"/*/; do
  agent=$(basename "$dir")
  [ "$agent" = "proof" ] && continue
  selected "$agent" || continue

  file=$(manifest_file "$agent")
  [ -f "$UPSTREAM/$file.toml" ] || { echo "no vendored manifest for $agent" >&2; continue; }
  cp "$UPSTREAM/$file.toml" "$override/$(override_name "$agent").toml"
  label=$(herdr_label "$agent")

  for fixture in "$dir"*.txt; do
    [ -e "$fixture" ] || continue
    base=$(basename "$fixture")
    case "$base" in process_identity.txt|availability.txt) continue ;; esac
    grep -q '^screen:$' "$fixture" || { skipped=$((skipped + 1)); continue; }

    # The pane height the fixture declares, or Herdr's own 24 fallback. Both
    # engines must window at the same N or the comparison measures the window
    # rather than the rules.
    rows=$(sed -n 's/^pane_height: *//p' "$fixture" | head -1)
    rows=${rows:-24}

    window="$work/window.txt"
    "$sidecar_bin" agent explain --file "$fixture" --agent "$agent" --rows "$rows" --print-window > "$window"

    sc=$("$sidecar_bin" agent explain --file "$window" --agent "$agent" --rows "$rows" --json)
    hd=$(XDG_CONFIG_HOME="$work/config" XDG_STATE_HOME="$work/state" \
      "$HERDR_BIN" agent explain --file "$window" --agent "$label" --json)

    read -r verdict sstate srule hstate hrule < <(
      SIDECAR_JSON="$sc" HERDR_JSON="$hd" python3 - <<'PY'
import json, os

def read(name):
    d = json.loads(os.environ[name])
    rule = d.get("matched_rule") or {}
    return (
        d.get("state") or "?",
        rule.get("id") or "(" + (d.get("fallback_reason") or "none") + ")",
        d.get("fallback_reason") or "",
    )

s_state, s_rule, s_fallback = read("SIDECAR_JSON")
h_state, h_rule, h_fallback = read("HERDR_JSON")
same = s_state == h_state and s_rule == h_rule and s_fallback == h_fallback
print("AGREE" if same else "DIFFER", s_state, s_rule, h_state, h_rule)
PY
    )

    total=$((total + 1))
    if [ "$verdict" = AGREE ]; then
      agree=$((agree + 1))
    else
      disagree=$((disagree + 1))
    fi
    printf '%-12s %-38s %-9s %-30s %-9s %-30s %s\n' \
      "$agent" "$base" "$sstate" "$srule" "$hstate" "$hrule" "$verdict"
  done
done

echo
echo "$total fixtures compared, $agree agree, $disagree disagree, $skipped skipped (no screen block)"
[ "$disagree" -eq 0 ]
