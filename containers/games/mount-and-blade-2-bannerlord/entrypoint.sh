#!/bin/bash
set -euo pipefail

# Gameplane entrypoint for Mount & Blade II: Bannerlord dedicated server.
# Installs/updates the game into STEAM_INSTALL_DIR, writes the settings
# Gameplane manages from env vars, then execs the server.

STEAM_INSTALL_DIR="${STEAM_INSTALL_DIR:-/data}"
UPDATE_ON_BOOT="${UPDATE_ON_BOOT:-true}"
STEAM_VALIDATE="${STEAM_VALIDATE:-false}"

if [[ "$UPDATE_ON_BOOT" == "true" ]]; then
  STEAM_SKIP_IF_INSTALLED="false"
else
  STEAM_SKIP_IF_INSTALLED="true"
fi

# FR-013: a missing token idles with instructions instead of crash-looping.
if [[ -z "${SERVER_TOKEN:-}" ]]; then
  echo "========================================================================"
  echo "DIAGNOSTIC: Bannerlord SERVER_TOKEN is not configured."
  echo "The dedicated server needs a TaleWorlds server token:"
  echo "1. Start Mount & Blade II: Bannerlord and open Multiplayer."
  echo "2. Press ALT+~ to open the console and run: customserver.gettoken"
  echo "3. The token is written to Documents/Mount and Blade II Bannerlord/Tokens."
  echo "4. Paste it into this server's SERVER_TOKEN setting and restart."
  echo "Tokens expire after 3 months; repeat these steps when it does."
  echo "Idling now; the server will not crash-loop."
  echo "========================================================================"
  trap 'exit 0' TERM INT
  while true; do
    sleep 3600 &
    wait $!
  done
fi

STEAM_APPID="1863440" \
STEAM_PLATFORM="windows" \
STEAM_INSTALL_DIR="${STEAM_INSTALL_DIR}" \
STEAM_VALIDATE="${STEAM_VALIDATE}" \
STEAM_SKIP_IF_INSTALLED="${STEAM_SKIP_IF_INSTALLED}" \
STEAM_SENTINEL_FILE="${STEAM_INSTALL_DIR}/bin/Win64_Shipping_Server/DedicatedCustomServer.Starter.exe" \
  steam-install.sh

if [[ ! -f "${STEAM_INSTALL_DIR}/bin/Win64_Shipping_Server/DedicatedCustomServer.Starter.exe" ]]; then echo "error: game binary missing after install: ${STEAM_INSTALL_DIR}/bin/Win64_Shipping_Server/DedicatedCustomServer.Starter.exe"; exit 1; fi

PORT="${PORT:-7210}"
CONFIG_NAME="ds_config_gameplane.txt"
CONFIG_FILE="${STEAM_INSTALL_DIR}/Modules/Native/${CONFIG_NAME}"

# Gameplane rewrites this file on every start; point DS_CONFIG_FILE at your
# own file under Modules/Native/ to manage the config by hand instead.
if [[ -z "${DS_CONFIG_FILE:-}" ]]; then
  mkdir -p "$(dirname "${CONFIG_FILE}")"
  {
    echo "ServerName ${SERVER_NAME:-Gameplane Bannerlord Server}"
    echo "GameType ${GAME_TYPE:-TeamDeathmatch}"
    echo "Map ${MAP:-mp_tdm_map_001}"
    echo "MaxNumberOfPlayers ${MAX_PLAYERS:-64}"
    echo "start_game_and_mission"
  } > "${CONFIG_FILE}"
  DS_CONFIG_FILE="${CONFIG_NAME}"
fi

export WINEPREFIX="${STEAM_INSTALL_DIR}/.wine"
export WINEDEBUG="-all"

cd "${STEAM_INSTALL_DIR}/bin/Win64_Shipping_Server"
echo "Bannerlord entrypoint: launching server under Wine (port ${PORT}, config ${DS_CONFIG_FILE})"
exec wine DedicatedCustomServer.Starter.exe \
  /dedicatedcustomserverconfigfile "${DS_CONFIG_FILE}" \
  /dedicatedcustomserverauthtoken "${SERVER_TOKEN}" \
  /port "${PORT}" \
  /DisableErrorReporting \
  /no_watchdog \
  "_MODULES_*Native*Multiplayer*_MODULES_"
