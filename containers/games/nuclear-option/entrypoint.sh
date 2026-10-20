#!/bin/bash
set -euo pipefail

# Gameplane entrypoint for Nuclear Option dedicated server.
#
# This script:
# 1. Installs/updates the game via SteamCMD (if UPDATE_ON_BOOT is enabled)
# 2. Configures the server (LD_LIBRARY_PATH, working directory)
# 3. Launches the game server as PID 1 via exec (so SIGTERM reaches it directly)

# Configuration from environment variables with sensible defaults.
STEAM_INSTALL_DIR="${STEAM_INSTALL_DIR:-/data}"
UPDATE_ON_BOOT="${UPDATE_ON_BOOT:-true}"
STEAM_VALIDATE="${STEAM_VALIDATE:-false}"

# Nuclear Option Steam app ID.
NUCLEAR_OPTION_APPID="3930080"

# The game binary, used as the sentinel file to detect a successful install.
GAME_BINARY="${STEAM_INSTALL_DIR}/NuclearOptionServer.x86_64"

# Optional config file path override (passed to -DedicatedServer flag).
DEDICATED_SERVER_CONFIG="${DEDICATED_SERVER_CONFIG:-}"

# Optional flag to enable the remote command server on 127.0.0.1:7779.
# Enabled by default (the game binds loopback-only anyway, safe to enable by default).
REMOTE_COMMANDS_ENABLED="${REMOTE_COMMANDS_ENABLED:-true}"

# Determine whether to skip the install based on UPDATE_ON_BOOT.
# If UPDATE_ON_BOOT is true, always run SteamCMD (STEAM_SKIP_IF_INSTALLED=false).
# If UPDATE_ON_BOOT is false, skip if already installed (STEAM_SKIP_IF_INSTALLED=true).
if [[ "$UPDATE_ON_BOOT" == "true" ]]; then
  STEAM_SKIP_IF_INSTALLED="false"
  echo "Nuclear Option entrypoint: UPDATE_ON_BOOT is enabled, will check for updates on boot"
else
  STEAM_SKIP_IF_INSTALLED="true"
  echo "Nuclear Option entrypoint: UPDATE_ON_BOOT is disabled, skipping install/update check"
fi

# Call the shared steam-install.sh helper.
# STEAM_APPID and STEAM_SENTINEL_FILE are required by steam-install.sh;
# we provide them here rather than relying on the environment.
STEAM_APPID="${NUCLEAR_OPTION_APPID}" \
STEAM_INSTALL_DIR="${STEAM_INSTALL_DIR}" \
STEAM_VALIDATE="${STEAM_VALIDATE}" \
STEAM_SKIP_IF_INSTALLED="${STEAM_SKIP_IF_INSTALLED}" \
STEAM_SENTINEL_FILE="${GAME_BINARY}" \
STEAM_RETRY_COUNT="3" \
STEAM_RETRY_DELAY="5" \
  steam-install.sh

# steam-install.sh may exit with code 0 if STEAM_SKIP_IF_INSTALLED was true
# and the sentinel file already exists. If it fails (non-zero exit code),
# the binary check below will catch it and fail loudly.

# Ensure the game binary exists before proceeding.
if [[ ! -f "$GAME_BINARY" ]]; then
  echo "error: game binary still missing after install: $GAME_BINARY"
  exit 1
fi

# Configure the runtime environment.
# The game's entrypoint (RunServer.sh) and the binary both require
# LD_LIBRARY_PATH to include the steamclient.so directory.
export LD_LIBRARY_PATH="${STEAM_INSTALL_DIR}/linux64:${LD_LIBRARY_PATH:-}"

# Change to the install directory. The game expects to be run from there
# (e.g., for relative paths to DedicatedServerConfig.json, logs/, etc.).
cd "${STEAM_INSTALL_DIR}"

# Build the game launch command.
# The game must be exec'd (not backgrounded) so that it becomes PID 1,
# allowing Kubernetes to send SIGTERM directly to it. This ensures graceful shutdown.
LAUNCH_CMD=("${GAME_BINARY}" -batchmode -nographics)

# Add the remote commands flag if enabled. It binds 127.0.0.1:7779 (loopback only),
# so it's safe to enable by default. The operator can disable it via env var if needed.
if [[ "$REMOTE_COMMANDS_ENABLED" == "true" ]]; then
  LAUNCH_CMD+=(-ServerRemoteCommands)
fi

# Add the dedicated server config path if provided by the operator.
# If absent, the game generates DedicatedServerConfig.json on first start.
if [[ -n "$DEDICATED_SERVER_CONFIG" ]]; then
  LAUNCH_CMD+=(-DedicatedServer "${DEDICATED_SERVER_CONFIG}")
fi

# Log the launch command for debugging (redact any secrets if present).
echo "Nuclear Option entrypoint: launching server"
echo "  Install directory: ${STEAM_INSTALL_DIR}"
echo "  LD_LIBRARY_PATH: ${LD_LIBRARY_PATH}"
echo "  Command: ${LAUNCH_CMD[*]}"

# Launch the game as PID 1. The shell is replaced with the game process,
# so SIGTERM sent by Kubernetes reaches the game directly.
# This ensures the game has time to save state and shut down gracefully.
exec "${LAUNCH_CMD[@]}"
