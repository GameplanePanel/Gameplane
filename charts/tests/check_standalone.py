"""CI-only checks for combined, standalone central, and remote deployment roles."""
from pathlib import Path

import yaml

from check_gateway import one, render


def main():
    defaults = render()
    one(defaults, "Deployment", "gameplane-operator")
    one(defaults, "Deployment", "gameplane-api")
    one(defaults, "ModuleSource", "default")
    api_rules = one(defaults, "ClusterRole", "gameplane-api-read")["rules"]
    assert any(rule.get("resources") == ["clusters"] and "update" in rule["verbs"] for rule in api_rules)
    assert "--standalone" not in one(defaults, "Deployment", "gameplane-api")["spec"]["template"]["spec"]["containers"][0]["args"]

    values = {
        "operator": {"enabled": False}, "api": {"standalone": True},
        "serviceMonitors": {"enabled": True}, "prometheusRules": {"enabled": True},
        "grafanaDashboards": {"enabled": True}, "clusterOps": {"enabled": True},
        "mcpServer": {"enabled": True}, "capture": {"enabled": True},
    }
    for upgrade in (False, True):
        objects = render(values, upgrade=upgrade)
        for obj in objects:
            assert obj["kind"] not in {"ClusterRole", "ClusterRoleBinding", "Role", "RoleBinding", "Namespace", "ModuleSource", "NetworkPolicy", "PodMonitor", "PrometheusRule", "Job"}, obj
            assert "operator" not in obj["metadata"]["name"], obj
            assert not obj["metadata"]["name"].startswith("gameplane-agent"), obj
        one(objects, "Deployment", "gameplane-web")
        one(objects, "ServiceMonitor", "gameplane-api")
        one(objects, "PersistentVolumeClaim", "gameplane-api-data")
        deployment = one(objects, "Deployment", "gameplane-api")
        pod = deployment["spec"]["template"]["spec"]
        assert pod["automountServiceAccountToken"] is False
        assert pod["securityContext"]["fsGroupChangePolicy"] == "OnRootMismatch"
        assert "annotations" not in deployment["spec"]["template"]["metadata"]
        container = pod["containers"][0]
        assert "--standalone" in container["args"]
        assert "--panel-key-file=/keys/panel.key" in container["args"]
        assert not any(arg.startswith("--agent-") for arg in container["args"])
        assert container["volumeMounts"] == [{"name": "data", "mountPath": "/data"}, {"name": "panel-key", "mountPath": "/keys"}]
        assert pod["volumes"] == [
            {"name": "data", "persistentVolumeClaim": {"claimName": "gameplane-api-data"}},
            {"name": "panel-key", "persistentVolumeClaim": {"claimName": "gameplane-api-key"}},
        ]
        one(objects, "PersistentVolumeClaim", "gameplane-api-key")

    # The encryption key must survive pod replacement with either database driver.
    objects = render({"operator": {"enabled": False}, "api": {"standalone": True, "db": {"driver": "postgres"}}})
    one(objects, "PersistentVolumeClaim", "gameplane-api-data")
    one(objects, "PersistentVolumeClaim", "gameplane-api-key")
    objects = render({"operator": {"enabled": False}, "api": {"standalone": True, "storage": {"existingClaim": "panel-data"}, "panelKey": {"storage": {"existingClaim": "panel-key"}}}})
    assert not any(obj["kind"] == "PersistentVolumeClaim" for obj in objects)
    assert one(objects, "Deployment", "gameplane-api")["spec"]["template"]["spec"]["volumes"][0]["persistentVolumeClaim"]["claimName"] == "panel-data"

    # Externally provisioned keys are mounted read-only, without generating a key PVC.
    objects = render({"operator": {"enabled": False}, "api": {"standalone": True, "panelKey": {"existingSecret": "protected-panel-key", "secretKey": "master"}}})
    pod = one(objects, "Deployment", "gameplane-api")["spec"]["template"]["spec"]
    assert "--panel-key-provisioned" in pod["containers"][0]["args"]
    assert {"name": "panel-key", "mountPath": "/keys", "readOnly": True} in pod["containers"][0]["volumeMounts"]
    key_volume = next(volume for volume in pod["volumes"] if volume["name"] == "panel-key")
    assert key_volume["secret"] == {"secretName": "protected-panel-key", "defaultMode": 0o440, "items": [{"key": "master", "path": "panel.key"}]}
    assert not any(obj["kind"] == "PersistentVolumeClaim" and obj["metadata"]["name"] == "gameplane-api-key" for obj in objects)

    for custody in ({}, {"existingSecret": "protected-panel-key", "secretKey": "next"}):
        objects = render({"operator": {"enabled": False}, "api": {"standalone": True, "panelKey": {**custody, "fileName": "panel-next.key"}}})
        pod = one(objects, "Deployment", "gameplane-api")["spec"]["template"]["spec"]
        assert "--panel-key-file=/keys/panel-next.key" in pod["containers"][0]["args"]
        if custody:
            volume = next(volume for volume in pod["volumes"] if volume["name"] == "panel-key")
            assert volume["secret"]["items"] == [{"key": "next", "path": "panel-next.key"}]
    render({"operator": {"enabled": False}, "api": {"standalone": True, "panelKey": {"fileName": "../outside.key"}}}, failure="api.panelKey.fileName must be a simple filename")

    render({"operator": {"enabled": False}, "api": {"standalone": True, "panelKey": {"existingSecret": "key", "storage": {"existingClaim": "key-pvc"}}}}, failure="choose either api.panelKey.existingSecret or api.panelKey.storage.existingClaim")

    objects = render({"operator": {"enabled": False}, "api": {"standalone": True, "remoteAllowedCIDRs": ["10.20.0.0/16", "fd12:3456::/48"]}})
    args = one(objects, "Deployment", "gameplane-api")["spec"]["template"]["spec"]["containers"][0]["args"]
    assert "--remote-allowed-cidrs=10.20.0.0/16,fd12:3456::/48" in args

    remote = render({"api": {"enabled": False}})
    one(remote, "Deployment", "gameplane-operator")
    one(remote, "Secret", "gameplane-agent-ca")
    assert not any(obj["metadata"]["name"] in {"gameplane-api", "gameplane-web"} for obj in remote)
    disabled = render({"operator": {"enabled": False}})
    assert not any("operator" in obj["metadata"]["name"] for obj in disabled)
    one(disabled, "Deployment", "gameplane-api")

    render({"api": {"standalone": True}}, failure="api.standalone requires operator.enabled=false")
    render({"operator": {"enabled": False}, "api": {"standalone": True, "replicas": 2}}, failure="api.standalone requires api.replicas=1")
    example = yaml.safe_load((Path(__file__).parents[1] / "gameplane/examples/standalone-panel-values.yaml").read_text())
    one(render(example), "Deployment", "gameplane-api")


if __name__ == "__main__":
    main()
