"""CI-only smoke test against the real Compose images, without Kubernetes."""
import base64
import hashlib
from http.cookies import SimpleCookie
import json
import os
from pathlib import Path
import secrets
import sqlite3
import subprocess
import tempfile
import time
import urllib.error
import urllib.request


ROOT = Path(__file__).resolve().parents[2]
COMPOSE = ["docker", "compose", "-f", str(ROOT / "deploy/standalone/compose.yaml")]
ORIGIN = "http://127.0.0.1:" + os.environ.get("GAMEPLANE_PORT", "8080")


def compose(*args, **kwargs):
    return subprocess.run(COMPOSE + list(args), check=True, text=True, **kwargs)


def request(path, data=None, cookie="", method=None):
    headers = {"Accept": "application/json", "Cookie": cookie}
    if data is not None:
        headers["Content-Type"] = "application/json"
    if cookie and (data is not None or method not in (None, "GET")):
        cookies = SimpleCookie(cookie)
        headers["X-Gameplane-CSRF"] = cookies["gameplane_csrf"].value
    req = urllib.request.Request(ORIGIN + path, headers=headers,
                                 data=json.dumps(data).encode() if data is not None else None,
                                 method=method)
    with urllib.request.urlopen(req, timeout=10) as response:
        return response.read(), response.headers


def wait_ready():
    deadline = time.monotonic() + 120
    while time.monotonic() < deadline:
        try:
            request("/healthz")
            return
        except (OSError, urllib.error.URLError):
            time.sleep(1)
    raise AssertionError("standalone API did not become healthy")


def login(password):
    body, headers = request("/auth/login", {"username": "standalone-admin", "password": password})
    assert json.loads(body)["user"]["role"] == "admin"
    cookies = SimpleCookie()
    for value in headers.get_all("Set-Cookie", []):
        cookies.load(value)
    assert "gameplane_session" in cookies
    # This isolated CI endpoint is loopback HTTP; send Secure cookies explicitly.
    # Production deployments retain HTTPS and normal browser cookie enforcement.
    return "; ".join(f"{key}={value.value}" for key, value in cookies.items())


def assert_empty(cookie):
    clusters = json.loads(request("/clusters", cookie=cookie)[0])
    assert clusters["items"] == [], clusters
    for kind in ("inventory", "servers", "backups", "placements"):
        fleet = json.loads(request("/fleet/" + kind, cookie=cookie)[0])
        assert fleet["items"] == [], fleet
        assert not fleet["partial"], fleet
        assert not fleet.get("issues"), fleet
    user = json.loads(request("/users/me", cookie=cookie)[0])
    assert user["username"] == "standalone-admin"


def key_digest(container, path="/keys/panel.key"):
    with tempfile.TemporaryDirectory() as directory:
        key = Path(directory) / "panel.key"
        subprocess.run(["docker", "cp", container + ":" + path, str(key)], check=True)
        return hashlib.sha256(key.read_bytes()).digest()


def registration(token):
    # No Kubernetes dependency: the registration is valid but its target is
    # deliberately unreachable inside the API container.
    kubeconfig = json.dumps({
        "apiVersion": "v1", "kind": "Config", "current-context": "smoke",
        "clusters": [{"name": "smoke", "cluster": {"server": "https://192.0.2.1:65534"}}],
        "users": [{"name": "smoke", "user": {"token": token}}],
        "contexts": [{"name": "smoke", "context": {"cluster": "smoke", "user": "smoke"}}],
    })
    return {"name": "smoke-remote", "displayName": "Persistent smoke remote", "kubeconfig": kubeconfig}


def assert_registered(cookie, token):
    body = request("/clusters", cookie=cookie)[0]
    items = json.loads(body)["items"]
    assert len(items) == 1, items
    assert items[0]["name"] == "smoke-remote", items
    assert items[0]["displayName"] == "Persistent smoke remote", items
    assert token.encode() not in body, "registration response exposed credentials"


def assert_encrypted_registration(container, token):
    # Caller stops the API first so the database and any WAL form one stable
    # snapshot. Read only the copy, never the live volume.
    with tempfile.TemporaryDirectory() as directory:
        subprocess.run(["docker", "cp", container + ":/data/.", directory], check=True)
        with sqlite3.connect(str(Path(directory) / "gameplane.db")) as database:
            registration_payload = database.execute(
                "SELECT payload FROM management_objects WHERE kind = 'clusters' AND name = ?",
                ("smoke-remote",),
            ).fetchone()[0]
            credential_name = json.loads(registration_payload)["spec"]["kubeconfigSecret"]["name"]
            rows = database.execute(
                "SELECT kind, name, payload FROM management_objects WHERE name IN (?, ?)",
                ("smoke-remote", credential_name),
            ).fetchall()
        assert {(kind, name) for kind, name, _ in rows} == {
            ("clusters", "smoke-remote"), ("secrets", credential_name),
        }, "registration and credential must both persist"
        payload = next(payload for kind, _, payload in rows if kind == "secrets")
        envelope, key_id, encoded = payload.split(".", 2)
        assert envelope == "gpk1" and len(key_id) == 64
        assert len(bytes.fromhex(key_id)) == 32, "ciphertext must identify its encryption key"
        sealed = base64.b64decode(encoded, validate=True)
        assert len(sealed) > 28, "credential ciphertext must include nonce and authentication tag"
        try:
            json.loads(sealed)
        except (ValueError, UnicodeDecodeError):
            pass
        else:
            raise AssertionError("credential payload is unencrypted JSON")
        for path in Path(directory).glob("gameplane.db*"):
            assert token.encode() not in path.read_bytes(), "database snapshot exposed credentials"


def main():
    config = json.loads(compose("config", "--format", "json", capture_output=True).stdout)
    assert set(config["services"]) == {"gameplane-api", "web"}
    api = config["services"]["gameplane-api"]
    assert "KUBECONFIG" not in api.get("environment", {})
    assert {volume["target"] for volume in api["volumes"]} == {"/data", "/keys"}
    assert not api.get("ports")
    assert api["read_only"] is True
    assert api["environment"]["GAMEPLANE_STANDALONE"] == "true"
    wait_ready()
    container = compose("ps", "-q", "gameplane-api", capture_output=True).stdout.strip()
    inspection = json.loads(subprocess.run(["docker", "inspect", container], check=True, text=True, capture_output=True).stdout)[0]
    assert inspection["Config"]["User"] == "65532:65532"
    assert not any("serviceaccount" in mount["Destination"] or "docker.sock" in mount["Destination"] for mount in inspection["Mounts"])
    original_key = key_digest(container)
    password = secrets.token_urlsafe(24)
    compose("exec", "-T", "gameplane-api", "/api", "bootstrap-admin", "--username", "standalone-admin", "--password-stdin", input=password + "\n")
    cookie = login(password)
    assert_empty(cookie)
    token = secrets.token_urlsafe(32)
    remote = registration(token)
    request("/clusters", remote, cookie=cookie)
    assert_registered(cookie, token)
    compose("stop", "gameplane-api")
    assert_encrypted_registration(container, token)
    compose("up", "-d", "--no-build", "--force-recreate", "gameplane-api")
    wait_ready()
    container = compose("ps", "-q", "gameplane-api", capture_output=True).stdout.strip()
    assert key_digest(container) == original_key, "panel key changed on container replacement"
    # Startup validates all persisted credentials using the original key.
    # Metadata and the existing session must survive alongside those credentials.
    assert_registered(cookie, token)
    cookie = login(password)
    assert_registered(cookie, token)
    token = secrets.token_urlsafe(32)
    remote = registration(token)
    request("/clusters/smoke-remote/kubeconfig", {"kubeconfig": remote["kubeconfig"]}, cookie=cookie, method="PUT")
    compose("stop", "gameplane-api")
    assert_encrypted_registration(container, token)
    rotation = ("run", "--rm", "--no-deps", "--entrypoint", "/api", "gameplane-api",
                "rotate-panel-key", "--old-key-file", "/keys/panel.key", "--new-key-file", "/keys/panel-next.key")
    compose(*rotation)
    compose(*rotation)  # The exact retry is safe after an unknown commit outcome.
    os.environ["GAMEPLANE_PANEL_KEY_FILE"] = "/keys/panel-next.key"
    compose("up", "-d", "--no-build", "--force-recreate", "gameplane-api")
    wait_ready()
    container = compose("ps", "-q", "gameplane-api", capture_output=True).stdout.strip()
    assert key_digest(container) == original_key, "rotation modified the historical backup key"
    assert key_digest(container, "/keys/panel-next.key") != original_key
    assert_registered(cookie, token)
    # Exercise provisioned mode against a genuinely read-only key volume.
    with tempfile.TemporaryDirectory() as directory:
        override = Path(directory) / "provisioned.json"
        override.write_text(json.dumps({"services": {"gameplane-api": {
            "environment": {"GAMEPLANE_PANEL_KEY_PROVISIONED": "true"},
            "volumes": [{"type": "volume", "source": "panel-keys", "target": "/keys", "read_only": True}],
        }}}))
        subprocess.run(COMPOSE + ["-f", str(override), "up", "-d", "--no-build", "--force-recreate", "gameplane-api"], check=True)
        wait_ready()
        assert_registered(cookie, token)
    request("/clusters/smoke-remote", cookie=cookie, method="DELETE")
    assert_empty(cookie)
    # An orphaned credential would make this second registration return 409.
    request("/clusters", remote, cookie=cookie)
    request("/clusters/smoke-remote", cookie=cookie, method="DELETE")
    assert_empty(cookie)
    print("Standalone Compose: bootstrap, encrypted registration, kubeconfig/master-key rotation, read-only provisioned key, restart and cleanup passed")


if __name__ == "__main__":
    main()
