#!/usr/bin/env bash
set -eo pipefail

PID_XVFB=""
PID_WEB=""
PID_GAME=""

term_handler() {
    echo "[gameplane-fs25] SIGTERM/SIGINT received, initiating graceful shutdown..."
    if [ -n "$PID_GAME" ]; then
        echo "[gameplane-fs25] Stopping game process $PID_GAME..."
        kill -TERM "$PID_GAME" 2>/dev/null || true
    fi
    if [ -n "$PID_WEB" ]; then
        echo "[gameplane-fs25] Stopping web portal process $PID_WEB..."
        kill -TERM "$PID_WEB" 2>/dev/null || true
    fi
    if [ -n "$PID_XVFB" ]; then
        echo "[gameplane-fs25] Stopping virtual display $PID_XVFB..."
        kill -TERM "$PID_XVFB" 2>/dev/null || true
    fi
    wait
    echo "[gameplane-fs25] Clean shutdown complete."
    exit 0
}

trap term_handler SIGTERM SIGINT

# Persistent volume: the GameTemplate mounts the PVC at
# "/data/My Games/FarmingSimulator2025". The user uploads the server files
# (a licensed Windows install) into this volume.
DATA="${GP_DATA_DIR:-/data/My Games/FarmingSimulator2025}"

echo "[gameplane-fs25] Initializing headless Wine environment..."

# Initialize Wine prefix if needed
if [ ! -d "$WINEPREFIX/drive_c" ]; then
    wineboot --init >/dev/null 2>&1 || true
fi

# Edge case: Isolated X11 dummy display setup
export DISPLAY="${DISPLAY:-:99}"
rm -f "/tmp/.X99-lock" "/tmp/.X11-unix/X99" 2>/dev/null || true
Xvfb "$DISPLAY" -screen 0 1024x768x16 -nolisten tcp >/dev/null 2>&1 &
PID_XVFB=$!

# Wait briefly for X display initialization
for i in $(seq 1 10); do
    if xset q >/dev/null 2>&1; then
        break
    fi
    sleep 0.5
done

# Look for GIANTS dedicated server or web management portal
DEDICATED_SERVER_EXE="${DATA}/FarmingSimulator2025.exe"
WEB_SERVER_EXE="${DATA}/dedicatedServer.exe"

# If neither executable is found, there is nothing to run: log how to fix it
# and exit non-zero so the failure surfaces instead of idling forever.
if [ ! -f "$DEDICATED_SERVER_EXE" ] && [ ! -f "$WEB_SERVER_EXE" ]; then
    echo "========================================================================" >&2
    echo "ERROR: Farming Simulator 25 server files not found in ${DATA}" >&2
    echo "The dedicated server requires the official FS25 server files, which" >&2
    echo "Gameplane cannot download for you." >&2
    echo "" >&2
    echo "1. Upload your licensed Farming Simulator 25 server install" >&2
    echo "   (FarmingSimulator2025.exe, dedicatedServer.exe and the game data)" >&2
    echo "   to the server's volume with the Files tab or SFTP." >&2
    echo "2. Configure your server settings in dedicatedServer.xml." >&2
    echo "3. Start the server again." >&2
    echo "========================================================================" >&2
    exit 1
fi

cd "$DATA"

# FR-012: Start web management portal alongside the dedicated server
if [ -f "$WEB_SERVER_EXE" ]; then
    echo "[gameplane-fs25] Starting GIANTS web management portal on port ${WEB_PORT:-8080}..."
    wine "$WEB_SERVER_EXE" &
    PID_WEB=$!
fi

# Start game process if present
if [ -f "$DEDICATED_SERVER_EXE" ]; then
    echo "[gameplane-fs25] Starting Farming Simulator 25 dedicated server on port ${GAME_PORT:-10823}..."
    wine "$DEDICATED_SERVER_EXE" -server -port "${GAME_PORT:-10823}" "$@" &
    PID_GAME=$!
    wait "$PID_GAME" 2>/dev/null || true
elif [ -n "$PID_WEB" ]; then
    wait "$PID_WEB" 2>/dev/null || true
fi
