"""CI-only checks for combined, standalone central, and remote deployment roles."""
from pathlib import Path
import subprocess
import tempfile

import yaml

from check_gateway import CHART, one, render


EXPECTED_CRDS = {
    "gameservers.gameplane.local", "gametemplates.gameplane.local",
    "backups.gameplane.local", "backupschedules.gameplane.local",
    "restores.gameplane.local", "modules.gameplane.local",
    "modulesources.gameplane.local", "clusters.gameplane.local",
    "networkcaptures.gameplane.local",
}


def assert_crds(objects, expected):
    """Require exactly the expected CRD names, with no duplicate definitions."""
    names = [obj["metadata"]["name"] for obj in objects if obj["kind"] == "CustomResourceDefinition"]
    assert set(names) == expected and len(names) == len(expected), names


def check_packaged_crds(example):
    """Verify vendored chart archives preserve CRD gating for each install role."""
    # Package only vendored files. Dependency downloads/builds would hide a
    # missing bundled CRD chart and require network access for installation.
    with tempfile.TemporaryDirectory() as directory:
        result = subprocess.run(["helm", "package", str(CHART), "--destination", directory],
                                capture_output=True, text=True, check=False)
        assert result.returncode == 0, result.stderr
        packages = list(Path(directory).glob("gameplane-*.tgz"))
        assert len(packages) == 1, packages
        for upgrade in (False, True):
            for values in ({}, {"api": {"enabled": False}}):
                assert_crds(render(values, chart=packages[0], include_crds=True, upgrade=upgrade), EXPECTED_CRDS)
            for values in ({"operator": {"enabled": False}, "api": {"standalone": True}}, example):
                objects = render(values, chart=packages[0], include_crds=True, upgrade=upgrade)
                assert_crds(objects, set())
                assert not any("crd-apply" in obj["metadata"]["name"] for obj in objects), objects


def check_api_availability():
    """All API modes stay singleton, migrate old strategies, and probe the DB."""
    for standalone in (False, True):
        for driver in ("sqlite", "postgres"):
            values = {"operator": {"enabled": not standalone},
                      "api": {"standalone": standalone, "db": {"driver": driver}}}
            for upgrade in (False, True):
                objects = render(values, upgrade=upgrade)
                deployment = one(objects, "Deployment", "gameplane-api")
                assert deployment["spec"]["replicas"] == 1
                assert deployment["spec"]["strategy"] == {"type": "Recreate", "rollingUpdate": None}
                container = next(c for c in deployment["spec"]["template"]["spec"]["containers"] if c["name"] == "api")
                assert container["livenessProbe"]["httpGet"]["path"] == "/healthz"
                assert container["readinessProbe"]["httpGet"]["path"] == "/readyz"
                assert container["readinessProbe"]["failureThreshold"] == 3
                assert container["readinessProbe"]["periodSeconds"] == 5
                assert container["readinessProbe"]["timeoutSeconds"] > 2
                if upgrade and not standalone:
                    role = one(objects, "Role", "gameplane-strategy-migrate")
                    assert role["rules"][0]["resourceNames"] == ["gameplane-api"]
                    job = one(objects, "Job", "gameplane-crd-apply")
                    # Client-side rendering has no live Deployment. Enabling
                    # the API on an older operator-only release must skip the
                    # migration instead of PATCHing an absent resource.
                    assert not any(c["name"] == "migrate-api-strategy" for c in job["spec"]["template"]["spec"].get("initContainers", []))
            for replicas in (0, 2, -1, 1.5):
                values["api"]["replicas"] = replicas
                render(values, failure="api requires api.replicas=1")
    # A remote installation with no API has no singleton requirement.
    render({"api": {"enabled": False, "replicas": 0}})


def main():
    """Check deployment roles, standalone key custody, and source/archive CRDs."""
    check_api_availability()
    defaults = render(include_crds=True)
    assert_crds(defaults, EXPECTED_CRDS)
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
        objects = render(values, upgrade=upgrade, include_crds=True)
        assert_crds(objects, set())
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

    # PostgreSQL needs persistent key custody, but no local database volume.
    # Retain Recreate to avoid overlapping API writers, including Secret custody.
    for custody in ({}, {"existingSecret": "protected-panel-key"}):
        objects = render({"operator": {"enabled": False}, "api": {"standalone": True, "db": {"driver": "postgres"}, "panelKey": custody}})
        assert not any(obj["kind"] == "PersistentVolumeClaim" and obj["metadata"]["name"] == "gameplane-api-data" for obj in objects)
        deployment = one(objects, "Deployment", "gameplane-api")
        assert deployment["spec"]["strategy"] == {"type": "Recreate", "rollingUpdate": None}
        pod = deployment["spec"]["template"]["spec"]
        assert not any(volume["name"] == "data" for volume in pod["volumes"])
        assert not any(mount["name"] == "data" for mount in pod["containers"][0]["volumeMounts"])
        assert any(mount["name"] == "panel-key" and mount["mountPath"] == "/keys" for mount in pod["containers"][0]["volumeMounts"])
        if not custody:
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

    remote = render({"api": {"enabled": False}}, include_crds=True)
    assert_crds(remote, EXPECTED_CRDS)
    one(remote, "Deployment", "gameplane-operator")
    one(remote, "Secret", "gameplane-agent-ca")
    assert not any(obj["metadata"]["name"] in {"gameplane-api", "gameplane-web"} for obj in remote)
    disabled = render({"operator": {"enabled": False}}, include_crds=True)
    assert_crds(disabled, set())
    one(disabled, "Job", "gameplane-crd-apply")
    assert not any("operator" in obj["metadata"]["name"] for obj in disabled)
    one(disabled, "Deployment", "gameplane-api")

    render({"api": {"standalone": True}}, failure="api.standalone requires operator.enabled=false")
    render({"operator": {"enabled": False}, "api": {"enabled": False, "standalone": True}}, failure="api.standalone requires api.enabled=true")
    render({"operator": {"enabled": False}, "api": {"standalone": True, "replicas": 2}}, failure="api requires api.replicas=1")
    example = yaml.safe_load((Path(__file__).parents[1] / "gameplane/examples/standalone-panel-values.yaml").read_text())
    for upgrade in (False, True):
        objects = render(example, include_crds=True, upgrade=upgrade)
        assert_crds(objects, set())
        one(objects, "Deployment", "gameplane-api")
        assert not any("crd-apply" in obj["metadata"]["name"] for obj in objects), objects
        assert_crds(render(include_crds=True, upgrade=upgrade), EXPECTED_CRDS)
        assert_crds(render({"api": {"enabled": False}}, include_crds=True, upgrade=upgrade), EXPECTED_CRDS)
    check_packaged_crds(example)


if __name__ == "__main__":
    main()
