package handlers

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/GameplanePanel/gameplane/api/internal/controlplane"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
)

func gatewayCertificateRequest(t *testing.T) clusterGatewayRequest {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, IsCA: true, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	privateKey, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certificate := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	return clusterGatewayRequest{URL: "https://gateway.example.invalid:8443", CACert: certificate, ClientCert: certificate, ClientKey: string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privateKey}))}
}

type gatewayInterleavingSecrets struct {
	kube.SecretStore
	beforeGet    func(string)
	beforeDelete func(string)
}

func (s gatewayInterleavingSecrets) Get(ctx context.Context, name string, opts metav1.GetOptions) (*corev1.Secret, error) {
	if s.beforeGet != nil {
		s.beforeGet(name)
	}
	return s.SecretStore.Get(ctx, name, opts)
}

func (s gatewayInterleavingSecrets) Delete(ctx context.Context, name string, opts metav1.DeleteOptions) error {
	if s.beforeDelete != nil {
		s.beforeDelete(name)
	}
	return s.SecretStore.Delete(ctx, name, opts)
}

type gatewayInterleavingClusters struct {
	kube.ClusterStore
	beforeUpdate func()
}

func (s gatewayInterleavingClusters) Update(ctx context.Context, obj *unstructured.Unstructured, opts metav1.UpdateOptions, subresources ...string) (*unstructured.Unstructured, error) {
	s.beforeUpdate()
	return s.ClusterStore.Update(ctx, obj, opts, subresources...)
}

func TestGatewayRemovalCannotDeleteConcurrentRotation(t *testing.T) {
	_, home, reg, _ := standaloneHandlerStore(t)
	if _, err := home.Clusters().Create(t.Context(), newCluster("remote", map[string]any{}, nil), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	router := standaloneClustersRouter(home, reg)
	original := gatewayCertificateRequest(t)
	if rr := doClusters(t, router, http.MethodPut, "/clusters/remote/gateway", original); rr.Code != http.StatusNoContent {
		t.Fatalf("initial configure: %d %s", rr.Code, rr.Body)
	}
	registration, err := home.Clusters().Get(t.Context(), "remote", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	oldSecret := gatewayCredentialName(registration)
	rotated := gatewayCertificateRequest(t)
	rotated.URL = "https://rotated.example.invalid"
	secrets := home.SecretStore
	interleaved := false
	home.SecretStore = func(ns string) kube.SecretStore {
		return gatewayInterleavingSecrets{SecretStore: secrets(ns), beforeGet: func(name string) {
			if name != oldSecret || interleaved {
				return
			}
			interleaved = true
			// DELETE has detached the old pointer but has not read its secret.
			if rr := doClusters(t, router, http.MethodPut, "/clusters/remote/gateway", rotated); rr.Code != http.StatusNoContent {
				t.Fatalf("concurrent configure: %d %s", rr.Code, rr.Body)
			}
		}}
	}
	if rr := doClusters(t, router, http.MethodDelete, "/clusters/remote/gateway", nil); rr.Code != http.StatusNoContent {
		t.Fatalf("remove: %d %s", rr.Code, rr.Body)
	}
	if !interleaved {
		t.Fatal("removal never reached credential cleanup")
	}
	assertLiveGateway(t, home, rotated)
	if _, err := secrets(standaloneTestNamespace).Get(t.Context(), oldSecret, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("old credential survived removal: %v", err)
	}
}

func TestConcurrentGatewayConfigurationsKeepURLAndCredentialsTogether(t *testing.T) {
	_, home, reg, _ := standaloneHandlerStore(t)
	if _, err := home.Clusters().Create(t.Context(), newCluster("remote", map[string]any{}, nil), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	router := standaloneClustersRouter(home, reg)
	first, second := gatewayCertificateRequest(t), gatewayCertificateRequest(t)
	second.URL = "https://second.example.invalid"
	interleaved := false
	home.ClusterStore = gatewayInterleavingClusters{ClusterStore: home.ClusterStore, beforeUpdate: func() {
		if interleaved {
			return
		}
		interleaved = true
		// The first request has persisted its key and read the registration.
		if rr := doClusters(t, router, http.MethodPut, "/clusters/remote/gateway", second); rr.Code != http.StatusNoContent {
			t.Fatalf("second configure: %d %s", rr.Code, rr.Body)
		}
	}}
	if rr := doClusters(t, router, http.MethodPut, "/clusters/remote/gateway", first); rr.Code != http.StatusConflict {
		t.Fatalf("stale configure: %d %s", rr.Code, rr.Body)
	}
	assertLiveGateway(t, home, second)
	secrets, err := home.Secrets(standaloneTestNamespace).List(t.Context(), metav1.ListOptions{LabelSelector: clusterGatewayLabel + "=true"})
	if err != nil || len(secrets.Items) != 1 {
		t.Fatalf("unpublished credential was not cleaned up: %+v %v", secrets, err)
	}
}

func assertLiveGateway(t *testing.T, home *kube.Client, want clusterGatewayRequest) {
	t.Helper()
	registration, err := home.Clusters().Get(t.Context(), "remote", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	endpoint, _, _ := unstructured.NestedString(registration.Object, "spec", "agentGateway", "url")
	secret, err := home.Secrets(standaloneTestNamespace).Get(t.Context(), gatewayCredentialName(registration), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("live gateway has no credentials: %v", err)
	}
	if endpoint != want.URL || string(secret.Data["tls.key"]) != want.ClientKey || string(secret.Data["tls.crt"]) != want.ClientCert || string(secret.Data["ca.crt"]) != want.CACert {
		t.Fatal("gateway URL and credentials came from different writes")
	}
}

func TestGatewayRotationAndClusterDeletionCleanOnlyDetachedCredentials(t *testing.T) {
	_, home, reg, _ := standaloneHandlerStore(t)
	if _, err := home.Clusters().Create(t.Context(), newCluster("remote", map[string]any{}, nil), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	router := standaloneClustersRouter(home, reg)
	previous := ""
	for range 2 {
		in := gatewayCertificateRequest(t)
		if rr := doClusters(t, router, http.MethodPut, "/clusters/remote/gateway", in); rr.Code != http.StatusNoContent {
			t.Fatalf("configure: %d %s", rr.Code, rr.Body)
		}
		assertLiveGateway(t, home, in)
		registration, err := home.Clusters().Get(t.Context(), "remote", metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		current := gatewayCredentialName(registration)
		if current == previous {
			t.Fatal("rotation reused a credential identity")
		}
		if previous != "" {
			if _, err := home.Secrets(standaloneTestNamespace).Get(t.Context(), previous, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
				t.Fatalf("detached credential survived rotation: %v", err)
			}
		}
		previous = current
	}
	if rr := doClusters(t, router, http.MethodDelete, "/clusters/remote", nil); rr.Code != http.StatusNoContent {
		t.Fatalf("delete cluster: %d %s", rr.Code, rr.Body)
	}
	if _, err := home.Secrets(standaloneTestNamespace).Get(t.Context(), previous, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("gateway credential survived cluster removal: %v", err)
	}
}

func TestClusterDeletionPreservesRecreatedKubeconfig(t *testing.T) {
	_, home, reg, _ := standaloneHandlerStore(t)
	name := clusterKubeconfigSecretName("remote")
	registration := newCluster("remote", map[string]any{"kubeconfigSecret": map[string]any{"name": name}}, nil)
	if _, err := home.Clusters().Create(t.Context(), registration, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	secrets := home.SecretStore
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{kube.ClusterKubeconfigLabel: "true"}}, StringData: map[string]string{"kubeconfig": "old-credentials"}}
	if _, err := secrets(standaloneTestNamespace).Create(t.Context(), secret, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	interleaved := false
	home.SecretStore = func(ns string) kube.SecretStore {
		return gatewayInterleavingSecrets{SecretStore: secrets(ns), beforeDelete: func(deleting string) {
			if deleting != name || interleaved {
				return
			}
			interleaved = true
			if err := secrets(ns).Delete(t.Context(), name, metav1.DeleteOptions{}); err != nil {
				t.Fatal(err)
			}
			secret.StringData["kubeconfig"] = "replacement-credentials"
			if _, err := secrets(ns).Create(t.Context(), secret, metav1.CreateOptions{}); err != nil {
				t.Fatal(err)
			}
			if _, err := home.Clusters().Create(t.Context(), registration, metav1.CreateOptions{}); err != nil {
				t.Fatal(err)
			}
		}}
	}
	if rr := doClusters(t, standaloneClustersRouter(home, reg), http.MethodDelete, "/clusters/remote", nil); rr.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rr.Code, rr.Body)
	}
	current, err := secrets(standaloneTestNamespace).Get(t.Context(), name, metav1.GetOptions{})
	if !interleaved || err != nil || string(current.Data["kubeconfig"]) != "replacement-credentials" {
		t.Fatalf("replacement kubeconfig was removed: %v", err)
	}
}

func TestStandaloneGatewayConfigurationEncryptsCredentialsAndStoresOnlyReference(t *testing.T) {
	store, home, reg, keyPath := standaloneHandlerStore(t)
	if _, err := home.Clusters().Create(t.Context(), newCluster("remote", map[string]any{}, nil), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	in := gatewayCertificateRequest(t)
	router := standaloneClustersRouter(home, reg)
	configured := doClusters(t, router, http.MethodPut, "/clusters/remote/gateway", in)
	if configured.Code != http.StatusNoContent || configured.Body.Len() != 0 {
		t.Fatalf("configure: %d %s", configured.Code, configured.Body)
	}
	registration, err := home.Clusters().Get(t.Context(), "remote", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	secretName := gatewayCredentialName(registration)
	if !strings.HasPrefix(secretName, gatewaySecretName("remote")+"-") {
		t.Fatalf("credential does not have a unique generated name: %s", secretName)
	}
	assertManagementCiphertext(t, store, secretName, in.ClientKey, in.ClientCert)
	home, err = controlplane.New(t.Context(), store, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := home.Secrets(standaloneTestNamespace).Get(t.Context(), secretName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if string(stored.Data["tls.key"]) != in.ClientKey || string(stored.Data["tls.crt"]) != in.ClientCert || string(stored.Data["ca.crt"]) != in.CACert {
		t.Fatal("gateway credentials did not survive reopen")
	}
	if stored.Labels[clusterGatewayLabel] != "true" || stored.Labels[ManagedByLabel] != managedByValue {
		t.Fatal("gateway credential ownership labels missing")
	}
	if stored.Immutable == nil || !*stored.Immutable {
		t.Fatal("gateway credentials must be immutable")
	}
	registration, err = home.Clusters().Get(t.Context(), "remote", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	gateway, found, err := unstructured.NestedMap(registration.Object, "spec", "agentGateway")
	if err != nil || !found || len(gateway) != 2 || gateway["url"] != in.URL {
		t.Fatalf("unexpected gateway registration: %+v %v", gateway, err)
	}
	ref, _, _ := unstructured.NestedString(registration.Object, "spec", "agentGateway", "tlsSecretRef", "name")
	if ref != secretName {
		t.Fatalf("wrong credential reference: %s", ref)
	}
	var payload string
	if err := store.DB.QueryRowContext(t.Context(), `SELECT payload FROM management_objects WHERE kind = 'clusters' AND name = 'remote'`).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(payload, "PRIVATE KEY") || strings.Contains(payload, "CERTIFICATE") || strings.Contains(payload, "clientKey") {
		t.Fatal("registration contains credential material")
	}
	listed := doClusters(t, router, http.MethodGet, "/clusters/", nil)
	if listed.Code != http.StatusOK || strings.Contains(listed.Body.String(), "gateway.example") || strings.Contains(listed.Body.String(), "PRIVATE KEY") {
		t.Fatalf("gateway leaked in discovery: %d %s", listed.Code, listed.Body)
	}
}

func TestGatewayRejectsNonHTTPSOriginsAndInvalidPEMWithoutWritingSecrets(t *testing.T) {
	valid := gatewayCertificateRequest(t)
	for _, badURL := range []string{"http://gateway.example.invalid", "https://user:password@gateway.example.invalid", "https://gateway.example.invalid/path", "https://gateway.example.invalid?token=private", "https://gateway.example.invalid#fragment", "https://"} {
		t.Run(badURL, func(t *testing.T) {
			_, home, reg, _ := standaloneHandlerStore(t)
			if _, err := home.Clusters().Create(t.Context(), newCluster("remote", map[string]any{}, nil), metav1.CreateOptions{}); err != nil {
				t.Fatal(err)
			}
			in := valid
			in.URL = badURL
			rr := doClusters(t, standaloneClustersRouter(home, reg), http.MethodPut, "/clusters/remote/gateway", in)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("invalid URL accepted: %d %s", rr.Code, rr.Body)
			}
			if secrets, err := home.Secrets(standaloneTestNamespace).List(t.Context(), metav1.ListOptions{}); err != nil || len(secrets.Items) != 0 {
				t.Fatalf("invalid URL wrote credentials: %+v %v", secrets, err)
			}
		})
	}
	for _, field := range []string{"ca", "certificate", "key"} {
		t.Run(field, func(t *testing.T) {
			_, home, reg, _ := standaloneHandlerStore(t)
			if _, err := home.Clusters().Create(t.Context(), newCluster("remote", map[string]any{}, nil), metav1.CreateOptions{}); err != nil {
				t.Fatal(err)
			}
			in := valid
			switch field {
			case "ca":
				in.CACert = "private-invalid-ca"
			case "certificate":
				in.ClientCert = "private-invalid-cert"
			case "key":
				in.ClientKey = "private-invalid-key"
			}
			rr := doClusters(t, standaloneClustersRouter(home, reg), http.MethodPut, "/clusters/remote/gateway", in)
			if rr.Code != http.StatusBadRequest || strings.Contains(rr.Body.String(), "private-invalid") {
				t.Fatalf("invalid PEM response: %d %s", rr.Code, rr.Body)
			}
			if secrets, err := home.Secrets(standaloneTestNamespace).List(t.Context(), metav1.ListOptions{}); err != nil || len(secrets.Items) != 0 {
				t.Fatalf("invalid PEM wrote credentials: %+v %v", secrets, err)
			}
		})
	}
}

func TestGatewayRemovalDeletesOnlyGeneratedSecretWithBothOwnershipLabels(t *testing.T) {
	for _, tc := range []struct {
		name, secretName string
		labels           map[string]string
		deleted          bool
	}{
		{"managed", gatewaySecretName("remote"), map[string]string{clusterGatewayLabel: "true", ManagedByLabel: managedByValue}, true},
		{"missing-feature-label", gatewaySecretName("remote"), map[string]string{ManagedByLabel: managedByValue}, false},
		{"gitops-secret", gatewaySecretName("remote"), map[string]string{clusterGatewayLabel: "true"}, false},
		{"external-reference", "externally-owned-gateway", map[string]string{clusterGatewayLabel: "true", ManagedByLabel: managedByValue}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, home, reg, _ := standaloneHandlerStore(t)
			_, err := home.Clusters().Create(t.Context(), newCluster("remote", map[string]any{"agentGateway": map[string]any{"url": "https://gateway.example.invalid", "tlsSecretRef": map[string]any{"name": tc.secretName}}}, nil), metav1.CreateOptions{})
			if err != nil {
				t.Fatal(err)
			}
			_, err = home.Secrets(standaloneTestNamespace).Create(t.Context(), &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: tc.secretName, Labels: tc.labels}, StringData: map[string]string{"tls.key": "private-key-value"}}, metav1.CreateOptions{})
			if err != nil {
				t.Fatal(err)
			}
			rr := doClusters(t, standaloneClustersRouter(home, reg), http.MethodDelete, "/clusters/remote/gateway", nil)
			if rr.Code != http.StatusNoContent {
				t.Fatalf("remove: %d %s", rr.Code, rr.Body)
			}
			_, err = home.Secrets(standaloneTestNamespace).Get(t.Context(), tc.secretName, metav1.GetOptions{})
			if tc.deleted && !apierrors.IsNotFound(err) {
				t.Fatalf("managed secret survived: %v", err)
			}
			if !tc.deleted && err != nil {
				t.Fatalf("unowned secret removed: %v", err)
			}
			registration, err := home.Clusters().Get(t.Context(), "remote", metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if _, found, err := unstructured.NestedMap(registration.Object, "spec", "agentGateway"); err != nil || found {
				t.Fatalf("gateway reference survived: %v", err)
			}
		})
	}
}
