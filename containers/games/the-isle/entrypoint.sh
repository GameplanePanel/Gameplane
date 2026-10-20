#!/bin/bash
set -euo pipefail

# Gameplane entrypoint for The Isle dedicated server.
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

STEAM_APPID="412680" \
STEAM_BETA="${THE_ISLE_BRANCH:-evrima}" \
STEAM_INSTALL_DIR="${STEAM_INSTALL_DIR}" \
STEAM_VALIDATE="${STEAM_VALIDATE}" \
STEAM_SKIP_IF_INSTALLED="${STEAM_SKIP_IF_INSTALLED}" \
STEAM_SENTINEL_FILE="${STEAM_INSTALL_DIR}/TheIsle/Binaries/Linux/TheIsleServer-Linux-Shipping" \
  steam-install.sh

if [[ ! -f "${STEAM_INSTALL_DIR}/TheIsle/Binaries/Linux/TheIsleServer-Linux-Shipping" ]]; then echo "error: game binary missing after install: ${STEAM_INSTALL_DIR}/TheIsle/Binaries/Linux/TheIsleServer-Linux-Shipping"; exit 1; fi

CONFIG_DIR="${STEAM_INSTALL_DIR}/TheIsle/Saved/Config/LinuxServer"
GAME_INI="${CONFIG_DIR}/Game.ini"
mkdir -p "${CONFIG_DIR}"
if [[ ! -f "${GAME_INI}" ]]; then
  printf '%s\n' "[/Script/TheIsle.TIGameSession]" > "${GAME_INI}"
fi

PORT="${PORT:-7777}"
RCON_PORT="${RCON_PORT:-8888}"

set_kv ServerName "${SERVER_NAME:-Gameplane The Isle Server}" "${GAME_INI}"
set_kv MaxPlayerCount "${MAX_PLAYERS:-50}" "${GAME_INI}"
if [[ -n "${SERVER_PASSWORD:-}" ]]; then
  set_kv bServerPassword "true" "${GAME_INI}"
  set_kv ServerPassword "${SERVER_PASSWORD}" "${GAME_INI}"
else
  set_kv bServerPassword "false" "${GAME_INI}"
fi
if [[ -n "${RCON_PASSWORD:-}" ]]; then
  set_kv bRconEnabled "true" "${GAME_INI}"
  set_kv RconPassword "${RCON_PASSWORD}" "${GAME_INI}"
  set_kv RconPort "${RCON_PORT}" "${GAME_INI}"
else
  echo "warning: RCON_PASSWORD is empty; The Isle RCON stays disabled"
  set_kv bRconEnabled "false" "${GAME_INI}"
fi

cd "${STEAM_INSTALL_DIR}"
echo "The Isle entrypoint: launching server (port ${PORT}, rcon ${RCON_PORT})"
exec "${STEAM_INSTALL_DIR}/TheIsle/Binaries/Linux/TheIsleServer-Linux-Shipping" \
  -Port="${PORT}" \
  -log
