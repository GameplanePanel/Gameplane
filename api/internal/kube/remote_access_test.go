package kube

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/GameplanePanel/gameplane/netguard"
)

func standaloneConfigInput(server, fields string) []byte {
	return []byte(fmt.Sprintf(`{"apiVersion":"v1","kind":"Config","clusters":[{"name":"remote","cluster":{"server":%q%s}}],"contexts":[{"name":"remote","context":{"cluster":"remote","user":"panel"}}],"current-context":"remote","users":[{"name":"panel","user":{"token":"private-token"}}]}`, server, fields))
}

func TestStandaloneKubeconfigRequiresVerifiedDirectHTTPS(t *testing.T) {
	for _, tc := range []struct{ name, server, fields string }{
		{"plaintext", "http://10.0.0.1:6443", ""},
		{"unverified TLS", "https://10.0.0.1:6443", `,"insecure-skip-tls-verify":true`},
		{"custom proxy", "https://10.0.0.1:6443", `,"proxy-url":"http://proxy.example:8080"`},
		{"URL credentials", "https://private:token@cluster.example:6443", ""},
		{"metadata hostname", "https://metadata.google.internal", ""},
		{"loopback", "https://127.0.0.1:6443", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := ValidateStandaloneKubeconfig(standaloneConfigInput(tc.server, tc.fields))
			if err == nil || cfg != nil {
				t.Fatal("unsafe standalone kubeconfig was accepted")
			}
			if strings.Contains(err.Error(), "private-token") || strings.Contains(err.Error(), "private:token") {
				t.Fatal("validation disclosed credentials")
			}
		})
	}
	cfg, err := ValidateStandaloneKubeconfig(standaloneConfigInput("https://10.0.0.1:6443", ""))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:8888")
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, cfg.Host, nil)
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := cfg.Proxy(req)
	if err != nil || proxy != nil {
		t.Fatal("standalone transport honored an environment proxy")
	}
}

func TestStandaloneKubeconfigChecksUnusedContexts(t *testing.T) {
	data := standaloneConfigInput("https://10.0.0.1:6443", "")
	// clientcmd preserves every named cluster; an unselected insecure entry must
	// not be retained for later current-context changes.
	data = []byte(strings.Replace(string(data), `"clusters":[`, `"clusters":[{"name":"unused","cluster":{"server":"https://10.0.0.2","insecure-skip-tls-verify":true}},`, 1))
	if cfg, err := ValidateStandaloneKubeconfig(data); err == nil || cfg != nil {
		t.Fatal("unused insecure cluster was accepted")
	}
}

func TestRemoteAccessPolicyCannotAllowUnsafeClasses(t *testing.T) {
	policy, err := NewRemoteAccessPolicy([]string{"0.0.0.0/0", "::/0"})
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"127.0.0.1", "::1", "169.254.169.254", "fd00:ec2::254", "100.100.100.200", "0.0.0.0", "::", "224.0.0.1", "ff02::1", "64:ff9b::a9fe:a9fe", "2002:a9fe:a9fe::1", "::ffff:127.0.0.1", "::127.0.0.1", "0.1.2.3", "255.255.255.255"} {
		t.Run(raw, func(t *testing.T) {
			called := false
			policy.dial = func(context.Context, string, string) (net.Conn, error) {
				called = true
				return nil, errors.New("unexpected dial")
			}
			_, err := policy.DialContext(t.Context(), "tcp", net.JoinHostPort(raw, "6443"))
			if !errors.Is(err, netguard.ErrBlockedAddr) || called {
				t.Fatalf("unsafe address reached dial: %v", err)
			}
		})
	}
}

func TestRemoteAccessPolicyPermitsOnlySelectedSafeDestinations(t *testing.T) {
	policy, err := NewRemoteAccessPolicy([]string{"10.0.0.0/8", "fd01::/64"})
	if err != nil {
		t.Fatal(err)
	}
	policy.dial = func(context.Context, string, string) (net.Conn, error) {
		a, b := net.Pipe()
		_ = b.Close()
		return a, nil
	}
	for _, raw := range []string{"10.2.3.4", "fd01::5"} {
		conn, err := policy.DialContext(t.Context(), "tcp", net.JoinHostPort(raw, "6443"))
		if err != nil {
			t.Fatal(err)
		}
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := policy.DialContext(t.Context(), "tcp", "192.168.1.1:6443"); !errors.Is(err, netguard.ErrBlockedAddr) {
		t.Fatal("destination outside operator CIDRs was allowed")
	}
	if _, err := NewRemoteAccessPolicy([]string{"typo"}); err == nil {
		t.Fatal("invalid operator allowlist failed open")
	}
}

func TestRemoteAccessPolicyPinsValidatedDNSAndRejectsRebinding(t *testing.T) {
	policy, err := NewRemoteAccessPolicy(nil)
	if err != nil {
		t.Fatal(err)
	}
	lookups, dials := 0, 0
	policy.resolve = func(context.Context, string) ([]net.IPAddr, error) {
		lookups++
		raw := "10.0.0.8"
		if lookups > 1 {
			raw = "169.254.169.254"
		}
		return []net.IPAddr{{IP: net.ParseIP(raw)}}, nil
	}
	policy.dial = func(_ context.Context, _ string, address string) (net.Conn, error) {
		dials++
		if address != "10.0.0.8:6443" {
			t.Fatalf("dial performed another DNS resolution: %s", address)
		}
		a, b := net.Pipe()
		_ = b.Close()
		return a, nil
	}
	conn, err := policy.DialContext(t.Context(), "tcp", "cluster.example:6443")
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := policy.DialContext(t.Context(), "tcp", "cluster.example:6443"); !errors.Is(err, netguard.ErrBlockedAddr) || dials != 1 {
		t.Fatal("rebound destination was dialed")
	}
}

func TestStandaloneTransportRejectsCredentialRedirects(t *testing.T) {
	client := &http.Client{Transport: standaloneRemoteTransport(responseRoundTripper(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusTemporaryRedirect, Header: http.Header{"Location": []string{"https://attacker.example/collect"}}, Body: io.NopCloser(strings.NewReader("redirect"))}, nil
	}))}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://cluster.example/version", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer private-token")
	resp, err := client.Do(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err == nil || strings.Contains(err.Error(), "private-token") {
		t.Fatal("credential redirect was followed or leaked its token")
	}
}
