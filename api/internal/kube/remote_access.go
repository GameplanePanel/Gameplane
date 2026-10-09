package kube

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/GameplanePanel/gameplane/netguard"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// RemoteAccessPolicy restricts standalone workload connections. Operator CIDRs
// can narrow the safe destinations, but can never permit unsafe address classes.
// A nil policy applies the safe baseline and allows private workload networks.
type RemoteAccessPolicy struct {
	allowed []netip.Prefix
	resolve func(context.Context, string) ([]net.IPAddr, error)
	dial    func(context.Context, string, string) (net.Conn, error)
}

// NewRemoteAccessPolicy parses an optional operator destination allowlist.
func NewRemoteAccessPolicy(allowedCIDRs []string) (*RemoteAccessPolicy, error) {
	policy := &RemoteAccessPolicy{}
	for _, raw := range allowedCIDRs {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(raw))
		if err != nil {
			return nil, errors.New("invalid remote destination CIDR")
		}
		policy.allowed = append(policy.allowed, prefix.Masked())
	}
	return policy, nil
}

func normalizedRemoteIP(ip net.IP) net.IP {
	if v4 := ip.To4(); v4 != nil {
		return v4
	}
	if len(ip) == net.IPv6len {
		compatible := true
		for _, b := range ip[:12] {
			if b != 0 {
				compatible = false
				break
			}
		}
		if compatible {
			return ip[12:]
		}
	}
	return ip
}

func (p *RemoteAccessPolicy) allowsIP(ip net.IP) bool {
	if ip == nil || ip.IsLoopback() || !netguard.IsAllowed(ip) {
		return false
	}
	ip = normalizedRemoteIP(ip)
	if ip.IsLoopback() || !ip.IsGlobalUnicast() {
		return false
	}
	if v4 := ip.To4(); v4 != nil && (v4[0] == 0 || v4[0] >= 240) {
		return false
	}
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	address = address.Unmap()
	if p == nil || len(p.allowed) == 0 {
		return true
	}
	for _, prefix := range p.allowed {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

// DialContext resolves once, validates every returned address, and dials only a
// validated literal. There is no second DNS lookup between checking and dialing.
func (p *RemoteAccessPolicy) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errors.New("invalid remote destination")
	}
	if netguard.HostIsMetadata(host) {
		return nil, netguard.ErrBlockedAddr
	}
	var addresses []net.IPAddr
	if ip := net.ParseIP(host); ip != nil {
		addresses = []net.IPAddr{{IP: ip}}
	} else {
		resolve := net.DefaultResolver.LookupIPAddr
		if p != nil && p.resolve != nil {
			resolve = p.resolve
		}
		addresses, err = resolve(ctx, host)
		if err != nil {
			return nil, errors.New("remote destination lookup failed")
		}
	}
	if len(addresses) == 0 {
		return nil, netguard.ErrBlockedAddr
	}
	for _, address := range addresses {
		if address.Zone != "" || !p.allowsIP(address.IP) {
			return nil, netguard.ErrBlockedAddr
		}
	}
	dial := (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	if p != nil && p.dial != nil {
		dial = p.dial
	}
	for _, address := range addresses {
		conn, dialErr := dial(ctx, network, net.JoinHostPort(address.IP.String(), port))
		if dialErr == nil {
			return conn, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return nil, errors.New("remote destination connection failed")
}

// ValidateStandaloneKubeconfig rejects insecure or proxy-dependent entries before
// building a client. It returns a configuration with response and dial guards.
func ValidateStandaloneKubeconfig(data []byte) (*rest.Config, error) {
	parsed, err := clientcmd.Load(data)
	if err != nil {
		return nil, errors.New("invalid remote kubeconfig")
	}
	for _, cluster := range parsed.Clusters {
		if cluster == nil {
			continue
		}
		if cluster.InsecureSkipTLSVerify {
			return nil, errors.New("standalone kubeconfigs require verified TLS")
		}
		if cluster.ProxyURL != "" {
			return nil, errors.New("standalone kubeconfigs cannot use a custom proxy")
		}
		if err := (*RemoteAccessPolicy)(nil).ValidateURL(cluster.Server); err != nil {
			return nil, err
		}
	}
	cfg, err := ConfigFromKubeconfig(data)
	if err != nil {
		return nil, err
	}
	ApplyStandaloneRemotePolicy(cfg, nil)
	return cfg, nil
}

// ValidateURL checks configured origins without a DNS preflight; DNS answers are
// checked when connecting. TLS hostname verification remains authoritative.
func (p *RemoteAccessPolicy) ValidateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("standalone remote endpoints require an HTTPS URL without credentials, query, or fragment")
	}
	if netguard.HostIsMetadata(u.Hostname()) {
		return netguard.ErrBlockedAddr
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && !p.allowsIP(ip) {
		return netguard.ErrBlockedAddr
	}
	return nil
}

// ApplyStandaloneRemotePolicy installs operator restrictions on a config returned
// by ValidateStandaloneKubeconfig. It never honors environment forward proxies.
func ApplyStandaloneRemotePolicy(cfg *rest.Config, policy *RemoteAccessPolicy) {
	cfg.Dial = policy.DialContext
	cfg.Proxy = func(*http.Request) (*url.URL, error) { return nil, nil }
	cfg.WrapTransport = standaloneRemoteTransport
}

type standaloneTransport struct{ next http.RoundTripper }

func standaloneRemoteTransport(next http.RoundTripper) http.RoundTripper {
	return &standaloneTransport{next: boundedRemoteTransport(next)}
}

func (t *standaloneTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.next.RoundTrip(req)
	if err != nil {
		return resp, err
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		_ = resp.Body.Close()
		return nil, errors.New("remote Kubernetes redirects are unsupported")
	}
	return resp, nil
}
