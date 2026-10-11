#!/bin/bash
set -euo pipefail

# Gameplane entrypoint for ARK: Survival Evolved dedicated server.
# Installs/updates the game into STEAM_INSTALL_DIR, writes the settings
# Gameplane manages from env vars, then execs the server.

STEAM_INSTALL_DIR="${STEAM_INSTALL_DIR:-/data}"
UPDATE_ON_BOOT="${UPDATE_ON_BOOT:-true}"
STEAM_VALIDATE="${STEAM_VALIDATE:-false}"

# set_kv KEY VALUE FILE: replace the first "KEY=..." line in FILE, or append
# "KEY=VALUE" when the key is missing. VALUE is escaped for sed.
set_kv() {
  local key="$1" value="$2" file="$3" escaped
  escaped="$(printf '%s' "$value" | sed -e 's/[\\|&]/\\&/g')"
  if grep -q "^${key}=" "$file"; then
    sed -i "0,/^${key}=.*/s|^${key}=.*|${key}=${escaped}|" "$file"
  else
    printf '%s=%s\n' "$key" "$value" >> "$file"
  fi
}

if [[ "$UPDATE_ON_BOOT" == "true" ]]; then
  STEAM_SKIP_IF_INSTALLED="false"
else
  STEAM_SKIP_IF_INSTALLED="true"
fi

STEAM_APPID="376030" \
STEAM_INSTALL_DIR="${STEAM_INSTALL_DIR}" \
STEAM_VALIDATE="${STEAM_VALIDATE}" \
STEAM_SKIP_IF_INSTALLED="${STEAM_SKIP_IF_INSTALLED}" \
STEAM_SENTINEL_FILE="${STEAM_INSTALL_DIR}/ShooterGame/Binaries/Linux/ShooterGameServer" \
  steam-install.sh

if [[ ! -f "${STEAM_INSTALL_DIR}/ShooterGame/Binaries/Linux/ShooterGameServer" ]]; then echo "error: game binary missing after install: ${STEAM_INSTALL_DIR}/ShooterGame/Binaries/Linux/ShooterGameServer"; exit 1; fi

PORT="${PORT:-7777}"
QUERY_PORT="${QUERY_PORT:-27015}"
RCON_PORT="${RCON_PORT:-27020}"
MAP_NAME="${MAP_NAME:-TheIsland}"

# ARK opens many files at once; raise the soft limit as far as allowed.
ulimit -n "$(ulimit -Hn)" 2>/dev/null || true

QUERY="${MAP_NAME}?listen"
QUERY+="?SessionName=${SESSION_NAME:-Gameplane ARK Server}"
QUERY+="?MaxPlayers=${MAX_PLAYERS:-70}"
QUERY+="?Port=${PORT}?QueryPort=${QUERY_PORT}"
if [[ -n "${SERVER_PASSWORD:-}" ]]; then
  QUERY+="?ServerPassword=${SERVER_PASSWORD}"
fi
if [[ -n "${RCON_PASSWORD:-}" ]]; then
  QUERY+="?ServerAdminPassword=${RCON_PASSWORD}?RCONEnabled=True?RCONPort=${RCON_PORT}"
else
  echo "warning: RCON_PASSWORD is empty; ARK RCON stays disabled"
fi

ARGS=("${QUERY}" -server -log -NoBattlEye)
if [[ -n "${CLUSTER_ID:-}" ]]; then
  mkdir -p "${STEAM_INSTALL_DIR}/ShooterGame/Saved/clusters"
  ARGS+=("-clusterid=${CLUSTER_ID}" "-ClusterDirOverride=${STEAM_INSTALL_DIR}/ShooterGame/Saved/clusters")
fi

cd "${STEAM_INSTALL_DIR}/ShooterGame/Binaries/Linux"
echo "ARK entrypoint: launching ${MAP_NAME} (port ${PORT}, query ${QUERY_PORT}, rcon ${RCON_PORT})"
exec ./ShooterGameServer "${ARGS[@]}"
