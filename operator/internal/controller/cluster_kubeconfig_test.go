package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// Bypassing the shared parser would probe the selected cluster using a config
// with unsafe credentials, especially when those credentials are not selected.
func TestClusterRejectsProcessLocalKubeconfigBeforeDiscovery(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"gitVersion":"v1.37.1"}`))
	}))
	defer server.Close()

	for _, user := range []string{"selected", "other"} {
		for _, field := range []string{"certificate-authority", "client-certificate", "client-key", "tokenFile", "exec", "auth-provider"} {
			t.Run(user+"/"+field, func(t *testing.T) {
				cfg := clientcmdapi.Config{
					Clusters: map[string]*clientcmdapi.Cluster{
						"selected": {Server: server.URL}, "other": {Server: server.URL},
					},
					AuthInfos: map[string]*clientcmdapi.AuthInfo{
						"selected": {Token: "embedded-token"}, "other": {Token: "other-token"},
					},
					Contexts: map[string]*clientcmdapi.Context{
						"selected": {Cluster: "selected", AuthInfo: "selected"}, "other": {Cluster: "other", AuthInfo: "other"},
					},
					CurrentContext: "selected",
				}
				auth := cfg.AuthInfos[user]
				switch field {
				case "certificate-authority":
					cfg.Clusters[user].CertificateAuthority = "/private/ca.pem"
				case "client-certificate":
					auth.ClientCertificate = "/private/cert.pem"
				case "client-key":
					auth.ClientKey = "/private/key.pem"
				case "tokenFile":
					auth.TokenFile = "/private/token"
				case "exec":
					auth.Exec = &clientcmdapi.ExecConfig{Command: "private-command", APIVersion: "client.authentication.k8s.io/v1", InteractiveMode: clientcmdapi.NeverExecInteractiveMode}
				case "auth-provider":
					auth.AuthProvider = &clientcmdapi.AuthProviderConfig{Name: "private-provider"}
				}
				data, err := clientcmd.Write(cfg)
				if err != nil {
					t.Fatal(err)
				}
				scheme := runtime.NewScheme()
				if err := corev1.AddToScheme(scheme); err != nil {
					t.Fatal(err)
				}
				if err := gameplanev1alpha1.AddToScheme(scheme); err != nil {
					t.Fatal(err)
				}
				secret := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: "credentials", Namespace: "control", Labels: map[string]string{gameplanev1alpha1.LabelClusterKubeconfig: "true"}},
					Data:       map[string][]byte{"custom-key": data},
				}
				cluster := &gameplanev1alpha1.Cluster{
					ObjectMeta: metav1.ObjectMeta{Name: "remote", Generation: 3},
					Spec:       gameplanev1alpha1.ClusterSpec{KubeconfigSecret: gameplanev1alpha1.KubeconfigSecretRef{Name: secret.Name, Key: "custom-key"}},
				}
				cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret, cluster).WithStatusSubresource(cluster).Build()
				r := &ClusterStatusReconciler{Client: cl, Scheme: scheme, Namespace: "control"}
				name := types.NamespacedName{Name: cluster.Name}
				result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: name})
				if err != nil {
					t.Fatal(err)
				}
				if result.RequeueAfter != 2*time.Minute {
					t.Fatalf("unexpected retry interval: %v", result.RequeueAfter)
				}
				var updated gameplanev1alpha1.Cluster
				if err := cl.Get(context.Background(), name, &updated); err != nil {
					t.Fatal(err)
				}
				if updated.Status.Phase != gameplanev1alpha1.ClusterPhaseUnhealthy || !strings.Contains(updated.Status.Message, field) {
					t.Fatalf("expected unhealthy credential policy rejection, got %+v", updated.Status)
				}
				if len(updated.Status.Conditions) != 1 || updated.Status.Conditions[0].Reason != "BadKubeconfig" {
					t.Fatalf("expected BadKubeconfig condition, got %+v", updated.Status.Conditions)
				}
				if requests.Load() != 0 {
					t.Fatal("unsafe kubeconfig reached discovery")
				}
			})
		}
	}
}
