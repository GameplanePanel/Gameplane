package kube

import (
	"context"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
)

func standaloneWatchFixture(t *testing.T, handler http.Handler) (*Client, *Registry, *unstructured.Unstructured) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	ca := base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}))
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf(`{"apiVersion":"v1","kind":"Config","clusters":[{"name":"remote","cluster":{"server":%q,"tls-server-name":"127.0.0.1","certificate-authority-data":%q}}],"contexts":[{"name":"remote","context":{"cluster":"remote","user":"panel"}}],"current-context":"remote","users":[{"name":"panel","user":{"token":"private-token"}}]}`, "https://"+net.JoinHostPort("10.0.0.1", port), ca)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-remote-kubeconfig", Namespace: "panel", Labels: map[string]string{ClusterKubeconfigLabel: "true"}},
		Data:       map[string][]byte{"kubeconfig": []byte(config)},
	}
	typed := kubefake.NewClientset(secret)
	registration := newTestCluster("remote")
	registration.SetUID("remote-identity")
	registration.SetResourceVersion("1")
	dynamic := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{GVRCluster: "ClusterList"}, registration)
	home := &Client{
		SecretStore:  func(ns string) SecretStore { return typed.CoreV1().Secrets(ns) },
		ClusterStore: dynamic.Resource(GVRCluster),
		RemoteAccess: &RemoteAccessPolicy{dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			if address != net.JoinHostPort("10.0.0.1", port) {
				return nil, fmt.Errorf("unexpected test destination")
			}
			return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
		}},
	}
	reg := NewRegistry("")
	reg.SetManagement(home)
	return home, reg, registration
}

func TestStandaloneProbeDoesNotResurrectDeletedRegistration(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	home, reg, registration := standaloneWatchFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		_, _ = w.Write([]byte(`{"gitVersion":"v1.35.0"}`))
	}))
	done := make(chan struct{})
	go func() { defer close(done); probeStandaloneCluster(t.Context(), home, reg, "panel", registration) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("probe did not start")
	}
	if err := home.Clusters().Delete(t.Context(), "remote", metav1.DeleteOptions{}); err != nil {
		close(release)
		t.Fatal(err)
	}
	reg.Remove("remote")
	close(release)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("probe did not finish")
	}
	if _, ok := reg.Get("remote"); ok {
		t.Fatal("stale probe resurrected deleted registration")
	}
}

func TestStandalonePollReusesClientsAndRotatesChangedCredentials(t *testing.T) {
	home, reg, registration := standaloneWatchFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"gitVersion":"v1.35.0"}`))
	}))
	probeStandaloneCluster(t.Context(), home, reg, "panel", registration)
	first, ok := reg.Get("remote")
	if !ok {
		t.Fatal("first probe did not load client")
	}
	current, err := home.Clusters().Get(t.Context(), "remote", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	probeStandaloneCluster(t.Context(), home, reg, "panel", current)
	second, _ := reg.Get("remote")
	if first != second {
		t.Fatal("unchanged credentials replaced the workload client")
	}
	secret, err := home.Secrets("panel").Get(t.Context(), "cluster-remote-kubeconfig", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	secret.Data["kubeconfig"] = []byte(strings.ReplaceAll(string(secret.Data["kubeconfig"]), "private-token", "rotated-token"))
	if _, err := home.Secrets("panel").Update(t.Context(), secret, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	current, err = home.Clusters().Get(t.Context(), "remote", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	probeStandaloneCluster(t.Context(), home, reg, "panel", current)
	third, _ := reg.Get("remote")
	if third == nil || third == second {
		t.Fatal("changed credentials did not replace workload client")
	}
}

func TestStandaloneProbePersistsSafeFailureMessage(t *testing.T) {
	home, reg, registration := standaloneWatchFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "private-token https://sensitive.example.invalid", http.StatusForbidden)
	}))
	probeStandaloneCluster(t.Context(), home, reg, "panel", registration)
	current, err := home.Clusters().Get(t.Context(), "remote", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	phase, _, _ := unstructured.NestedString(current.Object, "status", "phase")
	message, _, _ := unstructured.NestedString(current.Object, "status", "message")
	if phase != "Unhealthy" || message != "Cluster connection is unavailable" {
		t.Fatalf("unsafe or missing failure status: %s %s", phase, message)
	}
}

func TestStandaloneProbeRejectsOversizeVersion(t *testing.T) {
	home, reg, registration := standaloneWatchFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"gitVersion":"v1.35.0","padding":"` + strings.Repeat("x", 128*1024) + `"}`))
	}))
	probeStandaloneCluster(t.Context(), home, reg, "panel", registration)
	current, err := home.Clusters().Get(t.Context(), "remote", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	phase, _, _ := unstructured.NestedString(current.Object, "status", "phase")
	version, _, _ := unstructured.NestedString(current.Object, "status", "serverVersion")
	if phase != "Unhealthy" || version != "" {
		t.Fatal("oversize remote version was accepted")
	}
}
