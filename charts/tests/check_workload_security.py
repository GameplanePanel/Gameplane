"""CI-only chart checks for game workload and credential admission."""
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


def check_namespace_separation():
    operator = {"gameNamespaces": ["games-other"], "controlPlaneNamespace": "control",
                "agentCASecretNamespace": "certificates", "moduleNamespace": "modules"}
    for namespace in ("games-main", "games-other", "control", "certificates", "modules", "management"):
        render({"gamesNamespace": "games-main", "operator": operator,
                "capture": {"enabled": True, "nodeDiagnostics": {"enabled": True, "namespace": namespace}}},
               namespace="management", failure="separate explicit")
    for invalid in ("", "*", "games,other"):
        render({"operator": {"gameNamespaces": [invalid]},
                "capture": {"enabled": True, "nodeDiagnostics": {"enabled": True}}},
               failure="explicit DNS-label namespace")
    objects = render({"gamesNamespace": "games-main", "operator": operator,
                      "capture": {"enabled": True, "nodeDiagnostics": {"enabled": True}}},
                     namespace="management")
    one(objects, "Namespace", "gameplane-diagnostics")


def check_admission_policies():
    """Check that token protection covers credential stores even when game Pod restrictions are disabled."""
    def bound_namespaces(objects, name, resource_rules):
        policy = one(objects, "ValidatingAdmissionPolicy", name)
        assert policy["apiVersion"] == "admissionregistration.k8s.io/v1"
        assert policy["spec"]["failurePolicy"] == "Fail"
        assert policy["spec"]["matchConstraints"]["resourceRules"] == resource_rules
        binding = one(objects, "ValidatingAdmissionPolicyBinding", name)["spec"]
        assert binding["policyName"] == name and binding["validationActions"] == ["Deny"]
        requirement, = binding["matchResources"]["namespaceSelector"]["matchExpressions"]
        assert requirement["key"] == "kubernetes.io/metadata.name" and requirement["operator"] == "In"
        return set(requirement["values"])

    games = {"games-main", "games-eu"}
    values = {"gamesNamespace": "games-main", "operator": {"gameNamespaces": ["games-eu", "games-main"],
              "agentCASecretNamespace": "certificates", "moduleNamespace": "modules"}}
    objects = render(values, namespace="panel")
    secret_rule = [{"apiGroups": [""], "apiVersions": ["v1"], "operations": ["CREATE", "UPDATE"],
                    "resources": ["secrets"], "scope": "Namespaced"}]
    protected = games | {"panel", "certificates", "modules"}
    assert bound_namespaces(objects, "bound-tokens.gameplane.panel", secret_rule) == protected | {"panel-credentials"}
    assert bound_namespaces(objects, "restricted-games.gameplane.panel", [
        {"apiGroups": [""], "apiVersions": ["v1"], "operations": ["CREATE"],
         "resources": ["pods"], "scope": "Namespaced"},
        {"apiGroups": [""], "apiVersions": ["v1"], "operations": ["UPDATE"],
         "resources": ["pods/ephemeralcontainers"], "scope": "Namespaced"},
    ]) == games
    assert not any(o["kind"] == "Namespace" and o["metadata"]["name"] == "games-eu" for o in objects)

    # A legacy workload profile can still be used during a data migration.
    # Persistent token issuance must stay blocked there, including remote operators.
    legacy = render({**values, "api": {"enabled": False}, "podSecurity": {"enforceRestricted": False}}, namespace="panel")
    assert bound_namespaces(legacy, "bound-tokens.gameplane.panel", secret_rule) == protected
    assert "pod-security.kubernetes.io/enforce" not in one(legacy, "Namespace", "games-main")["metadata"]["labels"]
    assert not any(o["metadata"]["name"] == "restricted-games.gameplane.panel" for o in legacy)
    # A live pre-1.30 cluster reports Namespace discovery without the GA policy.
    render(api_versions=["v1/Namespace"], failure="requires Kubernetes 1.30+")
    central = render({"api": {"standalone": True}, "operator": {"enabled": False}}, api_versions=["v1/Namespace"])
    assert not any(o["kind"].startswith("ValidatingAdmissionPolicy") for o in central)


def main():
    check_admission_policies()
    check_reuse_values()
    check_namespace_separation()
    defaults = render()
    assert not any(o["metadata"]["name"] == "gameplane-diagnostics" for o in defaults)
    games = one(defaults, "Namespace", "gameplane-games")
    assert games["metadata"]["labels"]["pod-security.kubernetes.io/enforce"] == "restricted"
    assert games["metadata"]["labels"]["pod-security.kubernetes.io/enforce-version"] == "v1.34"
    args = one(defaults, "Deployment", "gameplane-operator")["spec"]["template"]["spec"]["containers"][0]["args"]
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
