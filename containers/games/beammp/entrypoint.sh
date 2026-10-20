#!/usr/bin/env bash
set -eo pipefail

PID_GAME=""

term_handler() {
    echo "[gameplane-beammp] SIGTERM/SIGINT received, initiating graceful shutdown..."
    if [ -n "$PID_GAME" ]; then
        echo "[gameplane-beammp] Stopping BeamMP-Server process $PID_GAME..."
        kill -TERM "$PID_GAME" 2>/dev/null || true
    fi
    wait
    echo "[gameplane-beammp] Clean shutdown complete."
    exit 0
}

trap term_handler SIGTERM SIGINT

# Resolve auth key from environment
BEAM_KEY="${BEAMMP_AUTH_KEY:-${AUTH_KEY:-}}"

# FR-013: Non-crashing diagnostic idle for missing auth key
if [ -z "$BEAM_KEY" ]; then
    echo "========================================================================"
    echo "DIAGNOSTIC: BeamMP Server AuthKey is not configured!"
    echo "The BeamMP dedicated server requires a valid authentication key from"
    echo "the BeamMP community portal to register on the master list."
    echo ""
    echo "Step-by-step instructions to obtain and configure your key:"
    echo "1. Sign in to the BeamMP server portal at https://beammp.com"
    echo "2. Navigate to the 'Keys' management section."
    echo "3. Generate a new server authentication key."
    echo "4. Copy the generated key."
    echo "5. Configure the key in your GameServer specification:"
    echo "     spec:"
    echo "       env:"
    echo "         - name: BEAMMP_AUTH_KEY"
    echo "           value: \"your_beammp_key_here\""
    echo ""
    echo "Entering graceful idle mode. The server will not crash-loop."
    echo "Update your manifest and restart the server once configured."
    echo "========================================================================"

    while true; do
        sleep 3600 &
        wait $!
    done
fi

CONFIG_FILE="/serverdata/ServerConfig.toml"

# Generate or update ServerConfig.toml with auth key and port
if [ ! -f "$CONFIG_FILE" ]; then
    cat <<EOF > "$CONFIG_FILE"
[General]
AuthKey = "$BEAM_KEY"
Port = ${PORT:-30814}
Name = "Gameplane BeamMP Server"
Description = "BeamMP Server powered by Gameplane"
MaxPlayers = 10
Private = false
Debug = false
Map = "/levels/gridmap_v2/info.json"
ResourceFolder = "Resources"
EOF
else
    # Update AuthKey in existing configuration
    sed -i "s/^AuthKey = .*/AuthKey = \"$BEAM_KEY\"/" "$CONFIG_FILE"
fi

SERVER_BIN="/opt/beammp/BeamMP-Server"
if [ ! -f "$SERVER_BIN" ] && [ -f "/serverdata/BeamMP-Server" ]; then
    SERVER_BIN="/serverdata/BeamMP-Server"
fi

echo "[gameplane-beammp] Starting BeamMP dedicated server on port ${PORT:-30814}..."

if [ -f "$SERVER_BIN" ]; then
    "$SERVER_BIN" "$@" &
    PID_GAME=$!
    wait "$PID_GAME" 2>/dev/null || true
else
    echo "[gameplane-beammp] Note: BeamMP-Server binary not found at $SERVER_BIN."
    echo "[gameplane-beammp] Entering diagnostic wait mode..."
    while true; do
        sleep 60 &
        wait $!
    done
fi
