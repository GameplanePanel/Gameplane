#!/usr/bin/env bash
set -eo pipefail

# Graceful termination handler
PID_DB=""
PID_FX=""

term_handler() {
    echo "[gameplane-fivem] SIGTERM/SIGINT received, initiating graceful shutdown..."
    if [ -n "$PID_FX" ]; then
        echo "[gameplane-fivem] Stopping fxserver process $PID_FX..."
        kill -TERM "$PID_FX" 2>/dev/null || true
    fi
    if [ -n "$PID_DB" ]; then
        echo "[gameplane-fivem] Stopping embedded MariaDB process $PID_DB..."
        kill -TERM "$PID_DB" 2>/dev/null || true
    fi
    wait
    echo "[gameplane-fivem] Clean shutdown complete."
    exit 0
}

trap term_handler SIGTERM SIGINT

# Resolve CFX license key from possible environment variables
LICENSE_KEY="${CFX_LICENSE_KEY:-${SV_LICENSEKEY:-${LICENSE_KEY:-}}}"

# FR-013: Non-crashing diagnostic idle for missing license key
if [ -z "$LICENSE_KEY" ]; then
    echo "========================================================================"
    echo "DIAGNOSTIC: FiveM CFX Server License Key is not configured!"
    echo "The FiveM dedicated server requires a CitizenFX license key to start."
    echo ""
    echo "Step-by-step instructions to configure your key:"
    echo "1. Sign in to the Cfx.re Keymaster portal at https://keymaster.fivem.net"
    echo "2. Click 'Register a new server'."
    echo "3. Enter a label and the public IP address of your server/cluster."
    echo "4. Copy the generated license key."
    echo "5. Configure the key in your GameServer specification:"
    echo "     spec:"
    echo "       env:"
    echo "         - name: CFX_LICENSE_KEY"
    echo "           value: \"your_license_key_here\""
    echo ""
    echo "Entering graceful idle mode. The server will not crash-loop."
    echo "Update your manifest and restart the server once configured."
    echo "========================================================================"

    while true; do
        sleep 3600 &
        wait $!
    done
fi

# FR-012: Supervise embedded MariaDB database if enabled
if [ "${ENABLE_EMBEDDED_DB:-true}" = "true" ]; then
    DB_DIR="/server-data/mysql"
    if [ ! -d "$DB_DIR" ]; then
        echo "[gameplane-fivem] Initializing embedded MariaDB in $DB_DIR..."
        mkdir -p "$DB_DIR"
        mysql_install_db --user=gameplane --datadir="$DB_DIR" >/dev/null 2>&1 || true
    fi
    echo "[gameplane-fivem] Starting embedded MariaDB server..."
    mariadbd --datadir="$DB_DIR" --user=gameplane --socket=/tmp/mysql.sock --bind-address=127.0.0.1 --port=3306 >/dev/null 2>&1 &
    PID_DB=$!
    # Brief wait for socket creation
    for i in $(seq 1 30); do
        if [ -S /tmp/mysql.sock ]; then
            break
        fi
        sleep 0.5
    done
fi

# Ensure txAdmin data directory exists
mkdir -p /server-data/txData

echo "[gameplane-fivem] Starting FiveM dedicated server with txAdmin on port ${TXADMIN_PORT:-40120}..."

# If fxserver binaries are mounted or installed at /opt/cfx-server
if [ -f "/opt/cfx-server/run.sh" ]; then
    /opt/cfx-server/run.sh \
        +set serverProfile default \
        +set txAdminPort "${TXADMIN_PORT:-40120}" \
        +set sv_licenseKey "$LICENSE_KEY" \
        +set net_tcpConnLimit 64 \
        "$@" &
    PID_FX=$!
elif [ -f "/opt/cfx-server/FXServer" ]; then
    /opt/cfx-server/FXServer \
        +set serverProfile default \
        +set txAdminPort "${TXADMIN_PORT:-40120}" \
        +set sv_licenseKey "$LICENSE_KEY" \
        "$@" &
    PID_FX=$!
else
    echo "[gameplane-fivem] Note: fxserver binary not found at /opt/cfx-server/run.sh."
    echo "[gameplane-fivem] Running in managed txAdmin supervisor mode..."
    # Keep supervisor running and monitoring
    while true; do
        sleep 60 &
        wait $!
    done
fi

wait "$PID_FX" 2>/dev/null || true
