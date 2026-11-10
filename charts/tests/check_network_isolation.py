"""CI-only rendered effective policy tuple and strict-profile regressions."""
from check_standalone import render, one


def main():
    # The chart-level probe rule is the fallback when the operator's
    # per-server probe policy (probeIngress) is off.
    values = {"networkPolicies": {"strict": True,
              "probeIngress": {"enabled": False},
              "kubeletCIDRs": ["192.0.2.10/32"],
              "probePorts": [{"protocol": "TCP", "port": 8090}],
              "apiServerCIDRs": ["198.51.100.20/32"]}}
    objects = render(values)
    probe = one(objects, "NetworkPolicy", "allow-kubelet-probes")["spec"]["ingress"]
    assert probe == [{"from": [{"ipBlock": {"cidr": "192.0.2.10/32"}}],
                      "ports": [{"protocol": "TCP", "port": 8090}]}]
    for name in ("allow-agent-to-apiserver", "allow-sentinel-to-apiserver"):
        egress = one(objects, "NetworkPolicy", name)["spec"]["egress"]
        assert egress == [{"to": [{"ipBlock": {"cidr": "198.51.100.20/32"}}],
                           "ports": [{"protocol": "TCP", "port": 443}, {"protocol": "TCP", "port": 6443}]}]
    excluded = [{"key": "gameplane.local/sentinel-isolation", "operator": "NotIn", "values": ["true"]}]
    for name, direction, peer in (("allow-sentinel-to-game", "ingress", "from"),
                                 ("allow-sentinel-to-game-egress", "egress", "to")):
        policy = one(objects, "NetworkPolicy", name)["spec"]
        assert policy["podSelector"]["matchExpressions"] == excluded
        assert policy[direction][0][peer][0]["podSelector"]["matchExpressions"] == excluded
    import copy
    for key in ("kubeletCIDRs", "apiServerCIDRs", "probePorts"):
        invalid = copy.deepcopy(values)
        invalid["networkPolicies"][key] = []
        render(invalid, failure="requires a non-empty networkPolicies." + key)
    for cidr in ("", "10.0.0.1", "999.0.0.1/32", "10.0.0.1/33", "2001:::1/64", "2001:db8/64", "::/129"):
        invalid = copy.deepcopy(values)
        invalid["networkPolicies"]["apiServerCIDRs"] = [cidr]
        render(invalid, failure="must be explicit IPv4/IPv6 CIDRs")
    for cidr in ("::/0", "::1/128", "2001:db8::/64", "2001:db8:1:2:3:4:5:6/128"):
        valid = copy.deepcopy(values)
        valid["networkPolicies"]["apiServerCIDRs"] = [cidr]
        render(valid)
    for port in (0, 65536, 1.5, "all"):
        invalid = copy.deepcopy(values)
        invalid["networkPolicies"]["probePorts"][0]["port"] = port
        render(invalid, failure="integer port")
    render({"networkPolicies": {"strict": True, "enabled": False}}, failure="requires networkPolicies.enabled=true")
    # With probeIngress off and no probePorts, the documented broad fallback renders.
    legacy = render({"networkPolicies": {"probeIngress": {"enabled": False}}})
    fallback = one(legacy, "NetworkPolicy", "allow-kubelet-probes")["spec"]["ingress"][0]
    assert "ports" not in fallback and {p["ipBlock"]["cidr"] for p in fallback["from"]} == {
        "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16"}
    # Default installs: the operator admits probes per server, so there is no
    # catch-all rule, and the operator gets the probe flags with the fallback CIDRs.
    default = render()
    assert not [o for o in default if o.get("kind") == "NetworkPolicy"
                and o["metadata"]["name"] == "allow-kubelet-probes"]
    operator = one(default, "Deployment", "gameplane-operator")
    args = operator["spec"]["template"]["spec"]["containers"][0]["args"]
    assert "--probe-ingress-policy=true" in args
    assert [a for a in args if a.startswith("--probe-ingress-from-cidr=")] == [
        "--probe-ingress-from-cidr=10.0.0.0/8", "--probe-ingress-from-cidr=172.16.0.0/12",
        "--probe-ingress-from-cidr=192.168.0.0/16", "--probe-ingress-from-cidr=169.254.0.0/16"]
    # Strict mode needs probePorts only when the per-server policy is off.
    strict = copy.deepcopy(values)
    del strict["networkPolicies"]["probeIngress"]
    del strict["networkPolicies"]["probePorts"]
    assert not [o for o in render(strict) if o.get("kind") == "NetworkPolicy"
                and o["metadata"]["name"] == "allow-kubelet-probes"]
    # Extra probePorts with probeIngress on render a rule for exactly those ports.
    extra = one(render({"networkPolicies": {"probePorts": [{"protocol": "TCP", "port": 9000}]}}),
                "NetworkPolicy", "allow-kubelet-probes")["spec"]["ingress"][0]
    assert extra["ports"] == [{"protocol": "TCP", "port": 9000}]


if __name__ == "__main__":
    main()
