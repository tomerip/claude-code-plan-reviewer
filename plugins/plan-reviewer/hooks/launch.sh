#!/usr/bin/env bash
# plan-reviewer launch wrapper.
#
# Only the gzipped binary for the current platform is stored in the repo (as
# bin/<os>-<arch>/plan-reviewer.gz). On first run we decompress it into a
# per-user cache and exec from there. Subsequent runs skip decompression.
set -e

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
  x86_64|amd64) ARCH=amd64 ;;
  arm64|aarch64) ARCH=arm64 ;;
esac

GZ="${CLAUDE_PLUGIN_ROOT}/bin/${OS}-${ARCH}/plan-reviewer.gz"
CACHE_DIR="${HOME}/.cache/plan-reviewer"
BIN="${CACHE_DIR}/${OS}-${ARCH}/plan-reviewer"

degrade_ask() {
  # Let ExitPlanMode proceed normally; don't break plan mode.
  printf '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"ask"},"systemMessage":"plan-reviewer: %s"}\n' "$1"
  exit 0
}

if [[ ! -f "$GZ" ]]; then
  degrade_ask "no binary bundled for ${OS}-${ARCH}"
fi

# Decompress if missing or older than the bundled .gz (e.g. after plugin update).
if [[ ! -x "$BIN" ]] || [[ "$GZ" -nt "$BIN" ]]; then
  mkdir -p "$(dirname "$BIN")"
  if ! gunzip -c "$GZ" > "$BIN.tmp"; then
    rm -f "$BIN.tmp"
    degrade_ask "failed to decompress binary"
  fi
  chmod +x "$BIN.tmp"
  mv "$BIN.tmp" "$BIN"
fi

exec "$BIN"
