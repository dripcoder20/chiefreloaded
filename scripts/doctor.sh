#!/usr/bin/env bash
#
# Loop-specific environment check. `wails3 doctor` covers the build toolchain;
# this covers the things Loop needs at *runtime* — git, gh, the stacked-PR
# extension, and at least one agent CLI.
#
# The same probes are implemented in Go as session.Environment() and surfaced in
# the app. This script exists so the answer is available before the app builds.

set -uo pipefail

ok=0
warn=0
fail=0

green() { printf '\033[32m%s\033[0m' "$1"; }
yellow() { printf '\033[33m%s\033[0m' "$1"; }
red() { printf '\033[31m%s\033[0m' "$1"; }

row() { printf '  %-22s %s\n' "$1" "$2"; }

pass() { row "$1" "$(green ✓) $2"; ok=$((ok + 1)); }
soft() { row "$1" "$(yellow ○) $2"; warn=$((warn + 1)); }
hard() { row "$1" "$(red ✗) $2"; fail=$((fail + 1)); }

echo
echo "Required"
command -v git >/dev/null && pass git "$(git --version | awk '{print $3}')" \
  || hard git "not found"
command -v go >/dev/null && pass go "$(go version | awk '{print $3}')" \
  || hard go "not found — brew install go"
command -v node >/dev/null && pass node "$(node --version)" \
  || hard node "not found"
# `wails3 version` writes to stderr, not stdout.
command -v wails3 >/dev/null && pass wails3 "$(wails3 version 2>&1 >/dev/null | head -1)" \
  || hard wails3 "not found — task setup"

echo
echo "Pull requests"
if command -v gh >/dev/null; then
  pass gh "$(gh --version | head -1 | awk '{print $3}')"
  if gh auth status >/dev/null 2>&1; then
    pass "gh auth" "authenticated"
  else
    soft "gh auth" "not authenticated — gh auth login"
  fi
  if gh extension list 2>/dev/null | grep -q 'github/gh-stack'; then
    pass "gh-stack" "installed"
  else
    # Not fatal: internal/ghstack falls back to plain `gh pr create --draft
    # --base <prev>`, which produces the same branch topology without the
    # native GitHub Stack object.
    soft "gh-stack" "not installed — gh extension install github/gh-stack"
  fi
else
  soft gh "not found — pull-request features disabled"
fi

echo
echo "Agent CLIs"
found_agent=0
# The set supported by the vendored chief v0.8.0. Gemini exists upstream but
# landed after the tag; add it here when sync-upstream picks it up.
for agent in claude codex opencode cursor; do
  if command -v "$agent" >/dev/null; then
    pass "$agent" "$(command -v "$agent")"
    found_agent=1
  fi
done
if [[ "$found_agent" == "0" ]]; then
  hard "agent" "none found — install at least one of claude, codex, opencode, cursor"
fi

echo
echo "Vendored engine"
if [[ -f internal/chief/UPSTREAM.manifest ]]; then
  ref="$(awk '/^# ref:/ {print $3}' internal/chief/UPSTREAM.manifest)"
  pass "internal/chief" "${ref:-unknown}"
else
  hard "internal/chief" "not synced — task sync-upstream"
fi

echo
printf '  %d ok, %d warning(s), %d problem(s)\n\n' "$ok" "$warn" "$fail"
[[ "$fail" -eq 0 ]]
