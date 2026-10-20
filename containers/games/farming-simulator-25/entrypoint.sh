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
DEDICATED_SERVER_EXE="/serverdata/FarmingSimulator2025.exe"
WEB_SERVER_EXE="/serverdata/dedicatedServer.exe"

# If neither executable is found, provide diagnostic instructions
if [ ! -f "$DEDICATED_SERVER_EXE" ] && [ ! -f "$WEB_SERVER_EXE" ]; then
    echo "========================================================================"
    echo "DIAGNOSTIC: Farming Simulator 25 server files not detected in /serverdata"
    echo "The dedicated server requires the official FS25 server files to run."
    echo ""
    echo "Step-by-step instructions:"
    echo "1. Install or copy the Farming Simulator 25 Dedicated Server files into"
    echo "   the persistent volume mounted at /serverdata."
    echo "2. Ensure the directory contains FarmingSimulator2025.exe and"
    echo "   dedicatedServer.exe."
    echo "3. Configure your server settings in dedicatedServer.xml."
    echo ""
    echo "Entering graceful idle mode. The server will not crash-loop."
    echo "========================================================================"

    while true; do
        sleep 3600 &
        wait $!
    done
fi

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
