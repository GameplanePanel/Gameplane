"""CI-only structural checks for the optional gateway chart and API-disabled mode."""
from pathlib import Path
import subprocess
import tempfile

import yaml

ROOT = Path(__file__).resolve().parents[2]
CHART = ROOT / "charts" / "gameplane"


def render(values=None, failure=None, upgrade=False, namespace="gameplane-system", release="gameplane", include_crds=False, chart=CHART):
    """Return rendered objects, or assert the expected Helm validation failure.

    Accept a source chart or packaged archive; native CRDs are opt-in to match
    Helm template's default behavior.
    """
    with tempfile.TemporaryDirectory() as directory:
        values_path = Path(directory) / "values.yaml"
        values_path.write_text(yaml.safe_dump(values or {}), encoding="utf-8")
        result = subprocess.run(
            ["helm", "template", release, str(chart), "--namespace", namespace, "--values", str(values_path)]
            + (["--is-upgrade"] if upgrade else [])
            + (["--include-crds"] if include_crds else []),
            capture_output=True, text=True, check=False,
        )
    if failure:
        assert result.returncode != 0, "invalid gateway configuration rendered successfully"
        assert failure in result.stderr, result.stderr
        return []
    assert result.returncode == 0, result.stderr
    return [obj for obj in yaml.safe_load_all(result.stdout) if obj]


def one(objects, kind, name):
    matching = [obj for obj in objects if obj["kind"] == kind and obj["metadata"]["name"] == name]
    assert len(matching) == 1, (kind, name, len(matching))
    return matching[0]


def settings():
    return {
        "enabled": True, "clusterID": "remote-1", "peerURI": "spiffe://example/central-api",
        "serverTLSSecret": "gateway-server", "centralClientCASecret": "central-client-ca",
        "networkPolicy": {"peerCIDRs": ["10.20.30.40/32"], "apiServerCIDRs": ["10.0.0.10/32"]},
    }


def capture_control_policy(objects, api_enabled=True, namespace="gameplane-system", games_namespace="gameplane-games", release="gameplane"):
    policy = one(objects, "NetworkPolicy", "allow-api-to-capture")
    assert policy["metadata"]["namespace"] == games_namespace
    sources = ["gameplane-api", "gameplane-operator"] if api_enabled else ["gameplane-operator"]
    peers = []
    for name in sources:
        labels = {"app.kubernetes.io/name": name, "app.kubernetes.io/instance": release}
        # Exact selectors must also select the actual chart-managed control pods.
        pod_labels = one(objects, "Deployment", name)["spec"]["template"]["metadata"]["labels"]
        assert labels.items() <= pod_labels.items()
        peers.append({"namespaceSelector": {"matchLabels": {"kubernetes.io/metadata.name": namespace}},
                      "podSelector": {"matchLabels": labels}})
    assert policy["spec"] == {
        "podSelector": {"matchLabels": {"app.kubernetes.io/name": "gameplane-game"}},
        "policyTypes": ["Ingress"],
        "ingress": [{"from": peers, "ports": [{"protocol": "TCP", "port": 9091}]}],
    }
    agent = one(objects, "NetworkPolicy", "allow-api-to-agent")["spec"]
    assert agent["ingress"][0]["ports"] == [{"protocol": "TCP", "port": 8090}]


def main():
    defaults = render()
    ingress = one(defaults, "Ingress", "gameplane")
    assert ingress["metadata"]["annotations"]["nginx.ingress.kubernetes.io/proxy-body-size"] == "512m"
    assert ingress["spec"]["rules"][0]["http"]["paths"][0]["backend"]["service"]["name"] == "gameplane-web"
    assert not any(obj["metadata"]["name"] == "gameplane-gateway" for obj in defaults)
    one(defaults, "Deployment", "gameplane-api")
    one(defaults, "PersistentVolumeClaim", "gameplane-api-data")
    assert not any(obj["kind"] == "NetworkPolicy" and obj["metadata"]["name"] == "allow-api-to-capture" for obj in defaults)

    gateway = settings()
    gateway["namespaces"] = ["games-one", "games-two"]
    objects = render({
        "gateway": gateway, "api": {"enabled": False, "telemetry": {"receiver": {"enabled": True}}},
        "serviceMonitors": {"enabled": True},
        "clusterOps": {"enabled": True},
    }, upgrade=True)
    assert not any(obj["metadata"]["name"] in {"gameplane-api", "gameplane-api-data", "gameplane-web", "gameplane-strategy-migrate", "gameplane-telemetry-receiver"} for obj in objects)
    assert not any(obj["kind"] == "Ingress" for obj in objects)
    one(objects, "Deployment", "gameplane-operator")
    one(objects, "Secret", "gameplane-agent-ca")
    one(objects, "Secret", "gameplane-agent-client")
    one(objects, "ServiceMonitor", "gameplane-operator")
    job = one(objects, "Job", "gameplane-crd-apply")
    assert "initContainers" not in job["spec"]["template"]["spec"]

    deployment = one(objects, "Deployment", "gameplane-gateway")
    pod = deployment["spec"]["template"]["spec"]
    assert pod["serviceAccountName"] == "gameplane-gateway"
    container = pod["containers"][0]
    assert container["args"][0] == "gateway"
    assert "--max-request-duration=0s" in container["args"]
    assert "--namespaces=games-one,games-two" in container["args"]
    assert "--peer-uri=spiffe://example/central-api" in container["args"]
    assert container["readinessProbe"]["tcpSocket"]["port"] == "mtls"
    assert container["livenessProbe"]["tcpSocket"]["port"] == "mtls"
    assert container["securityContext"]["readOnlyRootFilesystem"] is True
    for volume in pod["volumes"]:
        assert "secret" in volume
        assert all(item["key"] != "ca.key" for item in volume["secret"]["items"])
    service = one(objects, "Service", "gameplane-gateway")
    assert service["spec"]["type"] == "ClusterIP"
    assert service["spec"]["ports"] == [{"name": "mtls", "port": 8443, "targetPort": "mtls"}]
    roles = [obj for obj in objects if obj["kind"] == "Role" and obj["metadata"]["name"] == "gameplane-gateway-read"]
    assert {obj["metadata"]["namespace"] for obj in roles} == {"games-one", "games-two"}
    for role in roles:
        assert role["rules"] == [
            {"apiGroups": ["gameplane.local"], "resources": ["gameservers", "networkcaptures"], "verbs": ["get"]},
            {"apiGroups": [""], "resources": ["services"], "verbs": ["get"]},
        ]
    policy = one(objects, "NetworkPolicy", "gameplane-gateway")["spec"]
    assert policy["policyTypes"] == ["Ingress", "Egress"]
    assert policy["ingress"] == [{"from": [{"ipBlock": {"cidr": "10.20.30.40/32"}}], "ports": [{"protocol": "TCP", "port": 8443}]}]
    assert {port["port"] for rule in policy["egress"] for port in rule["ports"]} == {53, 443, 6443, 8090, 9091}
    for ingress in [obj for obj in objects if obj["kind"] == "NetworkPolicy" and obj["metadata"]["name"] == "gameplane-gateway-to-agent"]:
        assert {port["port"] for port in ingress["spec"]["ingress"][0]["ports"]} == {8090, 9091}
    assert "--capture-enabled=false" in container["args"]
    enabled = render({"gateway": settings(), "capture": {"enabled": True, "defaultRetentionSeconds": 120, "maxRetentionSeconds": 3600, "defaultMaxDurationSeconds": 90, "defaultMaxSizeBytes": 1048576},
                      "networkPolicies": {"probePorts": [{"protocol": "TCP", "port": 8090}]}})
    capture_control_policy(enabled)
    probes = one(enabled, "NetworkPolicy", "allow-kubelet-probes")["spec"]
    assert probes["ingress"][0]["ports"] == [{"protocol": "TCP", "port": 8090}]
    capture_args = one(enabled, "Deployment", "gameplane-gateway")["spec"]["template"]["spec"]["containers"][0]["args"]
    assert "--capture-enabled=true" in capture_args
    assert "--capture-default-retention=120" in capture_args
    assert "--capture-max-retention=3600" in capture_args
    assert "--capture-default-max-duration=90" in capture_args
    assert "--capture-default-max-size=1048576" in capture_args
    assert len([obj for obj in objects if obj["kind"] == "NetworkPolicy" and obj["metadata"]["name"] == "gameplane-gateway-to-agent"]) == 2

    remote_capture = render({"api": {"enabled": False}, "capture": {"enabled": True}, "gamesNamespace": "remote-games"},
                            namespace="remote-system", release="remote-plane")
    capture_control_policy(remote_capture, api_enabled=False, namespace="remote-system", games_namespace="remote-games", release="remote-plane")
    # Helm removes a default key overridden with null. This represents legacy
    # --reuse-values releases without api.enabled: both API and peer stay enabled.
    legacy_capture = render({"api": {"enabled": None}, "capture": {"enabled": True}}, upgrade=True)
    capture_control_policy(legacy_capture)

    selector_gateway = settings()
    selector_gateway["networkPolicy"]["peerCIDRs"] = []
    selector_gateway["networkPolicy"]["peerSelectors"] = [{
        "namespaceSelector": {"matchLabels": {"kubernetes.io/metadata.name": "relay"}},
        "podSelector": {"matchLabels": {"app": "private-relay"}},
    }]
    selector_objects = render({"gateway": selector_gateway, "networkPolicies": {"enabled": False}, "capture": {"enabled": True}})
    selector_policy = one(selector_objects, "NetworkPolicy", "gameplane-gateway")["spec"]
    assert selector_policy["ingress"][0]["from"] == selector_gateway["networkPolicy"]["peerSelectors"]
    one(selector_objects, "Role", "gameplane-gateway-read")  # defaults to gamesNamespace
    assert not any(obj["kind"] == "NetworkPolicy" and obj["metadata"]["name"] == "gameplane-gateway-to-agent" for obj in selector_objects)
    assert not any(obj["kind"] == "NetworkPolicy" and obj["metadata"]["name"] == "allow-api-to-capture" for obj in selector_objects)

    for field, message in [("clusterID", "gateway.clusterID"), ("peerURI", "gateway.peerURI"), ("serverTLSSecret", "gateway.serverTLSSecret"), ("centralClientCASecret", "gateway.centralClientCASecret")]:
        invalid = settings()
        invalid[field] = ""
        render({"gateway": invalid}, failure=message)
    invalid = settings()
    invalid["networkPolicy"]["peerCIDRs"] = []
    render({"gateway": invalid}, failure="no peer is trusted by default")
    invalid = settings()
    invalid["networkPolicy"]["apiServerCIDRs"] = []
    render({"gateway": invalid}, failure="apiServerCIDRs")
    invalid = settings()
    invalid["networkPolicy"]["peerCIDRs"] = ["0.0.0.0/0"]
    render({"gateway": invalid}, failure="not the public internet")
    invalid = settings()
    invalid["networkPolicy"]["peerSelectors"] = [{}]
    render({"gateway": invalid}, failure="both namespaceSelector and podSelector")
    invalid = settings()
    invalid["namespaces"] = ["*"]
    render({"gateway": invalid}, failure="explicit DNS-label namespace names")
    print("Gateway chart and API-disabled render assertions passed")


if __name__ == "__main__":
    main()
