"""CI contract for Argo CD's offline native Helm profile."""
import subprocess

import yaml

from check_gateway import CHART, one


PROFILE = CHART / "examples" / "argocd-values.yaml"
ADMISSION = "admissionregistration.k8s.io/v1/"
POLICY = ADMISSION + "ValidatingAdmissionPolicy"
BINDING = ADMISSION + "ValidatingAdmissionPolicyBinding"


def render(*flags, capabilities=(POLICY, BINDING), failure=None):
    cmd = ["helm", "template", "gameplane", str(CHART), "--namespace", "gameplane-system",
           "--kube-version", "1.34.0", "--values", str(PROFILE)]
    for capability in capabilities:
        cmd.extend(["--api-versions", capability])
    result = subprocess.run(cmd + list(flags), capture_output=True, check=False)
    if failure:
        assert result.returncode and failure.encode() in result.stderr, result.stderr.decode()
    else:
        result.check_returncode()
    return result.stdout


def objects(output):
    return [obj for obj in yaml.safe_load_all(output) if obj]


def check_trust(resources, ca, client):
    checksums = []
    for name in ("gameplane-api", "gameplane-operator"):
        template = one(resources, "Deployment", name)["spec"]["template"]
        volumes = {v["name"]: v for v in template["spec"]["volumes"]}
        assert volumes["agent-ca"]["secret"]["secretName"] == ca
        assert volumes["agent-client"]["secret"]["secretName"] == client
        checksums.append(template["metadata"]["annotations"]["checksum/mtls"])
    assert checksums[0] == checksums[1]
    return checksums[0]


def main():
    output = render()
    assert output == render() == render("--is-upgrade"), "render depends on time, randomness or release state"
    resources = objects(output)
    for obj in resources:
        assert obj["kind"] != "CustomResourceDefinition", obj["metadata"]["name"]
        assert "helm.sh/hook" not in obj["metadata"].get("annotations", {}), obj["metadata"]["name"]
        assert not (obj["kind"] == "Secret" and {"ca.key", "tls.key"} & obj.get("data", {}).keys())
    policy = one(resources, "ValidatingAdmissionPolicy", "gameplane-waker-wake-only")
    binding = one(resources, "ValidatingAdmissionPolicyBinding", "gameplane-waker-wake-only")
    assert policy["spec"]["failurePolicy"] == "Fail"
    assert binding["spec"]["policyName"] == policy["metadata"]["name"]
    assert "Deny" in binding["spec"]["validationActions"]
    for kind, name in (("PersistentVolumeClaim", "gameplane-api-data"), ("Namespace", "gameplane-games")):
        annotations = one(resources, kind, name)["metadata"]["annotations"]
        assert set(annotations["argocd.argoproj.io/sync-options"].split(",")) == {"Prune=false", "Delete=false"}
    current = check_trust(resources, "gameplane-external-ca-v1", "gameplane-external-client-v1")
    rotated = objects(render("--set", "api.agentMTLS.caSecretRef.name=external-ca-v2",
                             "--set", "api.agentMTLS.clientCertRef.name=external-client-v2"))
    assert check_trust(rotated, "external-ca-v2", "external-client-v2") != current
    for capabilities, missing in (((), POLICY), ((POLICY,), BINDING), ((BINDING,), POLICY)):
        render(capabilities=capabilities, failure=missing)
    for path, value, reason in (
        ("api.agentMTLS.caSecretRef.name", "gameplane-agent-ca", "distinct external"),
        ("api.agentMTLS.clientCertRef.name", "gameplane-agent-client", "distinct external"),
        ("api.agentMTLS.clientCertRef.name", "gameplane-external-ca-v1", "distinct external"),
        ("crds.autoApply.enabled", "true", "one external CRD owner"),
    ):
        render("--set", f"{path}={value}", failure=reason)
    handoff = objects(render("--is-upgrade", "--set", "api.storage.existingClaim=gameplane-api-data"))
    assert not any(obj["kind"] == "PersistentVolumeClaim" and obj["metadata"]["name"] == "gameplane-api-data" for obj in handoff)
    api = one(handoff, "Deployment", "gameplane-api")
    claims = [vol["persistentVolumeClaim"]["claimName"] for vol in api["spec"]["template"]["spec"]["volumes"]
              if "persistentVolumeClaim" in vol]
    assert "gameplane-api-data" in claims


if __name__ == "__main__":
    main()
