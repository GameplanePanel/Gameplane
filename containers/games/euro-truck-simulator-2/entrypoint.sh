#!/usr/bin/env bash
set -eo pipefail

PID_GAME=""

term_handler() {
    echo "[gameplane-ets2] SIGTERM/SIGINT received, initiating graceful shutdown..."
    if [ -n "$PID_GAME" ]; then
        echo "[gameplane-ets2] Stopping server process $PID_GAME..."
        kill -TERM "$PID_GAME" 2>/dev/null || true
    fi
    wait
    echo "[gameplane-ets2] Clean shutdown complete."
    exit 0
}

trap term_handler SIGTERM SIGINT

# Resolve logon token from environment
LOGON_TOKEN="${SERVER_LOGON_TOKEN:-${ETS2_SERVER_LOGON_TOKEN:-${LOGON_TOKEN:-}}}"

# FR-013: Non-crashing diagnostic idle for missing logon token
if [ -z "$LOGON_TOKEN" ]; then
    echo "========================================================================"
    echo "DIAGNOSTIC: Euro Truck Simulator 2 Server Logon Token is not configured!"
    echo "The dedicated server requires an authenticated Steam server logon token."
    echo ""
    echo "Step-by-step instructions to obtain and configure your logon token:"
    echo "1. Visit the Steam Game Server Account Management page:"
    echo "   https://steamcommunity.com/dev/managegameservers"
    echo "2. Enter App ID '227300' (Euro Truck Simulator 2)."
    echo "3. Enter a memo/label for your server and click 'Create'."
    echo "4. Copy the generated Login Token."
    echo "5. Configure the token in your GameServer specification:"
    echo "     spec:"
    echo "       env:"
    echo "         - name: SERVER_LOGON_TOKEN"
    echo "           value: \"your_logon_token_here\""
    echo ""
    echo "Entering graceful idle mode. The server will not crash-loop."
    echo "Update your manifest and restart the server once configured."
    echo "========================================================================"

    # Startup-probe marker: lets the probe accept this idle (never Ready).
    touch /tmp/gp-diagnostic-idle
    while true; do
        sleep 3600 &
        wait $!
    done
fi

# Persistent volume: the GameTemplate mounts the PVC at /data (GP_DATA_DIR);
# older templates mount it at the default below.
DATA="${GP_DATA_DIR:-/home/steam/.local/share/Euro Truck Simulator 2}"
SERVER_BIN="${DATA}/bin/linux_x64/eurotrucks2_server"

# Install (or update) the dedicated server into the volume.
# SteamCMD keeps its state under $HOME; fall back to a writable directory when
# the template overrides HOME to a path this image does not have.
if ! mkdir -p "$HOME" 2>/dev/null || [ ! -w "$HOME" ]; then
    export HOME=/tmp/gp-home
    mkdir -p "$HOME"
fi

# ETS2 Dedicated Server (free tool, anonymous login).
STEAM_APPID="1948160" \
STEAM_INSTALL_DIR="$DATA" \
STEAM_SKIP_IF_INSTALLED="${STEAM_SKIP_IF_INSTALLED:-true}" \
STEAM_SENTINEL_FILE="$SERVER_BIN" \
    steam-install.sh

if [ ! -f "$SERVER_BIN" ]; then
    echo "[gameplane-ets2] ERROR: server binary missing after install: $SERVER_BIN" >&2
    exit 1
fi

# Keep the profile (server_config.sii, logs, packages) on the volume: the
# game reads it from ~/.local/share/Euro Truck Simulator 2.
PROFILE_DIR="${DATA}/profile"
# Volumes from older templates keep the profile at the volume root: keep
# using it there so an existing server_config.sii is not ignored.
if [ -f "${DATA}/server_config.sii" ] && [ ! -f "${PROFILE_DIR}/server_config.sii" ]; then
    PROFILE_DIR="$DATA"
fi
CONFIG_DIR="${HOME}/.local/share/Euro Truck Simulator 2"
mkdir -p "$PROFILE_DIR" "$(dirname "$CONFIG_DIR")"
case "$(realpath -m "$DATA")/" in
    "$(realpath -m "$CONFIG_DIR")"/*)
        # The volume is the profile folder itself (or inside it): never
        # replace it with a link.
        PROFILE_DIR="$CONFIG_DIR" ;;
esac
if [ "$PROFILE_DIR" != "$CONFIG_DIR" ] && [ ! -L "$CONFIG_DIR" ]; then
    rm -rf "$CONFIG_DIR"
    ln -s "$PROFILE_DIR" "$CONFIG_DIR"
fi

# Configure server_config.sii if token is available
if [ -f "$CONFIG_DIR/server_config.sii" ]; then
    sed -i "s/server_logon_token: .*/server_logon_token: \"$LOGON_TOKEN\"/" "$CONFIG_DIR/server_config.sii"
fi

echo "[gameplane-ets2] Starting Euro Truck Simulator 2 dedicated server..."

cd "$DATA/bin/linux_x64"
"$SERVER_BIN" -server "$@" &
PID_GAME=$!
wait "$PID_GAME" 2>/dev/null || true
