#!/bin/bash
set -euo pipefail

# steam-install.sh: Shared SteamCMD installation helper for game server images.
#
# Downloads and installs a game via SteamCMD, with transient-failure resilience
# and validation. Intended to run at container startup (in pod spec entrypoint
# or init container) to populate a persistent volume with game files.
#
# Exit code 0 means success; non-zero on permanent failure.
# Progress is logged to stdout for `kubectl logs`.
#
# Usage:
#   STEAM_APPID=220 \
#   STEAM_INSTALL_DIR=/data \
#   STEAM_VALIDATE=false \
#   STEAM_SKIP_IF_INSTALLED=true \
#   STEAM_SENTINEL_FILE=/data/srcds_run \
#     steam-install.sh

# Configuration from env vars with sensible defaults.
STEAM_APPID="${STEAM_APPID:-}"
STEAM_INSTALL_DIR="${STEAM_INSTALL_DIR:-/data}"
STEAM_VALIDATE="${STEAM_VALIDATE:-false}"
STEAM_SKIP_IF_INSTALLED="${STEAM_SKIP_IF_INSTALLED:-true}"
STEAM_SENTINEL_FILE="${STEAM_SENTINEL_FILE:-}"
STEAM_RETRY_COUNT="${STEAM_RETRY_COUNT:-3}"
STEAM_RETRY_DELAY="${STEAM_RETRY_DELAY:-5}"

# Validate required parameters.
if [[ -z "$STEAM_APPID" ]]; then
  echo "error: STEAM_APPID is required"
  exit 1
fi

if [[ -z "$STEAM_INSTALL_DIR" ]]; then
  echo "error: STEAM_INSTALL_DIR is required"
  exit 1
fi

if [[ -z "$STEAM_SENTINEL_FILE" ]]; then
  echo "error: STEAM_SENTINEL_FILE is required (e.g. the game binary path)"
  exit 1
fi

# If --skip-if-installed is set and the sentinel file already exists, skip the install.
if [[ "$STEAM_SKIP_IF_INSTALLED" == "true" ]] && [[ -f "$STEAM_SENTINEL_FILE" ]]; then
  echo "steam-install: $STEAM_SENTINEL_FILE already exists, skipping install"
  exit 0
fi

# Ensure the install directory exists.
mkdir -p "$STEAM_INSTALL_DIR"

# Build the steamcmd command. steamcmd/steamcmd:latest (Ubuntu-based) puts
# the binary on PATH at /usr/bin/steamcmd — there is no
# /home/steam/steamcmd/steamcmd.sh (that path belonged to a different,
# Alpine-based image this script was originally written against and never
# existed in the image we actually ship on). Invoke it by name so this also
# keeps working if the upstream image ever moves the binary within PATH.
STEAMCMD_CMD=(
  "steamcmd"
  "+force_install_dir" "$STEAM_INSTALL_DIR"
  "+login" "anonymous"
  "+app_update" "$STEAM_APPID"
)

# Add validation flag if requested.
if [[ "$STEAM_VALIDATE" == "true" ]]; then
  STEAMCMD_CMD+=("validate")
fi

STEAMCMD_CMD+=("+quit")

# Attempt the install with retries on transient failure.
# SteamCMD is notorious for exiting 0 even when the CDN has a hiccup —
# that's why we verify the sentinel file exists after each run.
attempt=1
while [[ $attempt -le $STEAM_RETRY_COUNT ]]; do
  echo "steam-install: attempt $attempt/$STEAM_RETRY_COUNT: installing app $STEAM_APPID to $STEAM_INSTALL_DIR"

  # Run steamcmd. Capture exit code but don't fail yet.
  if "${STEAMCMD_CMD[@]}"; then
    steamcmd_exit=0
  else
    steamcmd_exit=$?
  fi

  # Check if the sentinel file exists (indicating a successful install).
  if [[ -f "$STEAM_SENTINEL_FILE" ]]; then
    echo "steam-install: success — sentinel file $STEAM_SENTINEL_FILE found"
    exit 0
  fi

  # Sentinel file not found. If this is the last attempt, fail.
  if [[ $attempt -eq $STEAM_RETRY_COUNT ]]; then
    echo "error: steam-install failed after $STEAM_RETRY_COUNT attempts"
    echo "  steamcmd exit code was $steamcmd_exit (0 does not guarantee success — checking for $STEAM_SENTINEL_FILE failed)"
    exit 1
  fi

  # Retry with backoff. Steam CDN blips are common; crashing is worse.
  echo "steam-install: sentinel file not found after attempt $attempt, retrying in ${STEAM_RETRY_DELAY}s..."
  sleep "$STEAM_RETRY_DELAY"
  attempt=$((attempt + 1))
done

# Unreachable (but required by set -e logic).
exit 1
