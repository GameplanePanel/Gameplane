package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

func newGameServer(ns, name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "gameplane.local/v1alpha1",
			"kind":       "GameServer",
			"metadata": map[string]any{
				"name":      name,
				"namespace": ns,
				"uid":       "test-uid-12345",
			},
			"spec": map[string]any{
				"template": "minecraft-java",
				"networking": map[string]any{
					"expose": "ClusterIP",
				},
			},
		},
	}
}

func newTunnelCredsRouter(k *kube.Client) *chi.Mux {
	r := chi.NewRouter()
	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.Set(scope.DefaultCluster, k)
	MountTunnelCredentials(r, reg)
	return r
}

func doTunnelReq(t *testing.T, h http.Handler, method, path string, body any) (int, []byte) {
	t.Helper()
	var buf io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		buf = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, path, buf)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	bodyOut, _ := io.ReadAll(rr.Body)
	return rr.Code, bodyOut
}

// syncSecretData populates Secret.Data from Secret.StringData.
// The fake typed client doesn't automatically convert StringData to Data like
// the production Kubernetes API does, so tests must do this manually when
// they need the handler to read from Data (e.g., in GET handlers that check keys).
func syncSecretData(t *testing.T, k *kube.Client, ns, secretName string) {
	t.Helper()
	secret, err := k.Typed.CoreV1().Secrets(ns).Get(context.Background(), secretName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret for sync: %v", err)
	}
	if secret.Data == nil {
		secret.Data = make(map[string][]byte)
	}
	for key, val := range secret.StringData {
		secret.Data[key] = []byte(val)
	}
	_, err = k.Typed.CoreV1().Secrets(ns).Update(context.Background(), secret, metav1.UpdateOptions{})
	if err != nil {
		t.Fatalf("update secret: %v", err)
	}
}

func TestTunnelCreds_PutFrp(t *testing.T) {
	gs := newGameServer("gameplane-games", "test-server")
	k := fakeKubeClient(gs)
	router := newTunnelCredsRouter(k)

	body := putReq{
		Provider: "frp",
		Values:   map[string]string{"token": "my-secret-token"},
	}
	status, respBody := doTunnelReq(t, router, "PUT", "/servers/test-server:tunnel-credentials", body)
	if status != http.StatusNoContent {
		t.Fatalf("PUT status = %d, want 204; body=%s", status, respBody)
	}

	// Verify Secret was created with the correct keys.
	secret, err := k.Typed.CoreV1().Secrets("gameplane-games").Get(context.Background(), "test-server-tunnel-auth", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	if secret.StringData["token"] != "my-secret-token" {
		t.Fatalf("secret token = %q, want %q", secret.StringData["token"], "my-secret-token")
	}

	// Verify Secret has owner reference.
	if len(secret.OwnerReferences) == 0 {
		t.Fatal("secret missing owner reference")
	}
	if secret.OwnerReferences[0].Name != "test-server" {
		t.Fatalf("owner name = %q, want test-server", secret.OwnerReferences[0].Name)
	}

	// Verify GameServer was patched with credentialsSecretRef.
	updated, err := k.Dynamic.Resource(kube.GVRs["servers"]).
		Namespace("gameplane-games").
		Get(context.Background(), "test-server", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get updated server: %v", err)
	}
	secretRef, ok, _ := getNestedString(updated.Object, "spec", "networking", "tunnel", "credentialsSecretRef", "name")
	if !ok || secretRef != "test-server-tunnel-auth" {
		t.Fatalf("credentialsSecretRef = %q (ok=%v), want test-server-tunnel-auth", secretRef, ok)
	}
}

func TestTunnelCreds_PutTailscale(t *testing.T) {
	gs := newGameServer("gameplane-games", "ts-server")
	k := fakeKubeClient(gs)
	router := newTunnelCredsRouter(k)

	body := putReq{
		Provider: "tailscale",
		Values:   map[string]string{"authKey": "tskey-client-xxxxx"},
	}
	status, _ := doTunnelReq(t, router, "PUT", "/servers/ts-server:tunnel-credentials", body)
	if status != http.StatusNoContent {
		t.Fatalf("PUT status = %d, want 204", status)
	}

	secret, err := k.Typed.CoreV1().Secrets("gameplane-games").Get(context.Background(), "ts-server-tunnel-auth", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	if secret.StringData["authKey"] != "tskey-client-xxxxx" {
		t.Fatalf("secret authKey mismatch")
	}
}

func TestTunnelCreds_PutPlayit(t *testing.T) {
	gs := newGameServer("gameplane-games", "playit-server")
	k := fakeKubeClient(gs)
	router := newTunnelCredsRouter(k)

	body := putReq{
		Provider: "playit",
		Values:   map[string]string{"secretKey": "pk_live_xxxxx"},
	}
	status, _ := doTunnelReq(t, router, "PUT", "/servers/playit-server:tunnel-credentials", body)
	if status != http.StatusNoContent {
		t.Fatalf("PUT status = %d, want 204", status)
	}

	secret, err := k.Typed.CoreV1().Secrets("gameplane-games").Get(context.Background(), "playit-server-tunnel-auth", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	if secret.StringData["secretKey"] != "pk_live_xxxxx" {
		t.Fatalf("secret secretKey mismatch")
	}
}

func TestTunnelCreds_PutWrongKeys(t *testing.T) {
	gs := newGameServer("gameplane-games", "test-server")
	k := fakeKubeClient(gs)
	router := newTunnelCredsRouter(k)

	// frp expects "token", not "apiKey"
	body := putReq{
		Provider: "frp",
		Values:   map[string]string{"apiKey": "wrong-key"},
	}
	status, respBody := doTunnelReq(t, router, "PUT", "/servers/test-server:tunnel-credentials", body)
	if status == http.StatusNoContent {
		t.Fatalf("PUT with wrong keys should fail, got %d", status)
	}
	if !bytes.Contains(respBody, []byte("missing required key")) {
		t.Fatalf("error should mention missing key, got: %s", respBody)
	}
}

func TestTunnelCreds_PutEmptyValue(t *testing.T) {
	gs := newGameServer("gameplane-games", "test-server")
	k := fakeKubeClient(gs)
	router := newTunnelCredsRouter(k)

	body := putReq{
		Provider: "frp",
		Values:   map[string]string{"token": ""},
	}
	status, respBody := doTunnelReq(t, router, "PUT", "/servers/test-server:tunnel-credentials", body)
	if status == http.StatusNoContent {
		t.Fatalf("PUT with empty value should fail, got %d", status)
	}
	if !bytes.Contains(respBody, []byte("empty value")) {
		t.Fatalf("error should mention empty value, got: %s", respBody)
	}
}

func TestTunnelCreds_PutUnknownProvider(t *testing.T) {
	gs := newGameServer("gameplane-games", "test-server")
	k := fakeKubeClient(gs)
	router := newTunnelCredsRouter(k)

	body := putReq{
		Provider: "wireguard",
		Values:   map[string]string{"key": "value"},
	}
	status, respBody := doTunnelReq(t, router, "PUT", "/servers/test-server:tunnel-credentials", body)
	if status == http.StatusNoContent {
		t.Fatalf("PUT with unknown provider should fail, got %d", status)
	}
	if !bytes.Contains(respBody, []byte("unknown provider")) {
		t.Fatalf("error should mention unknown provider, got: %s", respBody)
	}
}

func TestTunnelCreds_PutUpsert(t *testing.T) {
	gs := newGameServer("gameplane-games", "test-server")
	k := fakeKubeClient(gs)
	router := newTunnelCredsRouter(k)

	// First PUT
	body := putReq{
		Provider: "frp",
		Values:   map[string]string{"token": "token-v1"},
	}
	status, _ := doTunnelReq(t, router, "PUT", "/servers/test-server:tunnel-credentials", body)
	if status != http.StatusNoContent {
		t.Fatalf("first PUT failed: %d", status)
	}

	secret, _ := k.Typed.CoreV1().Secrets("gameplane-games").Get(context.Background(), "test-server-tunnel-auth", metav1.GetOptions{})
	if secret.StringData["token"] != "token-v1" {
		t.Fatalf("first token = %q, want token-v1", secret.StringData["token"])
	}

	// Second PUT to update (rotation)
	body2 := putReq{
		Provider: "frp",
		Values:   map[string]string{"token": "token-v2"},
	}
	status, _ = doTunnelReq(t, router, "PUT", "/servers/test-server:tunnel-credentials", body2)
	if status != http.StatusNoContent {
		t.Fatalf("second PUT failed: %d", status)
	}

	// Verify the rotated value is present in the Secret.
	secret, _ = k.Typed.CoreV1().Secrets("gameplane-games").Get(context.Background(), "test-server-tunnel-auth", metav1.GetOptions{})
	if secret.StringData["token"] != "token-v2" {
		t.Fatalf("rotated token = %q, want token-v2", secret.StringData["token"])
	}
}

// TestTunnelCreds_RotationPreservesExtraFields verifies that PUT rotation via
// the create-then-patch pattern preserves fields an admin may have added.
func TestTunnelCreds_RotationPreservesExtraFields(t *testing.T) {
	gs := newGameServer("gameplane-games", "test-server")
	k := fakeKubeClient(gs)
	router := newTunnelCredsRouter(k)

	// First PUT creates the Secret with just the credential.
	body := putReq{
		Provider: "frp",
		Values:   map[string]string{"token": "token-v1"},
	}
	status, _ := doTunnelReq(t, router, "PUT", "/servers/test-server:tunnel-credentials", body)
	if status != http.StatusNoContent {
		t.Fatalf("first PUT failed: %d", status)
	}

	// Simulate an admin adding an extra field via kubectl.
	secret, _ := k.Typed.CoreV1().Secrets("gameplane-games").Get(context.Background(), "test-server-tunnel-auth", metav1.GetOptions{})
	secret.StringData["admin-note"] = "pinned-to-v2.0"
	k.Typed.CoreV1().Secrets("gameplane-games").Update(context.Background(), secret, metav1.UpdateOptions{})

	// Second PUT rotates the credential without nuking the admin field.
	body2 := putReq{
		Provider: "frp",
		Values:   map[string]string{"token": "token-v2"},
	}
	status, _ = doTunnelReq(t, router, "PUT", "/servers/test-server:tunnel-credentials", body2)
	if status != http.StatusNoContent {
		t.Fatalf("rotation PUT failed: %d", status)
	}

	// Verify both the new token and the admin field are present.
	secret, _ = k.Typed.CoreV1().Secrets("gameplane-games").Get(context.Background(), "test-server-tunnel-auth", metav1.GetOptions{})
	if secret.StringData["token"] != "token-v2" {
		t.Fatalf("rotated token = %q, want token-v2", secret.StringData["token"])
	}
	if secret.StringData["admin-note"] != "pinned-to-v2.0" {
		t.Fatalf("admin field was lost during rotation")
	}
}

func TestTunnelCreds_GetNotConfigured(t *testing.T) {
	gs := newGameServer("gameplane-games", "test-server")
	k := fakeKubeClient(gs)
	router := newTunnelCredsRouter(k)

	status, respBody := doTunnelReq(t, router, "GET", "/servers/test-server:tunnel-credentials", nil)
	if status != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", status)
	}

	var resp getResp
	if err := json.Unmarshal(respBody, &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Configured {
		t.Fatal("should report not configured")
	}
	if resp.SecretName != "" || len(resp.Keys) != 0 {
		t.Fatalf("empty response should have no secret name or keys; got %+v", resp)
	}
}

func TestTunnelCreds_GetConfigured(t *testing.T) {
	gs := newGameServer("gameplane-games", "test-server")
	k := fakeKubeClient(gs)
	router := newTunnelCredsRouter(k)

	// First PUT to configure
	body := putReq{
		Provider: "frp",
		Values:   map[string]string{"token": "my-token"},
	}
	doTunnelReq(t, router, "PUT", "/servers/test-server:tunnel-credentials", body)

	// The fake typed client doesn't convert StringData to Data automatically.
	// Manually sync them to match what production Kubernetes would do.
	syncSecretData(t, k, "gameplane-games", "test-server-tunnel-auth")

	// Now GET should report configured
	status, respBody := doTunnelReq(t, router, "GET", "/servers/test-server:tunnel-credentials", nil)
	if status != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", status)
	}

	var resp getResp
	if err := json.Unmarshal(respBody, &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !resp.Configured {
		t.Fatal("should report configured")
	}
	if resp.SecretName != "test-server-tunnel-auth" {
		t.Fatalf("secret name = %q, want test-server-tunnel-auth", resp.SecretName)
	}
	if len(resp.Keys) != 1 || resp.Keys[0] != "token" {
		t.Fatalf("keys = %v, want [token]", resp.Keys)
	}
}

func TestTunnelCreds_GetNeverLeaksValue(t *testing.T) {
	gs := newGameServer("gameplane-games", "test-server")
	k := fakeKubeClient(gs)
	router := newTunnelCredsRouter(k)

	// PUT a credential
	body := putReq{
		Provider: "frp",
		Values:   map[string]string{"token": "super-secret-token-12345"},
	}
	doTunnelReq(t, router, "PUT", "/servers/test-server:tunnel-credentials", body)

	// The fake typed client doesn't convert StringData to Data automatically.
	// Manually sync them to match what production Kubernetes would do.
	syncSecretData(t, k, "gameplane-games", "test-server-tunnel-auth")

	// GET and verify response never contains the value
	_, respBody := doTunnelReq(t, router, "GET", "/servers/test-server:tunnel-credentials", nil)
	if bytes.Contains(respBody, []byte("super-secret-token-12345")) {
		t.Fatal("GET response contains the credential value")
	}
}

func TestTunnelCreds_Delete(t *testing.T) {
	gs := newGameServer("gameplane-games", "test-server")
	k := fakeKubeClient(gs)
	router := newTunnelCredsRouter(k)

	// PUT to configure
	body := putReq{
		Provider: "frp",
		Values:   map[string]string{"token": "my-token"},
	}
	doTunnelReq(t, router, "PUT", "/servers/test-server:tunnel-credentials", body)

	// Verify configured
	secret, err := k.Typed.CoreV1().Secrets("gameplane-games").Get(context.Background(), "test-server-tunnel-auth", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret before delete: %v", err)
	}
	if secret == nil {
		t.Fatal("secret should exist before delete")
	}

	// DELETE
	status, _ := doTunnelReq(t, router, "DELETE", "/servers/test-server:tunnel-credentials", nil)
	if status != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want 204", status)
	}

	// Verify Secret was deleted
	_, err = k.Typed.CoreV1().Secrets("gameplane-games").Get(context.Background(), "test-server-tunnel-auth", metav1.GetOptions{})
	if err == nil {
		t.Fatal("secret should be deleted after DELETE")
	}

	// Verify GameServer credentialsSecretRef was cleared
	updated, err := k.Dynamic.Resource(kube.GVRs["servers"]).
		Namespace("gameplane-games").
		Get(context.Background(), "test-server", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get updated server: %v", err)
	}
	secretRef, _, _ := getNestedString(updated.Object, "spec", "networking", "tunnel", "credentialsSecretRef", "name")
	if secretRef != "" {
		t.Fatalf("credentialsSecretRef should be cleared, got %q", secretRef)
	}
}

func TestTunnelCreds_DeleteNotConfigured(t *testing.T) {
	gs := newGameServer("gameplane-games", "test-server")
	k := fakeKubeClient(gs)
	router := newTunnelCredsRouter(k)

	// DELETE on an unconfigured server should not error
	status, _ := doTunnelReq(t, router, "DELETE", "/servers/test-server:tunnel-credentials", nil)
	if status != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want 204", status)
	}
}

// TestTunnelCreds_DeleteRefusedWhenTunnelEnabled verifies that DELETE is
// rejected if the tunnel is currently enabled, and that the Secret still exists.
func TestTunnelCreds_DeleteRefusedWhenTunnelEnabled(t *testing.T) {
	// Create a GameServer with tunnel enabled and credentials configured.
	gs := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "gameplane.local/v1alpha1",
			"kind":       "GameServer",
			"metadata": map[string]any{
				"name":      "test-server",
				"namespace": "gameplane-games",
				"uid":       "test-uid-12345",
			},
			"spec": map[string]any{
				"template": "minecraft-java",
				"networking": map[string]any{
					"expose": "ClusterIP",
					"tunnel": map[string]any{
						"enabled":  true,
						"provider": "frp",
						"credentialsSecretRef": map[string]any{
							"name": "test-server-tunnel-auth",
						},
					},
				},
			},
		},
	}
	k := fakeKubeClient(gs)
	// Manually create the Secret since the fake client won't auto-create it.
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-server-tunnel-auth",
			Namespace: "gameplane-games",
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "gameplane.local/v1alpha1",
					Kind:       "GameServer",
					Name:       "test-server",
					UID:        "test-uid-12345",
				},
			},
		},
		StringData: map[string]string{"token": "my-token"},
	}
	k.Typed.CoreV1().Secrets("gameplane-games").Create(context.Background(), secret, metav1.CreateOptions{})

	router := newTunnelCredsRouter(k)

	// Attempt DELETE
	status, respBody := doTunnelReq(t, router, "DELETE", "/servers/test-server:tunnel-credentials", nil)

	// Should be rejected with Conflict (409).
	if status != http.StatusConflict {
		t.Fatalf("DELETE with tunnel enabled should return 409, got %d", status)
	}

	// Error message should mention disabling the tunnel.
	if !bytes.Contains(respBody, []byte("disable the tunnel")) {
		t.Fatalf("error message should mention disabling the tunnel, got: %s", respBody)
	}

	// Verify the Secret still exists.
	_, err := k.Typed.CoreV1().Secrets("gameplane-games").Get(context.Background(), "test-server-tunnel-auth", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Secret should still exist after rejected DELETE, but got error: %v", err)
	}

	// Verify the credentialsSecretRef is still set on the GameServer.
	updated, _ := k.Dynamic.Resource(kube.GVRs["servers"]).
		Namespace("gameplane-games").
		Get(context.Background(), "test-server", metav1.GetOptions{})
	secretRef, _, _ := getNestedString(updated.Object, "spec", "networking", "tunnel", "credentialsSecretRef", "name")
	if secretRef != "test-server-tunnel-auth" {
		t.Fatalf("credentialsSecretRef should remain set after rejected DELETE")
	}
}

// TestTunnelCreds_DeleteSucceedsWhenTunnelDisabled verifies that DELETE works
// when the tunnel is disabled: the Secret is deleted and the credentialsSecretRef
// is cleared from the GameServer.
func TestTunnelCreds_DeleteSucceedsWhenTunnelDisabled(t *testing.T) {
	// Create a GameServer with tunnel disabled but credentials configured.
	gs := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "gameplane.local/v1alpha1",
			"kind":       "GameServer",
			"metadata": map[string]any{
				"name":      "test-server",
				"namespace": "gameplane-games",
				"uid":       "test-uid-12345",
			},
			"spec": map[string]any{
				"template": "minecraft-java",
				"networking": map[string]any{
					"expose": "ClusterIP",
					"tunnel": map[string]any{
						"enabled":  false,
						"provider": "frp",
						"credentialsSecretRef": map[string]any{
							"name": "test-server-tunnel-auth",
						},
					},
				},
			},
		},
	}
	k := fakeKubeClient(gs)
	// Manually create the Secret.
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-server-tunnel-auth",
			Namespace: "gameplane-games",
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "gameplane.local/v1alpha1",
					Kind:       "GameServer",
					Name:       "test-server",
					UID:        "test-uid-12345",
				},
			},
		},
		StringData: map[string]string{"token": "my-token"},
	}
	k.Typed.CoreV1().Secrets("gameplane-games").Create(context.Background(), secret, metav1.CreateOptions{})

	router := newTunnelCredsRouter(k)

	// DELETE should succeed (204).
	status, _ := doTunnelReq(t, router, "DELETE", "/servers/test-server:tunnel-credentials", nil)
	if status != http.StatusNoContent {
		t.Fatalf("DELETE with tunnel disabled should succeed, got %d", status)
	}

	// Verify the Secret was deleted.
	_, err := k.Typed.CoreV1().Secrets("gameplane-games").Get(context.Background(), "test-server-tunnel-auth", metav1.GetOptions{})
	if err == nil {
		t.Fatalf("Secret should be deleted after successful DELETE")
	}

	// Verify the credentialsSecretRef was cleared from the GameServer.
	updated, _ := k.Dynamic.Resource(kube.GVRs["servers"]).
		Namespace("gameplane-games").
		Get(context.Background(), "test-server", metav1.GetOptions{})
	secretRef, _, _ := getNestedString(updated.Object, "spec", "networking", "tunnel", "credentialsSecretRef", "name")
	if secretRef != "" {
		t.Fatalf("credentialsSecretRef should be cleared after successful DELETE, got %q", secretRef)
	}
}

func TestTunnelCreds_BadJSON(t *testing.T) {
	gs := newGameServer("gameplane-games", "test-server")
	k := fakeKubeClient(gs)
	router := newTunnelCredsRouter(k)

	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/servers/test-server:tunnel-credentials", bytes.NewReader([]byte("{not json")))
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code == http.StatusNoContent {
		t.Fatalf("bad JSON should be rejected, got %d", rr.Code)
	}
}

func TestTunnelCreds_GetMissingSecret(t *testing.T) {
	// Create a GameServer with credentialsSecretRef set but Secret deleted
	gs := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "gameplane.local/v1alpha1",
			"kind":       "GameServer",
			"metadata": map[string]any{
				"name":      "test-server",
				"namespace": "gameplane-games",
				"uid":       "test-uid-12345",
			},
			"spec": map[string]any{
				"template": "minecraft-java",
				"networking": map[string]any{
					"expose": "ClusterIP",
					"tunnel": map[string]any{
						"credentialsSecretRef": map[string]any{
							"name": "missing-secret",
						},
					},
				},
			},
		},
	}
	k := fakeKubeClient(gs)
	router := newTunnelCredsRouter(k)

	status, respBody := doTunnelReq(t, router, "GET", "/servers/test-server:tunnel-credentials", nil)
	if status != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", status)
	}

	var resp getResp
	json.Unmarshal(respBody, &resp)
	// Should report as not configured since the Secret doesn't exist
	if resp.Configured {
		t.Fatal("should report not configured when Secret is missing")
	}
	if resp.SecretName != "missing-secret" {
		t.Fatalf("should still return the referenced secret name; got %q", resp.SecretName)
	}
}

func TestTunnelCreds_Put_ExistingUnownedSecretConflict(t *testing.T) {
	gs := newGameServer("gameplane-games", "test-server")
	k := fakeKubeClient(gs)
	router := newTunnelCredsRouter(k)

	// Create an unowned secret with the canonical name
	unownedSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-server-tunnel-auth",
			Namespace: "gameplane-games",
			// No OwnerReferences
		},
		Data: map[string][]byte{"token": []byte("initial")},
	}
	if _, err := k.Typed.CoreV1().Secrets("gameplane-games").Create(context.Background(), unownedSecret, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create unowned secret: %v", err)
	}

	body := putReq{
		Provider: "frp",
		Values:   map[string]string{"token": "new-token"},
	}
	status, _ := doTunnelReq(t, router, "PUT", "/servers/test-server:tunnel-credentials", body)
	if status != http.StatusConflict {
		t.Fatalf("PUT on unowned secret status = %d, want 409 Conflict", status)
	}
}

func TestTunnelCreds_Delete_UnownedSecretForbidden(t *testing.T) {
	gs := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "gameplane.local/v1alpha1",
			"kind":       "GameServer",
			"metadata": map[string]any{
				"name":      "test-server",
				"namespace": "gameplane-games",
				"uid":       "test-uid-12345",
			},
			"spec": map[string]any{
				"template": "minecraft-java",
				"networking": map[string]any{
					"expose": "ClusterIP",
					"tunnel": map[string]any{
						"enabled": false,
						"credentialsSecretRef": map[string]any{
							"name": "foreign-secret",
						},
					},
				},
			},
		},
	}
	k := fakeKubeClient(gs)
	router := newTunnelCredsRouter(k)

	// Create a secret NOT owned by test-server (e.g. backup or another server)
	foreignSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "foreign-secret",
			Namespace: "gameplane-games",
			// No OwnerReferences to test-server
		},
		Data: map[string][]byte{"password": []byte("secret-pass")},
	}
	if _, err := k.Typed.CoreV1().Secrets("gameplane-games").Create(context.Background(), foreignSecret, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create foreign secret: %v", err)
	}

	status, _ := doTunnelReq(t, router, "DELETE", "/servers/test-server:tunnel-credentials", nil)
	if status != http.StatusForbidden {
		t.Fatalf("DELETE referencing unowned secret status = %d, want 403 Forbidden", status)
	}

	// Verify foreign secret was NOT deleted
	sec, err := k.Typed.CoreV1().Secrets("gameplane-games").Get(context.Background(), "foreign-secret", metav1.GetOptions{})
	if err != nil || sec == nil {
		t.Fatal("foreign secret was deleted! Must be preserved.")
	}
}

// TestTunnelCreds_ProviderSwitchLeavesOnlyNewProviderKey checks that after
// the provider changes the credential Secret holds exactly the new
// provider's key, keeps keys that no provider uses, and reads back the
// same keys every time.
func TestTunnelCreds_ProviderSwitchLeavesOnlyNewProviderKey(t *testing.T) {
	const ns, name, secretName = "gameplane-games", "test-server", "test-server-tunnel-auth"
	gs := newGameServer(ns, name)
	k := fakeKubeClient(gs)
	router := newTunnelCredsRouter(k)
	path := "/servers/" + name + ":tunnel-credentials"

	status, respBody := doTunnelReq(t, router, "PUT", path, putReq{
		Provider: "frp",
		Values:   map[string]string{"token": "frp-token"},
	})
	if status != http.StatusNoContent {
		t.Fatalf("frp PUT status = %d, want 204; body=%s", status, respBody)
	}
	syncSecretData(t, k, ns, secretName)

	// A key set outside the API must survive the switch.
	secret, err := k.Typed.CoreV1().Secrets(ns).Get(context.Background(), secretName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	secret.Data["admin-note"] = []byte("keep-me")
	if _, err := k.Typed.CoreV1().Secrets(ns).Update(context.Background(), secret, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("update secret: %v", err)
	}

	status, respBody = doTunnelReq(t, router, "PUT", path, putReq{
		Provider: "tailscale",
		Values:   map[string]string{"authKey": "ts-key"},
	})
	if status != http.StatusNoContent {
		t.Fatalf("tailscale PUT status = %d, want 204; body=%s", status, respBody)
	}
	syncSecretData(t, k, ns, secretName)

	secret, err = k.Typed.CoreV1().Secrets(ns).Get(context.Background(), secretName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret after switch: %v", err)
	}
	if _, ok := secret.Data["token"]; ok {
		t.Fatalf("previous provider key still in data after the switch (%d data keys)", len(secret.Data))
	}
	if _, ok := secret.StringData["token"]; ok {
		t.Fatal("previous provider key still in stringData after the switch")
	}
	if got := string(secret.Data["authKey"]); got != "ts-key" {
		t.Fatalf("authKey = %q, want ts-key", got)
	}
	if got := string(secret.Data["admin-note"]); got != "keep-me" {
		t.Fatalf("admin-note = %q, want keep-me (non-provider keys must be kept)", got)
	}

	for i := range 20 {
		status, respBody := doTunnelReq(t, router, "GET", path, nil)
		if status != http.StatusOK {
			t.Fatalf("GET #%d status = %d, want 200; body=%s", i, status, respBody)
		}
		var resp getResp
		if err := json.Unmarshal(respBody, &resp); err != nil {
			t.Fatalf("GET #%d unmarshal: %v", i, err)
		}
		if !resp.Configured || len(resp.Keys) != 1 || resp.Keys[0] != "authKey" {
			t.Fatalf("GET #%d = %+v, want configured with keys [authKey]", i, resp)
		}
	}
}

// TestTunnelCreds_Put_KeepsActiveProviderKeyDuringSwitch checks that saving
// a new provider's credentials does not delete the key of the provider the
// GameServer spec still names as active. The dashboard saves credentials and
// switches spec.networking.tunnel.provider in separate requests, so a pod
// still running the active provider must keep being able to read its
// credential until the spec provider field actually changes.
func TestTunnelCreds_Put_KeepsActiveProviderKeyDuringSwitch(t *testing.T) {
	const ns, name, secretName = "gameplane-games", "test-server", "test-server-tunnel-auth"
	gs := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "gameplane.local/v1alpha1",
			"kind":       "GameServer",
			"metadata": map[string]any{
				"name":      name,
				"namespace": ns,
				"uid":       "test-uid-12345",
			},
			"spec": map[string]any{
				"template": "minecraft-java",
				"networking": map[string]any{
					"expose": "ClusterIP",
					"tunnel": map[string]any{
						"enabled":  true,
						"provider": "frp",
					},
				},
			},
		},
	}
	k := fakeKubeClient(gs)
	router := newTunnelCredsRouter(k)
	path := "/servers/" + name + ":tunnel-credentials"

	// The frp tunnel is already running with a saved token.
	status, respBody := doTunnelReq(t, router, "PUT", path, putReq{
		Provider: "frp",
		Values:   map[string]string{"token": "frp-token"},
	})
	if status != http.StatusNoContent {
		t.Fatalf("frp PUT status = %d, want 204; body=%s", status, respBody)
	}
	syncSecretData(t, k, ns, secretName)

	// The dashboard saves tailscale credentials before the GameServer spec's
	// provider field is switched away from frp: the fake object above still
	// names "frp" as spec.networking.tunnel.provider.
	status, respBody = doTunnelReq(t, router, "PUT", path, putReq{
		Provider: "tailscale",
		Values:   map[string]string{"authKey": "ts-key"},
	})
	if status != http.StatusNoContent {
		t.Fatalf("tailscale PUT status = %d, want 204; body=%s", status, respBody)
	}
	syncSecretData(t, k, ns, secretName)

	secret, err := k.Typed.CoreV1().Secrets(ns).Get(context.Background(), secretName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	if got := string(secret.Data["token"]); got != "frp-token" {
		t.Fatalf("token = %q, want frp-token (active provider key must survive an in-flight switch)", got)
	}
	if got := string(secret.Data["authKey"]); got != "ts-key" {
		t.Fatalf("authKey = %q, want ts-key", got)
	}

	// Once the spec provider field actually switches to tailscale, saving
	// credentials again must clean up the now-inactive frp key.
	gs.Object["spec"].(map[string]any)["networking"].(map[string]any)["tunnel"].(map[string]any)["provider"] = "tailscale"
	if _, err := k.Dynamic.Resource(kube.GVRs["servers"]).Namespace(ns).Update(
		context.Background(), gs, metav1.UpdateOptions{},
	); err != nil {
		t.Fatalf("update gameserver spec provider: %v", err)
	}

	status, respBody = doTunnelReq(t, router, "PUT", path, putReq{
		Provider: "tailscale",
		Values:   map[string]string{"authKey": "ts-key-2"},
	})
	if status != http.StatusNoContent {
		t.Fatalf("tailscale re-save PUT status = %d, want 204; body=%s", status, respBody)
	}
	syncSecretData(t, k, ns, secretName)

	secret, err = k.Typed.CoreV1().Secrets(ns).Get(context.Background(), secretName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret after spec switch: %v", err)
	}
	if _, ok := secret.Data["token"]; ok {
		t.Fatal("frp key still present after the spec provider switched away from frp")
	}
	if got := string(secret.Data["authKey"]); got != "ts-key-2" {
		t.Fatalf("authKey = %q, want ts-key-2", got)
	}
}

// TestTunnelCreds_GetReportsKeysDeterministically checks that GET reports the
// GameServer's own provider when a Secret holds keys for several providers,
// and falls back to a fixed provider order when the spec names none.
func TestTunnelCreds_GetReportsKeysDeterministically(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		want     string
	}{
		{"spec provider wins", "tailscale", "authKey"},
		{"fixed order without a spec provider", "", "token"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tunnel := map[string]any{
				"enabled": false,
				"credentialsSecretRef": map[string]any{
					"name": "test-server-tunnel-auth",
				},
			}
			if tc.provider != "" {
				tunnel["provider"] = tc.provider
			}
			gs := &unstructured.Unstructured{
				Object: map[string]any{
					"apiVersion": "gameplane.local/v1alpha1",
					"kind":       "GameServer",
					"metadata": map[string]any{
						"name":      "test-server",
						"namespace": "gameplane-games",
						"uid":       "test-uid-12345",
					},
					"spec": map[string]any{
						"template": "minecraft-java",
						"networking": map[string]any{
							"expose": "ClusterIP",
							"tunnel": tunnel,
						},
					},
				},
			}
			k := fakeKubeClient(gs)
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-server-tunnel-auth",
					Namespace: "gameplane-games",
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "gameplane.local/v1alpha1",
							Kind:       "GameServer",
							Name:       "test-server",
							UID:        "test-uid-12345",
						},
					},
				},
				Data: map[string][]byte{
					"token":     []byte("a"),
					"authKey":   []byte("b"),
					"secretKey": []byte("c"),
				},
			}
			if _, err := k.Typed.CoreV1().Secrets("gameplane-games").Create(context.Background(), secret, metav1.CreateOptions{}); err != nil {
				t.Fatalf("create secret: %v", err)
			}
			router := newTunnelCredsRouter(k)

			for i := range 20 {
				status, respBody := doTunnelReq(t, router, "GET", "/servers/test-server:tunnel-credentials", nil)
				if status != http.StatusOK {
					t.Fatalf("GET #%d status = %d, want 200; body=%s", i, status, respBody)
				}
				var resp getResp
				if err := json.Unmarshal(respBody, &resp); err != nil {
					t.Fatalf("GET #%d unmarshal: %v", i, err)
				}
				if len(resp.Keys) != 1 || resp.Keys[0] != tc.want {
					t.Fatalf("GET #%d keys = %v, want [%s]", i, resp.Keys, tc.want)
				}
			}
		})
	}
}

// TestTunnelProviderOrder_CoversEveryProvider checks that reads match every
// provider that has credential keys.
func TestTunnelProviderOrder_CoversEveryProvider(t *testing.T) {
	if len(tunnelProviderOrder) != len(tunnelProviderKeys) {
		t.Fatalf("tunnelProviderOrder has %d providers, tunnelProviderKeys has %d", len(tunnelProviderOrder), len(tunnelProviderKeys))
	}
	for _, p := range tunnelProviderOrder {
		if _, ok := tunnelProviderKeys[p]; !ok {
			t.Fatalf("tunnelProviderOrder lists %q, which has no credential keys", p)
		}
	}
}
