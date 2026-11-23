package handlers

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"testing"

	"golang.org/x/crypto/ssh"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kubefake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

const hostKeyTestSecret = "gameplane-sftp-host-key"

// hostKeyTestPEM returns a PEM ed25519 host key and its SHA256 fingerprint.
func hostKeyTestPEM(t *testing.T) ([]byte, string) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	pub, err := ssh.NewPublicKey(priv.Public())
	if err != nil {
		t.Fatalf("public key: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), ssh.FingerprintSHA256(pub)
}

func hostKeyTestSecretObj(ns string, data []byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: hostKeyTestSecret, Namespace: ns},
		Data:       map[string][]byte{sftpHostKeyDataKey: data},
	}
}

// hostKeyTestClient returns a cluster client whose Secret reads are answered by the fake clientset.
func hostKeyTestClient(objs ...runtime.Object) (*kube.Client, *kubefake.Clientset) {
	cs := kubefake.NewClientset(objs...)
	return &kube.Client{Typed: cs}, cs
}

func TestSFTPHostKeyUnit_FingerprintFromReleaseNamespace(t *testing.T) {
	pemData, want := hostKeyTestPEM(t)
	k, _ := hostKeyTestClient(hostKeyTestSecretObj("gameplane-system", pemData))

	got, err := localSFTPFingerprint(k, "gameplane-system", hostKeyTestSecret)(t.Context())
	if err != nil || got != want {
		t.Fatalf("fingerprint = %q, %v; want %q", got, err, want)
	}

	// The Secret is read in the namespace given, not in another one that happens to hold no key.
	got, err = localSFTPFingerprint(k, "gameplane-credentials", hostKeyTestSecret)(t.Context())
	if err != nil || got != "" {
		t.Fatalf("other namespace: %q, %v; want empty and no error", got, err)
	}
}

func TestSFTPHostKeyUnit_ForbiddenReadsAsNoFingerprint(t *testing.T) {
	k, cs := hostKeyTestClient()
	cs.PrependReactor("get", "secrets", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, hostKeyTestSecret, errors.New("rbac"))
	})

	fp, err := localSFTPFingerprint(k, "gameplane-system", hostKeyTestSecret)(t.Context())
	if err != nil || fp != "" {
		t.Fatalf("forbidden read: %q, %v; want empty fingerprint and no error so the drawer still loads", fp, err)
	}
}

func TestSFTPHostKeyUnit_OtherReadErrorIsReturned(t *testing.T) {
	k, cs := hostKeyTestClient()
	cs.PrependReactor("get", "secrets", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("apiserver unavailable")
	})

	if _, err := localSFTPFingerprint(k, "gameplane-system", hostKeyTestSecret)(t.Context()); err == nil {
		t.Fatal("a non-authorization read error must still be reported")
	}
}

func TestSFTPHostKeyUnit_MissingSecretAndUnreadableKey(t *testing.T) {
	k, _ := hostKeyTestClient()
	if fp, err := localSFTPFingerprint(k, "gameplane-system", hostKeyTestSecret)(t.Context()); err != nil || fp != "" {
		t.Fatalf("missing secret: %q, %v", fp, err)
	}

	k, _ = hostKeyTestClient(hostKeyTestSecretObj("gameplane-system", []byte("not a key")))
	if fp, err := localSFTPFingerprint(k, "gameplane-system", hostKeyTestSecret)(t.Context()); err != nil || fp != "" {
		t.Fatalf("unreadable key: %q, %v; want empty fingerprint and no error", fp, err)
	}
}

func TestSFTPHostKeyUnit_LocalEndpointSource(t *testing.T) {
	pemData, fp := hostKeyTestPEM(t)
	k, _ := hostKeyTestClient(hostKeyTestSecretObj("gameplane-system", pemData))
	base := SFTPClusterMount{
		Kube:          k,
		Namespace:     "gameplane-system",
		HostKeySecret: hostKeyTestSecret,
		Enabled:       true,
		Port:          2222,
		PublicAddress: "files.example.com",
	}

	src := LocalSFTPEndpointSource(base, scope.DefaultCluster)
	ep, ok := src(t.Context(), scope.DefaultCluster)
	if !ok {
		t.Fatal("enabled local endpoint with an address must be reported")
	}
	want := SFTPEndpoint{Address: "files.example.com", Port: 2222, HostKeyFingerprint: fp}
	if ep != want {
		t.Fatalf("endpoint = %+v; want %+v", ep, want)
	}

	// host:port in the address overrides the configured port.
	withPort := base
	withPort.PublicAddress = "files.example.com:30222"
	if ep, ok := LocalSFTPEndpointSource(withPort, scope.DefaultCluster)(t.Context(), scope.DefaultCluster); !ok || ep.Port != 30222 || ep.Address != "files.example.com" {
		t.Fatalf("host:port endpoint = %+v, %v", ep, ok)
	}

	// Bracketed IPv6 literals keep their host without brackets.
	v6 := base
	v6.PublicAddress = "[2001:db8::1]:2200"
	if ep, ok := LocalSFTPEndpointSource(v6, scope.DefaultCluster)(t.Context(), scope.DefaultCluster); !ok || ep.Address != "2001:db8::1" || ep.Port != 2200 {
		t.Fatalf("ipv6 endpoint = %+v, %v", ep, ok)
	}

	// Disabled SFTP, another cluster, no address and an invalid address all read as no endpoint (null).
	disabled := base
	disabled.Enabled = false
	noAddress := base
	noAddress.PublicAddress = ""
	invalid := base
	invalid.PublicAddress = "files.example.com/path"
	cases := map[string]struct {
		mount   SFTPClusterMount
		cluster string
	}{
		"disabled":      {disabled, scope.DefaultCluster},
		"other cluster": {base, "remote-a"},
		"no address":    {noAddress, scope.DefaultCluster},
		"invalid addr":  {invalid, scope.DefaultCluster},
	}
	for name, tc := range cases {
		if ep, ok := LocalSFTPEndpointSource(tc.mount, scope.DefaultCluster)(t.Context(), tc.cluster); ok {
			t.Errorf("%s: endpoint = %+v; want none", name, ep)
		}
	}
}

func TestSFTPHostKeyUnit_LocalEndpointSourceWithoutFingerprint(t *testing.T) {
	k, cs := hostKeyTestClient()
	cs.PrependReactor("get", "secrets", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, hostKeyTestSecret, errors.New("rbac"))
	})
	m := SFTPClusterMount{Kube: k, Namespace: "gameplane-system", HostKeySecret: hostKeyTestSecret, Enabled: true, Port: 2222, PublicAddress: "files.example.com"}

	ep, ok := LocalSFTPEndpointSource(m, scope.DefaultCluster)(context.Background(), scope.DefaultCluster)
	if !ok || ep.Address != "files.example.com" || ep.Port != 2222 || ep.HostKeyFingerprint != "" {
		t.Fatalf("endpoint without fingerprint = %+v, %v; want address and port with an empty fingerprint", ep, ok)
	}
}
