"""CI-only checks for the operator's namespace-local Secret authority."""
from pathlib import Path

import yaml

from check_gateway import one, render


def allows_secrets(rule):
    return "" in rule.get("apiGroups", []) and any(
        resource in rule.get("resources", []) for resource in ("secrets", "*")
    )


def check(values, expected_games, expected_secrets, namespace="gameplane-system"):
    objects = render(values, namespace=namespace, upgrade=True)
    watch = one(objects, "ClusterRole", "gameplane-operator-watch")
    assert not any(allows_secrets(rule) for rule in watch["rules"]), watch
    reads = [obj for obj in objects if obj["kind"] == "Role" and obj["metadata"]["name"] == "gameplane-operator-secrets"]
    assert {obj["metadata"]["namespace"] for obj in reads} == expected_secrets
    assert len(reads) == len(expected_secrets), "duplicate namespace Roles"
    for role in reads:
        assert role["rules"] == [{"apiGroups": [""], "resources": ["secrets"], "verbs": ["get", "list", "watch"]}]
        bindings = [obj for obj in objects if obj["kind"] == "RoleBinding"
                    and obj["metadata"]["name"] == role["metadata"]["name"]
                    and obj["metadata"]["namespace"] == role["metadata"]["namespace"]]
        assert len(bindings) == 1
        assert bindings[0]["roleRef"]["kind"] == "Role"
        assert bindings[0]["roleRef"]["name"] == role["metadata"]["name"]
        assert bindings[0]["subjects"] == [{"kind": "ServiceAccount", "name": "gameplane-operator", "namespace": namespace}]
    manages = [obj for obj in objects if obj["kind"] == "Role" and obj["metadata"]["name"] == "gameplane-operator-manage"]
    assert {obj["metadata"]["namespace"] for obj in manages} == expected_games
    args = one(objects, "Deployment", "gameplane-operator")["spec"]["template"]["spec"]["containers"][0]["args"]
    flags = dict(arg[2:].split("=", 1) for arg in args if arg.startswith("--") and "=" in arg)
    assert set(flags["game-namespaces"].split(",")) == expected_games
    assert expected_games | {flags[key] for key in ("control-plane-namespace", "agent-ca-secret-namespace", "module-namespace")} == expected_secrets


def main():
    check({}, {"gameplane-games"}, {"gameplane-games", "gameplane-system", "gameplane-system-credentials"})
    check({"gamesNamespace": "games-one", "operator": {"gameNamespaces": ["games-two", "games-one", "games-two"],
          "controlPlaneNamespace": "credentials", "agentCASecretNamespace": "certificates", "moduleNamespace": "catalog"}},
          {"games-one", "games-two"}, {"games-one", "games-two", "credentials", "certificates", "catalog"}, namespace="management")
    for invalid in ("*", "", "games,other", "Upper", "a" * 64):
        render({"operator": {"gameNamespaces": [invalid]}}, failure="explicit DNS-label namespace")
    render({"gamesNamespace": ""}, failure="explicit DNS-label namespace")
    render({"operator": {"moduleNamespace": "bad/name"}}, failure="explicit DNS-label namespace")
    for values, expected_namespace in [({}, "management-credentials"),
                                       ({"api": {"credentialsNamespace": "panel-creds"}}, "panel-creds")]:
        objects = render(values, namespace="management")
        api = one(objects, "Deployment", "gameplane-api")["spec"]["template"]["spec"]["containers"][0]
        assert {"name": "GAMEPLANE_CREDENTIALS_NAMESPACE", "value": expected_namespace} in api["env"]
        operator = one(objects, "Deployment", "gameplane-operator")["spec"]["template"]["spec"]["containers"][0]
        assert "--control-plane-namespace=" + expected_namespace in operator["args"]
        assert "--module-configmap-namespace=" + expected_namespace in operator["args"]
        assert all(set(rule["resources"]) <= {"pods", "pods/log"} for rule in one(objects, "Role", "gameplane-api-logs")["rules"])
        role = one(objects, "Role", "gameplane-api-credentials")
        assert role["metadata"]["namespace"] == expected_namespace
        assert role["rules"] == [
            {"apiGroups": [""], "resources": ["secrets"], "verbs": ["get", "create", "update", "delete"]},
            {"apiGroups": [""], "resources": ["configmaps"], "verbs": ["get", "list", "create", "update", "delete"]},
        ]
        binding = one(objects, "RoleBinding", "gameplane-api-credentials")
        assert binding["metadata"]["namespace"] == expected_namespace
        assert binding["roleRef"]["name"] == role["metadata"]["name"]
        assert binding["subjects"] == [{"kind": "ServiceAccount", "name": "gameplane-api", "namespace": "management"}]
        namespaces = {obj["metadata"]["name"] for obj in objects if obj["kind"] == "Namespace"}
        assert (expected_namespace in namespaces) == (not values)
    for values in ({"api": {"credentialsNamespace": "gameplane-system"}},
                   {"operator": {"gameNamespaces": ["gameplane-system"]}},
                   {"operator": {"agentCASecretNamespace": "gameplane-games"}},
                   {"operator": {"agentCASecretNamespace": "gameplane-system-credentials"}},
                   {"api": {"credentialsNamespace": "gameplane-games"}},
                   {"api": {"credentialsNamespace": "kube-system"}}):
        render(values, failure="dedicated namespace")
    # Operator-only releases apply admission and workload RBAC to every game namespace.
    for values in ({"operator": {"gameNamespaces": ["kube-system"]}},
                   {"gamesNamespace": "gameplane-system"},
                   {"operator": {"gameNamespaces": ["cluster-creds"], "controlPlaneNamespace": "cluster-creds"}},
                   {"operator": {"moduleNamespace": "gameplane-games"}},
                   {"operator": {"agentCASecretNamespace": "gameplane-games"}}):
        render({"api": {"enabled": False}, **values}, failure="dedicated namespace")
    check({"api": {"enabled": False}, "operator": {"gameNamespaces": ["remote-games"]}},
          {"gameplane-games", "remote-games"}, {"gameplane-games", "remote-games", "gameplane-system"})
    # Valid release namespaces may already occupy the entire DNS label.
    # Two sharing a long prefix must still get distinct credential stores.
    long_targets = set()
    for suffix in ("a", "b"):
        release_namespace = "m" * 62 + suffix
        objects = render(namespace=release_namespace)
        target = one(objects, "Role", "gameplane-api-credentials")["metadata"]["namespace"]
        assert len(target) <= 63 and target != release_namespace
        one(objects, "Namespace", target)
        long_targets.add(target)
    assert len(long_targets) == 2
    # The development install must not regain global Secret access after codegen.
    root = Path(__file__).resolve().parents[2]
    dev = list(yaml.safe_load_all((root / "operator/config/rbac/role.yaml").read_text()))
    assert all(not allows_secrets(rule) for obj in dev if obj for rule in obj.get("rules", []))
    dev_reads = list(yaml.safe_load_all((root / "operator/config/rbac/role_secrets.yaml").read_text()))
    role = one(dev_reads, "Role", "gameplane-operator-secrets")
    assert role["metadata"]["namespace"] == "gameplane-system"
    assert role["rules"] == [{"apiGroups": [""], "resources": ["secrets"], "verbs": ["get", "list", "watch"]}]
    print("Operator Secret namespace render assertions passed")


if __name__ == "__main__":
    main()
