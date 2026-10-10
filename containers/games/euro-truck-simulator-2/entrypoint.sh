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
    echo "2. Enter App ID '1948400' (Euro Truck Simulator 2 Dedicated Server)."
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

    while true; do
        sleep 3600 &
        wait $!
    done
fi

SERVER_BIN="/serverdata/bin/linux_x64/eurotrucks2_server"
CONFIG_DIR="/home/gameplane/.local/share/Euro Truck Simulator 2"
mkdir -p "$CONFIG_DIR"

# Configure server_config.sii if token is available
if [ -f "$CONFIG_DIR/server_config.sii" ]; then
    sed -i "s/server_logon_token: .*/server_logon_token: \"$LOGON_TOKEN\"/" "$CONFIG_DIR/server_config.sii"
fi

echo "[gameplane-ets2] Starting Euro Truck Simulator 2 dedicated server..."

if [ -f "$SERVER_BIN" ]; then
    "$SERVER_BIN" -server "$@" &
    PID_GAME=$!
    wait "$PID_GAME" 2>/dev/null || true
else
    echo "[gameplane-ets2] Server binary not yet installed at $SERVER_BIN."
    echo "[gameplane-ets2] Entering diagnostic wait mode..."
    while true; do
        sleep 60 &
        wait $!
    done
fi
