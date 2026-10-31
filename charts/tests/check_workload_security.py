"""CI-only chart checks for the opt-in Restricted workload and node exception."""
from pathlib import Path
import shutil
import tempfile

import yaml

from check_gateway import CHART, one, render, settings


def check_reuse_values():
    with tempfile.TemporaryDirectory() as directory:
        chart = Path(directory) / "chart"
        shutil.copytree(CHART, chart)
        values_file = chart / "values.yaml"
        old_values = yaml.safe_load(values_file.read_text())
        old_values["capture"].pop("nodeDiagnostics", None)
        values_file.write_text(yaml.safe_dump(old_values))
        enabled = {"capture": {"enabled": True, "nodeDiagnostics": {"enabled": True}}}
        objects = render(enabled, upgrade=True, chart=chart)
        one(objects, "Namespace", "gameplane-diagnostics")
        quota = one(objects, "ResourceQuota", "gameplane-diagnostics")["spec"]["hard"]
        assert quota == {"persistentvolumeclaims": "10", "requests.storage": "10Gi", "pods": "20", "count/jobs.batch": "10"}
        args = one(objects, "Deployment", "gameplane-operator")["spec"]["template"]["spec"]["containers"][0]["args"]
        assert "--diagnostics-namespace=gameplane-diagnostics" in args
        enabled["capture"]["nodeDiagnostics"]["maxCaptures"] = 0
        render(enabled, upgrade=True, chart=chart, failure="maxCaptures must be positive")
        enabled["capture"]["nodeDiagnostics"].pop("maxCaptures")
        enabled["capture"]["nodeDiagnostics"]["namespace"] = ""
        render(enabled, upgrade=True, chart=chart, failure="separate explicit")


def main():
    check_reuse_values()
    defaults = render()
    assert not any(o["metadata"]["name"] == "gameplane-diagnostics" for o in defaults)
    objects = render({"podSecurity": {"enforceRestricted": True}})
    games = one(objects, "Namespace", "gameplane-games")
    assert games["metadata"]["labels"]["pod-security.kubernetes.io/enforce"] == "restricted"
    assert games["metadata"]["labels"]["pod-security.kubernetes.io/enforce-version"] == "v1.34"
    args = one(objects, "Deployment", "gameplane-operator")["spec"]["template"]["spec"]["containers"][0]["args"]
    assert "--restricted-workloads=true" in args  # even when capture is disabled
    assert not any(a.startswith("--diagnostics-namespace=") for a in args)
    values = {"podSecurity": {"enforceRestricted": True, "version": "v1.35"},
              "capture": {"enabled": True, "nodeDiagnostics": {"enabled": True}},
              "gateway": settings()}
    objects = render(values)
    assert one(objects, "Namespace", "gameplane-games")["metadata"]["labels"]["pod-security.kubernetes.io/enforce-version"] == "v1.35"
    assert one(objects, "Namespace", "gameplane-diagnostics")["metadata"]["labels"]["pod-security.kubernetes.io/enforce"] == "privileged"
    quota = one(objects, "ResourceQuota", "gameplane-diagnostics")["spec"]["hard"]
    assert quota["persistentvolumeclaims"] == "10" and quota["requests.storage"] == "10Gi"
    role = one(objects, "Role", "gameplane-diagnostics")
    assert role["metadata"]["namespace"] == "gameplane-diagnostics"
    assert all("*" not in r["resources"] for r in role["rules"])
    policy = one(objects, "NetworkPolicy", "gameplane-diagnostics")["spec"]
    assert policy["egress"] == [] and policy["ingress"][0]["ports"] == [{"protocol": "TCP", "port": 9091}]
    one(objects, "NetworkPolicy", "gameplane-gateway-diagnostics")
    for namespace in ("gameplane-games", "gameplane-system", "../host"):
        render({"capture": {"enabled": True, "nodeDiagnostics": {"enabled": True, "namespace": namespace}}}, failure="separate explicit")
    render({"capture": {"nodeDiagnostics": {"enabled": True}}}, failure="requires capture.enabled")
    render({"podSecurity": {"version": "latest"}}, failure="must pin")


if __name__ == "__main__":
    main()
