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
#   scripts/herdr-diff.sh                # every fixture, upstream bytes only
#   scripts/herdr-diff.sh claude codex   # only these agents
#   scripts/herdr-diff.sh --merged       # both engines on the merged manifest
#   HERDR_BIN=/path/to/herdr scripts/herdr-diff.sh
#
# Exit status is 0 when every fixture agrees and 1 when any disagrees.
#
# There are two modes and they ask two different questions. Run both.
#
#   default   Herdr is given the *vendored upstream file alone* and Sidecar runs
#             upstream plus its overlay. The question is "where do we knowingly
#             differ from Herdr, and is that list exactly the overlay?"
#   --merged  Herdr is given the *same merged manifest Sidecar runs*, assembled
#             from upstream plus the overlay by the same three rules Merge uses.
#             The question is "does our engine execute our own rules the way
#             Herdr's engine would?" — which the default mode cannot ask, because
#             in that mode every sidecar.* rule is a divergence by construction.
#             Here there are no overlay divergences: every fixture must agree.
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
# Sidecar's side is isolated the same way and for the same reason: it also reads
# a local override directory, so every `sidecar` invocation here is given its own
# config axis with -config. See run_sidecar below.
#
# Sidecar's side additionally merges its own overlay, and the overlay holds two
# different kinds of rule, which the harness must treat differently.
#
#   - A rule carrying an *upstream* id is a rewrite of upstream's own rule: the
#     four `\p{Alphabetic}` patterns RE2 cannot compile. Those rules are live on
#     Herdr's side too, so agreement on them is a real result — it says the
#     rewrite matches the same screens the original does — and a disagreement is
#     a bug.
#   - A rule carrying a `sidecar.` id is a rule Herdr does not have, added
#     because we believe Herdr is wrong or silent about that screen. A fixture
#     that matches one is *expected* to differ: the divergence is the whole
#     point of the overlay, and the fixture beside it is the evidence.
#
# So a fixture whose Sidecar verdict comes from a `sidecar.` rule is counted as
# an overlay divergence and reported separately. It does not fail the run. What
# would fail the run is such a fixture agreeing with Herdr, because that means
# the overlay rule is no longer doing anything and should be deleted — the
# "overlay changes nothing" signal the plan asks the sync report to raise.

set -euo pipefail

cd "$(dirname "$0")/.."

HERDR_BIN=${HERDR_BIN:-$HOME/.local/bin/herdr}
FIXTURES=internal/agentactivity/testdata
UPSTREAM=internal/agentactivity/manifests/upstream
SIDECAR_OVERLAYS=internal/agentactivity/manifests/sidecar

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

# merge_manifest assembles the merged manifest --merged hands Herdr, applying the
# same three rules manifest.Merge applies: an overlay rule whose id matches an
# upstream rule replaces it in place, `disable = true` on an upstream id removes
# it, and any other overlay rule is appended. It works on whole `[[rules]]`
# blocks keyed by their `id =` line, which is enough because an overlay is
# written in the manifest grammar and nothing else in these files is
# block-structured. It is here rather than in Go because the only consumer is
# this script: Sidecar itself never needs the merged file as text.
merge_manifest() {
  UPSTREAM_FILE="$1" OVERLAY_FILE="$2" python3 - <<'PY'
import os, re, sys

def split_blocks(text):
    """(preamble, [(id, block_text)]) for one manifest file."""
    parts = re.split(r'(?m)^\[\[rules\]\]\s*$', text)
    blocks = []
    for body in parts[1:]:
        match = re.search(r'(?m)^id\s*=\s*"([^"]+)"', body)
        if not match:
            sys.exit("a [[rules]] block has no id")
        blocks.append((match.group(1), "[[rules]]\n" + body.strip("\n") + "\n"))
    return parts[0], blocks

upstream, rules = split_blocks(open(os.environ["UPSTREAM_FILE"]).read())
_, overlay = split_blocks(open(os.environ["OVERLAY_FILE"]).read())

index = {rule_id: i for i, (rule_id, _) in enumerate(rules)}
appended = []
for rule_id, block in overlay:
    disabled = re.search(r'(?m)^disable\s*=\s*true\s*$', block) is not None
    if rule_id in index:
        if disabled:
            rules[index[rule_id]] = None
        else:
            rules[index[rule_id]] = (rule_id, block)
    elif disabled:
        sys.exit("overlay disables unknown rule " + rule_id)
    else:
        appended.append((rule_id, block))

sys.stdout.write(upstream.rstrip("\n") + "\n\n")
for entry in rules + appended:
    if entry is not None:
        sys.stdout.write(entry[1] + "\n")
PY
}

# The classifier lives in a file rather than inline, because a quoted heredoc
# nested inside a process substitution is parsed for quotes before it is read as
# a heredoc, so an odd apostrophe in a Python comment breaks the script.
# harness-exempt: a `sidecar.` rule whose only effect is a title-versus-screen
# conflict cannot be judged here, because `herdr agent explain --file` passes an
# empty osc_title and this script therefore blanks the title on both sides. Such
# a rule looks redundant on every fixture and is not. An overlay declares one by
# writing a line of the form
#
#   # harness-exempt: sidecar.<rule id> — why the harness cannot see it
#
# and this is where that list is read. It is deliberately a whitelist of exact
# rule ids: it silences the redundancy check for those rules only, and every
# other `sidecar.` rule still has to earn its place on a fixture.
#
# Each entry is keyed by the overlay file that declares it, because a rule id is
# not unique across agents: `sidecar.overlay_retain` exists in both claude.toml
# and grok.toml, and an unscoped whitelist would silence the redundancy check for
# one agent's rule because another agent's rule of the same name is exempt. The
# id pattern allows dots for the same reason it must: every id here begins
# "sidecar." and a further dot in the name is legal.
exempt=$(for overlay in "$SIDECAR_OVERLAYS"/*.toml; do
  [ -e "$overlay" ] || continue
  base=$(basename "$overlay" .toml)
  # An overlay with no exemption is the normal case, and grep exits 1 on it;
  # `|| true` keeps that from ending the run under `set -o pipefail`.
  { grep -ho '^# harness-exempt: sidecar\.[A-Za-z0-9_.]*' "$overlay" 2>/dev/null || true; } |
    sed "s/^# harness-exempt: /$base:/"
done | paste -sd, -)

classifier="$work/classify.py"
cat > "$classifier" <<'PY'
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
exempt = set(filter(None, os.environ.get("EXEMPT", "").split(",")))

if h_fallback == "unknown_agent":
    # The installed Herdr binary has no manifest for this agent at all, so
    # there is nothing to compare against. muse is the standing case: Herdr
    # ships muse.toml bundled-only and 0.8.2 predates it.
    verdict = "NO-ORACLE"
elif os.environ["OVERLAY_FILE_BASE"] + ":" + s_rule in exempt and os.environ["MERGED"] != "1":
    # A rule the overlay declares the harness structurally cannot judge. See
    # the harness-exempt block below.
    verdict = "TITLE-ONLY"
elif s_rule.startswith("sidecar.") and os.environ["MERGED"] != "1":
    # A Sidecar-owned rule matched while Herdr was given upstream alone.
    # Differing is the expected outcome and is not a failure; reaching the
    # verdict Herdr reaches without the rule means the rule has stopped
    # changing anything.
    #
    # Redundancy is measured on the state and the fallback reason alone,
    # deliberately: the two rule ids can never be equal here, since one begins
    # with "sidecar." and the other is a Herdr id. Folding the id into this
    # comparison would make REDUNDANT unreachable and silently retire the
    # overlay-changes-nothing signal the plan asks for.
    verdict = "REDUNDANT" if (s_state == h_state and s_fallback == h_fallback) else "OVERLAY"
else:
    verdict = "AGREE" if (s_state == h_state and s_rule == h_rule and s_fallback == h_fallback) else "DIFFER"
print(verdict, s_state, s_rule, h_state, h_rule)
PY

sidecar_bin="$work/sidecar"
echo "building sidecar..." >&2
go build -o "$sidecar_bin" ./cmd/sidecar

# Sidecar's side needs the same isolation the Herdr side gets, for the same
# reason. `sidecar agent explain` reads a local manifest override from
# ~/.config/sidecar/agent-detection, so a developer who has tuned one rule for
# one agent -- which is exactly what that directory is for -- would otherwise
# have this harness compare their override against Herdr's vendored bytes and
# report a disagreement per fixture. The numbers in
# docs/reference/herdr-detection-parity.md are the only tripwire for that.
#
# -config moves the whole config axis, and the override directory is derived from
# it (manifests.OverrideDir). It has to come *before* the subcommand: global
# flags are stripped ahead of dispatch (internal/cli/cli.go stripGlobalFlags),
# and `sidecar agent explain -config X` is an unknown flag to the subcommand.
# Note that XDG_CONFIG_HOME moves nothing here; -config is the only lever.
mkdir -p "$work/sidecar-config" "$work/sidecar-state"
run_sidecar() {
  XDG_STATE_HOME="$work/sidecar-state" \
    "$sidecar_bin" -config "$work/sidecar-config/config.json" "$@"
}

merged=0
args=()
for arg in "$@"; do
  case "$arg" in
    --merged) merged=1 ;;
    -*) echo "unknown flag $arg" >&2; exit 2 ;;
    *) args+=("$arg") ;;
  esac
done

wanted=("${args[@]+"${args[@]}"}")
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
overlay=0
redundant=0
unjudged=0
skipped=0

for dir in "$FIXTURES"/*/; do
  agent=$(basename "$dir")
  [ "$agent" = "proof" ] && continue
  selected "$agent" || continue

  file=$(manifest_file "$agent")
  [ -f "$UPSTREAM/$file.toml" ] || { echo "no vendored manifest for $agent" >&2; continue; }
  target="$override/$(override_name "$agent").toml"
  if [ "$merged" -eq 1 ] && [ -f "$SIDECAR_OVERLAYS/$file.toml" ]; then
    merge_manifest "$UPSTREAM/$file.toml" "$SIDECAR_OVERLAYS/$file.toml" > "$target"
  else
    cp "$UPSTREAM/$file.toml" "$target"
  fi
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
    run_sidecar agent explain --file "$fixture" --agent "$agent" --rows "$rows" --print-window > "$window"

    sc=$(run_sidecar agent explain --file "$window" --agent "$agent" --rows "$rows" --json)
    hd=$(XDG_CONFIG_HOME="$work/config" XDG_STATE_HOME="$work/state" \
      "$HERDR_BIN" agent explain --file "$window" --agent "$label" --json)

    read -r verdict sstate srule hstate hrule < <(
      SIDECAR_JSON="$sc" HERDR_JSON="$hd" MERGED="$merged" EXEMPT="$exempt" \
        OVERLAY_FILE_BASE="$file" python3 "$classifier")

    total=$((total + 1))
    case "$verdict" in
      AGREE) agree=$((agree + 1)) ;;
      OVERLAY | TITLE-ONLY) overlay=$((overlay + 1)) ;;
      NO-ORACLE) unjudged=$((unjudged + 1)) ;;
      REDUNDANT) redundant=$((redundant + 1)) ;;
      *) disagree=$((disagree + 1)) ;;
    esac
    printf '%-12s %-38s %-9s %-30s %-9s %-30s %s\n' \
      "$agent" "$base" "$sstate" "$srule" "$hstate" "$hrule" "$verdict"
  done
done

echo
echo "$total fixtures compared, $agree agree, $disagree disagree, $overlay overlay divergences, $redundant redundant overlay rules, $unjudged with no Herdr oracle, $skipped skipped (no screen block)"
if [ "$redundant" -gt 0 ]; then
  echo "a sidecar.* rule reached the same verdict Herdr reaches without it; delete the rule or explain why it stays" >&2
fi
[ "$disagree" -eq 0 ] && [ "$redundant" -eq 0 ]
