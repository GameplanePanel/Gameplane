"""CI-only rendered effective policy tuple and strict-profile regressions."""
from check_standalone import render, one


def main():
    values = {"networkPolicies": {"strict": True,
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
    # Legacy/default installs still render the documented broad fallback.
    fallback = one(render(), "NetworkPolicy", "allow-kubelet-probes")["spec"]["ingress"][0]
    assert "ports" not in fallback and {p["ipBlock"]["cidr"] for p in fallback["from"]} == {
        "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16"}


if __name__ == "__main__":
    main()
