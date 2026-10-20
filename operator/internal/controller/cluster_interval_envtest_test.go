//go:build envtest

package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func TestCluster_SlowProbeDoesNotQueueItself(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("failure=%t", fail), func(t *testing.T) {
			var probes atomic.Int32
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				probes.Add(1)
				// Cross the timestamp's second boundary, including on failure.
				// Otherwise the apiserver could suppress a no-op status write.
				timer := time.NewTimer(1100 * time.Millisecond)
				defer timer.Stop()
				select {
				case <-req.Context().Done():
					return
				case <-timer.C:
				}
				if fail {
					http.Error(w, "unavailable", http.StatusUnauthorized)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"gitVersion":"v1.35.0"}`))
			}))
			defer endpoint.Close()
			ns := newNamespace(t)
			startMgr(t, ns, withClusterReconciler(ns))
			secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "kubeconfig", Namespace: ns, Labels: map[string]string{gameplanev1alpha1.LabelClusterKubeconfig: "true"}}, Data: map[string][]byte{"kubeconfig": []byte(fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: remote
  cluster:
    server: %s
contexts:
- name: remote
  context:
    cluster: remote
    user: remote
current-context: remote
users:
- name: remote
  user:
    token: test-token
`, endpoint.URL))}}
			if err := k8sClient.Create(t.Context(), secret); err != nil {
				t.Fatal(err)
			}
			cluster := &gameplanev1alpha1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: uniqueName("probe")}, Spec: gameplanev1alpha1.ClusterSpec{KubeconfigSecret: gameplanev1alpha1.KubeconfigSecretRef{Name: secret.Name}}}
			if err := k8sClient.Create(t.Context(), cluster); err != nil {
				t.Fatal(err)
			}
			deleteCleanup(t, cluster)
			key := client.ObjectKeyFromObject(cluster)
			eventually(t, func() (bool, string) {
				if err := k8sClient.Get(t.Context(), key, cluster); err != nil {
					return false, err.Error()
				}
				return cluster.Status.LastCheckTime != nil, "waiting for first probe"
			})
			consistently(t, 2500*time.Millisecond, func() (bool, string) {
				return probes.Load() == 1, fmt.Sprintf("probes=%d, want 1", probes.Load())
			})
			// Spec changes must still bypass the periodic wait.
			if err := k8sClient.Get(t.Context(), key, cluster); err != nil {
				t.Fatal(err)
			}
			cluster.Spec.DisplayName = "updated"
			if err := k8sClient.Update(t.Context(), cluster); err != nil {
				t.Fatal(err)
			}
			eventually(t, func() (bool, string) { return probes.Load() == 2, "waiting for changed-spec probe" })
		})
	}
}
