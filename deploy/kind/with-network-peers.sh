#!/usr/bin/env bash
# Run the Cilium bucket with reachable, off-cluster endpoints on kind's bridge.
set -euo pipefail
node="$(kind get nodes --name "${CLUSTER:?}" | head -n1)"
[[ -n "$node" && "$(docker inspect --format '{{index .Config.Labels "io.x-k8s.kind.cluster"}}' "$node")" == "$CLUSTER" ]]
scratch="$(mktemp -d)"
peer="" repo="" route_added=false
metadata=169.254.169.253
cleanup() {
    if [[ "$route_added" == true ]]; then docker exec "$node" ip route del "$metadata/32" via "$GAMEPLANE_NET_PEER_IP" || true; fi
    if [[ -n "$repo" ]]; then docker rm -f "$repo" >/dev/null || true; fi
    if [[ -n "$peer" ]]; then docker rm -f "$peer" >/dev/null || true; fi
    rm -rf "$scratch"
}
trap cleanup EXIT
openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj /CN=kind-private-fixture \
    -keyout "$scratch/tls.key" -out "$scratch/tls.crt" >/dev/null 2>&1
cat >"$scratch/listen.py" <<'PY'
import http.server, ssl, threading

class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b"isolated-kind-peer\n")

metadata = http.server.ThreadingHTTPServer(("169.254.169.253", 80), Handler)
threading.Thread(target=metadata.serve_forever, daemon=True).start()
tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
tls.load_cert_chain("/fixture/tls.crt", "/fixture/tls.key")
https = http.server.ThreadingHTTPServer(("0.0.0.0", 443), Handler)
https.socket = tls.wrap_socket(https.socket, server_side=True)
https.serve_forever()
PY
peer="$(docker run --detach --network kind --cap-add NET_ADMIN --volume "$scratch:/fixture:ro" \
    python:3.14-alpine sh -ec 'ip addr add 169.254.169.253/32 dev lo; exec python /fixture/listen.py')"
GAMEPLANE_NET_PEER_IP="$(docker inspect --format '{{(index .NetworkSettings.Networks "kind").IPAddress}}' "$peer")"
[[ -n "$GAMEPLANE_NET_PEER_IP" ]]
# Route only this alternate metadata address inside the disposable node.
# The real cloud metadata address is never used.
docker exec "$node" ip route add "$metadata/32" via "$GAMEPLANE_NET_PEER_IP"
route_added=true
repo="$(docker run --detach --network "container:$peer" --entrypoint rest-server \
    restic/rest-server:0.14.0 --listen :8000 --no-auth --path /tmp/repository)"
GAMEPLANE_NET_NODE_IP="$(docker inspect --format '{{(index .NetworkSettings.Networks "kind").IPAddress}}' "$node")"
export GAMEPLANE_NET_PEER_IP GAMEPLANE_NET_NODE_IP
export GAMEPLANE_NET_PEER_CONTAINER="$peer"
"$@"
