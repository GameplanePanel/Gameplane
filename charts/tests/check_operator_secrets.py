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
    check({}, {"gameplane-games"}, {"gameplane-games", "gameplane-system"})
    check({"gamesNamespace": "games-one", "operator": {"gameNamespaces": ["games-two", "games-one", "games-two"],
          "controlPlaneNamespace": "credentials", "agentCASecretNamespace": "certificates", "moduleNamespace": "catalog"}},
          {"games-one", "games-two"}, {"games-one", "games-two", "credentials", "certificates", "catalog"}, namespace="management")
    for invalid in ("*", "", "games,other", "Upper", "a" * 64):
        render({"operator": {"gameNamespaces": [invalid]}}, failure="explicit DNS-label namespace")
    render({"gamesNamespace": ""}, failure="explicit DNS-label namespace")
    render({"operator": {"moduleNamespace": "bad/name"}}, failure="explicit DNS-label namespace")
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
