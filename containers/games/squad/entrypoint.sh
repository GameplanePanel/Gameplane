#!/bin/bash
set -euo pipefail

# Gameplane entrypoint for Squad dedicated server.
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

STEAM_APPID="403240" \
STEAM_INSTALL_DIR="${STEAM_INSTALL_DIR}" \
STEAM_VALIDATE="${STEAM_VALIDATE}" \
STEAM_SKIP_IF_INSTALLED="${STEAM_SKIP_IF_INSTALLED}" \
STEAM_SENTINEL_FILE="${STEAM_INSTALL_DIR}/SquadGameServer.sh" \
  steam-install.sh

if [[ ! -f "${STEAM_INSTALL_DIR}/SquadGameServer.sh" ]]; then echo "error: game binary missing after install: ${STEAM_INSTALL_DIR}/SquadGameServer.sh"; exit 1; fi

CONFIG_DIR="${STEAM_INSTALL_DIR}/SquadGame/ServerConfig"
mkdir -p "${CONFIG_DIR}"
touch "${CONFIG_DIR}/Rcon.cfg" "${CONFIG_DIR}/Server.cfg"

PORT="${PORT:-7787}"
QUERYPORT="${QUERYPORT:-27165}"
RCONPORT="${RCONPORT:-21114}"
BEACONPORT="${BEACONPORT:-15000}"
MAX_PLAYERS="${MAX_PLAYERS:-100}"

# Rcon.cfg overrides the command-line RCON port, so keep both in step.
set_kv Port "${RCONPORT}" "${CONFIG_DIR}/Rcon.cfg"
if [[ -n "${RCON_PASSWORD:-}" ]]; then
  set_kv Password "${RCON_PASSWORD}" "${CONFIG_DIR}/Rcon.cfg"
else
  echo "warning: RCON_PASSWORD is empty; Squad RCON stays disabled"
fi

if [[ -n "${SERVER_NAME:-}" ]]; then
  set_kv ServerName "\"${SERVER_NAME}\"" "${CONFIG_DIR}/Server.cfg"
fi
set_kv MaxPlayers "${MAX_PLAYERS}" "${CONFIG_DIR}/Server.cfg"

cd "${STEAM_INSTALL_DIR}"
echo "Squad entrypoint: launching server (port ${PORT}, query ${QUERYPORT}, rcon ${RCONPORT})"
exec "${STEAM_INSTALL_DIR}/SquadGameServer.sh" \
  Port="${PORT}" \
  QueryPort="${QUERYPORT}" \
  RCONPORT="${RCONPORT}" \
  beaconport="${BEACONPORT}" \
  FIXEDMAXPLAYERS="${MAX_PLAYERS}" \
  RANDOM=NONE \
  -log
