package verify

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func testPubPEM(t *testing.T) []byte {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
}

func TestBuild_VerifyWithoutOCIErrors(t *testing.T) {
	src := &gameplanev1alpha1.ModuleSource{
		Spec: gameplanev1alpha1.ModuleSourceSpec{
			Type:   gameplanev1alpha1.ModuleSourceTypeOCI,
			Verify: &gameplanev1alpha1.VerifySpec{Key: &corev1.LocalObjectReference{Name: "k"}},
			// OCI deliberately nil.
		},
	}
	if _, err := Build(context.Background(), fake.NewClientBuilder().Build(), "ns", src); err == nil {
		t.Fatal("expected error when spec.verify is set without an oci source")
	}
}

func TestBuild_NeitherKeyNorKeylessErrors(t *testing.T) {
	src := ociSource(&gameplanev1alpha1.VerifySpec{}) // both key and keyless nil
	if _, err := Build(context.Background(), fake.NewClientBuilder().Build(), "ns", src); err == nil {
		t.Fatal("expected error when verify sets neither key nor keyless")
	}
}

func TestBuild_KeySecretMissingDataKeyErrors(t *testing.T) {
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "k", Namespace: "ns"},
		Data:       map[string][]byte{"wrong-key": []byte("x")}, // no cosign.pub
	}
	src := ociSource(&gameplanev1alpha1.VerifySpec{Key: &corev1.LocalObjectReference{Name: "k"}})
	c := fake.NewClientBuilder().WithObjects(sec).Build()
	if _, err := Build(context.Background(), c, "ns", src); err == nil {
		t.Fatal("expected error when the key secret lacks cosign.pub")
	}
}

func TestAuthFor_Branches(t *testing.T) {
	ref := &corev1.LocalObjectReference{Name: "ps"}

	t.Run("missing secret errors", func(t *testing.T) {
		_, err := authFor(context.Background(), fake.NewClientBuilder().Build(), "ns", ref, "ghcr.io")
		if err == nil {
			t.Fatal("expected error for a missing pull secret")
		}
	})

	t.Run("no dockerconfig data falls back to anonymous", func(t *testing.T) {
		sec := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "ps", Namespace: "ns"},
			Data:       map[string][]byte{},
		}
		c := fake.NewClientBuilder().WithObjects(sec).Build()
		a, err := authFor(context.Background(), c, "ns", ref, "ghcr.io")
		if err != nil || a != authn.Anonymous {
			t.Fatalf("a=%#v err=%v, want anonymous", a, err)
		}
	})

	t.Run("malformed dockerconfigjson errors", func(t *testing.T) {
		sec := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "ps", Namespace: "ns"},
			Type:       corev1.SecretTypeDockerConfigJson,
			Data:       map[string][]byte{corev1.DockerConfigJsonKey: []byte("{not json")},
		}
		c := fake.NewClientBuilder().WithObjects(sec).Build()
		if _, err := authFor(context.Background(), c, "ns", ref, "ghcr.io"); err == nil {
			t.Fatal("expected error for malformed dockerconfigjson")
		}
	})

	t.Run("auth field is used", func(t *testing.T) {
		cfg := `{"auths":{"ghcr.io":{"auth":"dXNlcjpwYXNz"}}}`
		sec := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "ps", Namespace: "ns"},
			Type:       corev1.SecretTypeDockerConfigJson,
			Data:       map[string][]byte{corev1.DockerConfigJsonKey: []byte(cfg)},
		}
		c := fake.NewClientBuilder().WithObjects(sec).Build()
		a, err := authFor(context.Background(), c, "ns", ref, "ghcr.io")
		if err != nil {
			t.Fatalf("authFor: %v", err)
		}
		got, err := a.Authorization()
		if err != nil {
			t.Fatalf("authorization: %v", err)
		}
		if got.Auth != "dXNlcjpwYXNz" {
			t.Fatalf("auth=%q, want the base64 auth blob", got.Auth)
		}
	})

	t.Run("empty auths entry falls back to anonymous", func(t *testing.T) {
		cfg := `{"auths":{"ghcr.io":{}}}`
		sec := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "ps", Namespace: "ns"},
			Type:       corev1.SecretTypeDockerConfigJson,
			Data:       map[string][]byte{corev1.DockerConfigJsonKey: []byte(cfg)},
		}
		c := fake.NewClientBuilder().WithObjects(sec).Build()
		a, err := authFor(context.Background(), c, "ns", ref, "ghcr.io")
		if err != nil || a != authn.Anonymous {
			t.Fatalf("a=%#v err=%v, want anonymous", a, err)
		}
	})
}

// The keyed path's transparency-log policy is a security control with no
// visible effect until a signature is actually rejected, so assert the
// CheckOpts it produces directly.
func TestNewKeyed_TransparencyLogPolicy(t *testing.T) {
	t.Run("default verifies offline and ignores the log", func(t *testing.T) {
		v, err := newKeyed(context.Background(), testPubPEM(t), authn.Anonymous, false, false)
		if err != nil {
			t.Fatalf("newKeyed: %v", err)
		}
		co := v.(*cosignVerifier).mkOpts(context.Background())
		if !co.IgnoreTlog {
			t.Error("IgnoreTlog = false, want true: a keyed signature is complete without a log entry, and requiring one would break air-gapped clusters")
		}
		if co.RekorPubKeys != nil {
			t.Error("RekorPubKeys set, want nil when the log is not required")
		}
		if !co.Offline {
			t.Error("Offline = false, want true")
		}
		if !co.IgnoreSCT {
			t.Error("IgnoreSCT = false, want true: a keyed signature carries no Fulcio certificate")
		}
	})

	t.Run("requireTransparencyLog enforces log inclusion", func(t *testing.T) {
		// Loading the Rekor keys reaches the Sigstore TUF root, so this can
		// only run on a networked machine. Skipping keeps an offline `go test`
		// honest instead of silently asserting nothing.
		v, err := newKeyed(context.Background(), testPubPEM(t), authn.Anonymous, false, true)
		if err != nil {
			t.Skipf("rekor public keys unavailable (offline?): %v", err)
		}
		co := v.(*cosignVerifier).mkOpts(context.Background())
		if co.IgnoreTlog {
			t.Error("IgnoreTlog = true, want false when requireTransparencyLog is set")
		}
		if co.RekorPubKeys == nil {
			t.Error("RekorPubKeys = nil, want the loaded keys so inclusion proofs can be checked")
		}
		if !co.Offline {
			t.Error("Offline = false, want true: the bundled inclusion proof is checked without a live log query")
		}
	})
}

func TestCosignVerifier_Verify(t *testing.T) {
	v, err := newKeyed(context.Background(), testPubPEM(t), authn.Anonymous, true, false)
	if err != nil {
		t.Fatalf("newKeyed: %v", err)
	}

	t.Run("malformed digest is a parse error", func(t *testing.T) {
		if err := v.Verify(context.Background(), "registry.example/x", "not-a-digest"); err == nil {
			t.Fatal("expected a parse error for a malformed digest")
		}
	})

	t.Run("unreachable registry surfaces a verify error", func(t *testing.T) {
		// 127.0.0.1:1 is always closed → cosign's signature fetch fails
		// fast. This exercises mkOpts/baseCheckOpts and the verify-error
		// branch without reaching a real registry.
		digest := "sha256:" + strings.Repeat("a", 64)
		if err := v.Verify(context.Background(), "127.0.0.1:1/x", digest); err == nil {
			t.Fatal("expected a verify error against a closed registry")
		}
	})
}
