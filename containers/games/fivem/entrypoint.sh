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

# Persistent volume: the GameTemplate mounts the PVC at /server-data.
DATA="${GP_DATA_DIR:-/server-data}"

# Pinned FXServer artifact (recommended build 38435). Override FXSERVER_URL and
# FXSERVER_SHA256 together to move to another build; the sha256 is always verified.
FXSERVER_URL="${FXSERVER_URL:-https://runtime.fivem.net/artifacts/fivem/build_proot_linux/master/38435-5c3065c261fb3ba82032fee6803f247198fec221/fx.tar.xz}"
FXSERVER_SHA256="${FXSERVER_SHA256:-1b58507f68086ca3578c187f31e7b4a2259b7192d76b0c80cfc1af682fdf2be9}"
FXSERVER_DIR="${DATA}/fxserver"

# install_fxserver downloads the pinned artifact into the volume, verifies its
# sha256 and extracts it. Failures are fatal so the pod surfaces them.
install_fxserver() {
    local archive="${DATA}/.fx.tar.xz.part"
    echo "[gameplane-fivem] Downloading FXServer artifact into ${FXSERVER_DIR}..."
    local staging="${DATA}/.fxserver.tmp"
    mkdir -p "$DATA"
    rm -rf "$staging"
    mkdir -p "$staging"
    if ! curl -fsSL --retry 3 --retry-delay 5 -o "$archive" "$FXSERVER_URL"; then
        rm -f "$archive"
        echo "[gameplane-fivem] ERROR: failed to download FXServer from $FXSERVER_URL" >&2
        exit 1
    fi
    if ! echo "${FXSERVER_SHA256}  ${archive}" | sha256sum -c - >/dev/null 2>&1; then
        rm -f "$archive"
        echo "[gameplane-fivem] ERROR: FXServer artifact sha256 mismatch (expected ${FXSERVER_SHA256})" >&2
        exit 1
    fi
    if ! tar -xJf "$archive" -C "$staging"; then
        rm -rf "$archive" "$staging"
        echo "[gameplane-fivem] ERROR: failed to extract FXServer artifact" >&2
        exit 1
    fi
    rm -f "$archive"
    # Publish only a complete extract, so a crash mid-extract never leaves a
    # truncated tree that looks installed.
    rm -rf "$FXSERVER_DIR"
    if ! mv "$staging" "$FXSERVER_DIR"; then
        rm -rf "$staging"
        echo "[gameplane-fivem] ERROR: failed to move FXServer into ${FXSERVER_DIR}" >&2
        exit 1
    fi
}

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
    DB_DIR="${DATA}/mysql"
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
    # A MariaDB major upgrade (10.11 -> 11.8 with the Alpine 3.24 base) leaves
    # existing system tables on the old layout. mariadb-upgrade brings them
    # current and is a no-op when they already are. mysql_install_db --user
    # created gameplane@localhost with unix_socket auth, matching uid 1000.
    # The socket can appear before MariaDB accepts authenticated connections,
    # so retry briefly. A final failure only logs: the next start retries it.
    if [ -S /tmp/mysql.sock ]; then
        upgraded=false
        for i in $(seq 1 10); do
            if mariadb-upgrade --socket=/tmp/mysql.sock --user=gameplane >/dev/null 2>&1; then
                upgraded=true
                break
            fi
            sleep 1
        done
        if [ "$upgraded" != true ]; then
            echo "[gameplane-fivem] mariadb-upgrade failed after 10 attempts; continuing with existing system tables"
        fi
    fi
fi

# Ensure txAdmin data directory exists
mkdir -p "${DATA}/txData"

# Install FXServer into the mounted volume when missing.
if [ ! -f "${FXSERVER_DIR}/run.sh" ]; then
    install_fxserver
fi
if [ ! -f "${FXSERVER_DIR}/run.sh" ]; then
    echo "[gameplane-fivem] ERROR: FXServer binary not found at ${FXSERVER_DIR}/run.sh after install" >&2
    exit 1
fi

echo "[gameplane-fivem] Starting FiveM dedicated server with txAdmin on port ${TXADMIN_PORT:-40120}..."

bash "${FXSERVER_DIR}/run.sh" \
    +set serverProfile default \
    +set txDataPath "${DATA}/txData" \
    +set txAdminPort "${TXADMIN_PORT:-40120}" \
    +set sv_licenseKey "$LICENSE_KEY" \
    +set net_tcpConnLimit 64 \
    "$@" &
PID_FX=$!

wait "$PID_FX" 2>/dev/null || true
