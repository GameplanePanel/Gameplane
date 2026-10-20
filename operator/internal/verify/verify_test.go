package verify

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func ociSource(verify *gameplanev1alpha1.VerifySpec) *gameplanev1alpha1.ModuleSource {
	return &gameplanev1alpha1.ModuleSource{
		Spec: gameplanev1alpha1.ModuleSourceSpec{
			Type:   gameplanev1alpha1.ModuleSourceTypeOCI,
			OCI:    &gameplanev1alpha1.OCISourceSpec{URL: "ghcr.io/test/modules"},
			Verify: verify,
		},
	}
}

func TestBuild_NilVerifyReturnsNop(t *testing.T) {
	v, err := Build(context.Background(), fake.NewClientBuilder().Build(), "ns", ociSource(nil))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, ok := v.(Nop); !ok {
		t.Fatalf("want Nop, got %T", v)
	}
}

func TestBuild_KeyMissingSecretErrors(t *testing.T) {
	src := ociSource(&gameplanev1alpha1.VerifySpec{Key: &corev1.LocalObjectReference{Name: "missing"}})
	if _, err := Build(context.Background(), fake.NewClientBuilder().Build(), "ns", src); err == nil {
		t.Fatal("expected error for missing key secret")
	}
}

func TestBuild_KeyBadPEMErrors(t *testing.T) {
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "k", Namespace: "ns"},
		Data:       map[string][]byte{cosignPubKey: []byte("not a key")},
	}
	src := ociSource(&gameplanev1alpha1.VerifySpec{Key: &corev1.LocalObjectReference{Name: "k"}})
	c := fake.NewClientBuilder().WithObjects(sec).Build()
	verifier, err := Build(t.Context(), c, "ns", src)
	if err == nil {
		t.Fatal("expected error for malformed public key")
	}
	if verifier != nil {
		t.Fatal("malformed public key returned a verifier")
	}
}

func TestBuild_KeyedTrustFailureDoesNotReturnVerifier(t *testing.T) {
	// A valid signing key proceeds to Rekor trust loading. Keep this failure
	// deterministic and offline; no TUF/network request should be necessary.
	t.Setenv("SIGSTORE_REKOR_PUBLIC_KEY", filepath.Join(t.TempDir(), "missing-rekor.pub"))
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "key", Namespace: "ns"},
		Data:       map[string][]byte{cosignPubKey: testPubPEM(t)},
	}
	src := ociSource(&gameplanev1alpha1.VerifySpec{
		Key: &corev1.LocalObjectReference{Name: sec.Name}, RequireTransparencyLog: true,
	})
	c := fake.NewClientBuilder().WithObjects(sec).Build()
	verifier, err := Build(t.Context(), c, "ns", src)
	if err == nil || !strings.Contains(err.Error(), "load rekor public keys") {
		t.Fatalf("expected Rekor trust-loading error, got %v", err)
	}
	if verifier != nil {
		t.Fatal("failed trust loading returned a verifier")
	}
}

func TestDockerHubSignatureCredentials(t *testing.T) {
	for _, source := range []string{"docker.io/org/modules", "index.docker.io/org/modules", "registry-1.docker.io/org/modules"} {
		for _, credentialHost := range []string{"registry-1.docker.io", "index.docker.io", "docker.io"} {
			t.Run(source+"/"+credentialHost, func(t *testing.T) {
				// ORAS uses registry-1.docker.io for a docker.io source, while
				// the signature library normalizes docker.io to index.docker.io.
				repository, err := name.NewRepository(source)
				if err != nil {
					t.Fatal(err)
				}
				cfg, err := json.Marshal(map[string]any{"auths": map[string]any{
					credentialHost:      map[string]string{"username": "hub-user", "password": "hub-password"},
					"unrelated.example": map[string]string{"username": "private-user", "password": "private-password"},
				}})
				if err != nil {
					t.Fatal(err)
				}
				sec := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "pull", Namespace: "ns"}, Data: map[string][]byte{corev1.DockerConfigJsonKey: cfg}}
				c := fake.NewClientBuilder().WithObjects(sec).Build()
				ref := &corev1.LocalObjectReference{Name: sec.Name}
				a, err := authFor(t.Context(), c, "ns", ref, repository.RegistryStr())
				if err != nil {
					t.Fatal(err)
				}
				credentials, err := a.Authorization()
				if err != nil {
					t.Fatal(err)
				}
				if credentials.Username != "hub-user" || credentials.Password != "hub-password" {
					t.Fatal("Docker Hub credentials were not selected")
				}
				for _, foreign := range []string{"docker.io.evil.example", "registry-1.docker.io:5000", "other.example"} {
					a, err := authFor(t.Context(), c, "ns", ref, foreign)
					if err != nil {
						t.Fatal(err)
					}
					if a != authn.Anonymous {
						t.Fatalf("Hub credentials escaped to %s", foreign)
					}
				}
			})
		}
	}
}

func TestBuild_KeyValidPEMReturnsVerifier(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})

	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "k", Namespace: "ns"},
		Data:       map[string][]byte{cosignPubKey: pubPEM},
	}
	src := ociSource(&gameplanev1alpha1.VerifySpec{Key: &corev1.LocalObjectReference{Name: "k"}})
	c := fake.NewClientBuilder().WithObjects(sec).Build()
	v, err := Build(context.Background(), c, "ns", src)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, ok := v.(Nop); ok {
		t.Fatal("expected a real verifier, got Nop")
	}
}

func TestBuild_OCIRegistryPrefixes(t *testing.T) {
	key := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "key", Namespace: "ns"}, Data: map[string][]byte{cosignPubKey: testPubPEM(t)}}
	c := fake.NewClientBuilder().WithObjects(key).Build()
	for _, tc := range []struct {
		prefix   string
		registry string
	}{
		{prefix: "ghcr.io", registry: "ghcr.io"},
		{prefix: "ghcr.io/", registry: "ghcr.io"},
		{prefix: "ghcr.io/org/modules", registry: "ghcr.io"},
		{prefix: "ghcr.io/org/modules/", registry: "ghcr.io"},
		{prefix: "gameplane-test-registry.gameplane-system.svc:5000", registry: "gameplane-test-registry.gameplane-system.svc:5000"},
		{prefix: "localhost", registry: "localhost"},
		{prefix: "localhost:5000/", registry: "localhost:5000"},
		{prefix: "localhost:5001/modules", registry: "localhost:5001"},
		{prefix: "[::1]:5000", registry: "[::1]:5000"},
		{prefix: "docker.io", registry: "index.docker.io"},
		{prefix: "docker.io/", registry: "index.docker.io"},
		{prefix: "docker.io/org/modules", registry: "index.docker.io"},
		{prefix: "index.docker.io", registry: "index.docker.io"},
		{prefix: "registry-1.docker.io", registry: "registry-1.docker.io"},
		{prefix: "org/modules", registry: "index.docker.io"},
		{prefix: ""},
		{prefix: "/"},
		{prefix: "https://ghcr.io/org/modules"},
		{prefix: "http://localhost:5000"},
		{prefix: "ghcr.io/modules:latest"},
		{prefix: "ghcr.io/modules@sha256:" + strings.Repeat("a", 64)},
		{prefix: "ghcr.io/invalid path"},
		{prefix: "ghcr.io/modules?query=value"},
		{prefix: "localhost:invalid"},
	} {
		t.Run(tc.prefix, func(t *testing.T) {
			src := ociSource(&gameplanev1alpha1.VerifySpec{Key: &corev1.LocalObjectReference{Name: key.Name}})
			src.Spec.OCI.URL = tc.prefix
			v, err := Build(t.Context(), c, "ns", src)
			if tc.registry == "" {
				if err == nil || !strings.Contains(err.Error(), "parse OCI source registry") || v != nil {
					t.Fatalf("malformed prefix returned verifier %T, error %v", v, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			guard, ok := v.(*registryVerifier)
			if !ok {
				t.Fatalf("expected registry verifier, got %T", v)
			}
			if guard.registry != tc.registry {
				t.Fatalf("registry = %q, want %q", guard.registry, tc.registry)
			}
		})
	}
}

func TestAuthFor(t *testing.T) {
	t.Run("anonymous when no ref", func(t *testing.T) {
		a, err := authFor(context.Background(), fake.NewClientBuilder().Build(), "ns", nil, "ghcr.io")
		if err != nil {
			t.Fatalf("authFor: %v", err)
		}
		if a != authn.Anonymous {
			t.Fatalf("want anonymous, got %#v", a)
		}
	})

	t.Run("from dockerconfigjson", func(t *testing.T) {
		cfg := `{"auths":{"ghcr.io":{"username":"u","password":"p"}}}`
		sec := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "ps", Namespace: "ns"},
			Type:       corev1.SecretTypeDockerConfigJson,
			Data:       map[string][]byte{corev1.DockerConfigJsonKey: []byte(cfg)},
		}
		c := fake.NewClientBuilder().WithObjects(sec).Build()
		a, err := authFor(context.Background(), c, "ns", &corev1.LocalObjectReference{Name: "ps"}, "ghcr.io")
		if err != nil {
			t.Fatalf("authFor: %v", err)
		}
		got, err := a.Authorization()
		if err != nil {
			t.Fatalf("authorization: %v", err)
		}
		if got.Username != "u" || got.Password != "p" {
			t.Fatalf("creds = %q/%q, want u/p", got.Username, got.Password)
		}
	})
}

func TestSignatureCredentialsStayWithTheirRegistry(t *testing.T) {
	var requests, leaked atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/" {
			w.WriteHeader(http.StatusOK)
			return
		}
		requests.Add(1)
		user, password, ok := r.BasicAuth()
		if !ok || user != "target-user" || password != "target-password" {
			leaked.Add(1)
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	host := strings.TrimPrefix(server.URL, "http://")
	cfg, err := json.Marshal(map[string]any{"auths": map[string]any{
		"unrelated.example": map[string]string{"username": "private-user", "password": "private-password"},
		host:                map[string]string{"username": "target-user", "password": "target-password"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	sec := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "pull", Namespace: "ns"}, Data: map[string][]byte{corev1.DockerConfigJsonKey: cfg}}
	key := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "key", Namespace: "ns"}, Data: map[string][]byte{cosignPubKey: testPubPEM(t)}}
	c := fake.NewClientBuilder().WithObjects(sec, key).Build()
	ref := &corev1.LocalObjectReference{Name: "pull"}
	src := ociSource(&gameplanev1alpha1.VerifySpec{Key: &corev1.LocalObjectReference{Name: "key"}})
	src.Spec.OCI.URL = host + "/modules"
	src.Spec.OCI.Insecure = true
	src.Spec.OCI.PullSecretRef = ref
	for _, prefix := range []string{host, host + "/", host + "/modules", host + "/modules/"} {
		t.Run(prefix, func(t *testing.T) {
			src.Spec.OCI.URL = prefix
			before := requests.Load()
			for range 20 {
				v, err := Build(t.Context(), c, "ns", src)
				if err != nil {
					t.Fatal(err)
				}
				// No signature is served; reaching the registry with the right credentials
				// is the assertion, not accepting an unsigned bundle.
				if err := v.Verify(t.Context(), host+"/module", "sha256:"+strings.Repeat("a", 64)); err == nil {
					t.Fatal("unsigned bundle accepted")
				}
			}
			if requests.Load() == before {
				t.Fatal("signature verification did not contact the source registry")
			}
		})
	}
	if requests.Load() == 0 || leaked.Load() != 0 {
		t.Fatalf("signature requests=%d, wrong credentials=%d", requests.Load(), leaked.Load())
	}
	for _, unmatched := range []string{"other.example", host + ".other.example"} {
		a, err := authFor(t.Context(), c, "ns", ref, unmatched)
		if err != nil {
			t.Fatal(err)
		}
		if a != authn.Anonymous {
			t.Fatalf("credentials selected for unmatched registry %s", unmatched)
		}
	}
	var foreignRequests atomic.Int32
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		foreignRequests.Add(1)
		http.NotFound(w, r)
	}))
	defer foreign.Close()
	v, err := Build(t.Context(), c, "ns", src)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Verify(t.Context(), strings.TrimPrefix(foreign.URL, "http://")+"/module", "sha256:"+strings.Repeat("a", 64)); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("wrong registry error = %v", err)
	}
	if foreignRequests.Load() != 0 {
		t.Fatal("stale catalog reference contacted another registry")
	}
}
