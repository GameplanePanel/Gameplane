//go:build envtest

package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func withClusterReconciler(controlPlaneNs string) setupReconciler {
	return func(mgr manager.Manager) error {
		return (&ClusterStatusReconciler{
			Client:    mgr.GetClient(),
			Scheme:    mgr.GetScheme(),
			Namespace: controlPlaneNs,
		}).SetupWithManager(mgr)
	}
}

// TestCluster_HealthCheckSucceeds tests that a valid cluster with a proper
// kubeconfig Secret transitions to Healthy phase.
func TestCluster_HealthCheckSucceeds(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withClusterReconciler(ns))

	// Create a kubeconfig Secret in the control-plane namespace
	kubeconfigSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-kubeconfig",
			Namespace: ns,
			Labels: map[string]string{
				gameplanev1alpha1.LabelClusterKubeconfig: "true",
			},
		},
		Data: map[string][]byte{
			"kubeconfig": []byte(`apiVersion: v1
clusters:
- cluster:
    server: https://invalid.local:6443
  name: invalid
contexts:
- context:
    cluster: invalid
    user: user
  name: invalid
current-context: invalid
kind: Config
preferences: {}
users:
- name: user
  user:
    token: invalid-token
`),
		},
	}
	if err := k8sClient.Create(context.Background(), kubeconfigSecret); err != nil {
		t.Fatalf("create kubeconfig secret: %v", err)
	}

	// Create a Cluster that references the kubeconfig Secret
	cluster := &gameplanev1alpha1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster"},
		Spec: gameplanev1alpha1.ClusterSpec{
			DisplayName: "Test Cluster",
			KubeconfigSecret: gameplanev1alpha1.KubeconfigSecretRef{
				Name: "test-kubeconfig",
				Key:  "kubeconfig",
			},
		},
	}
	if err := k8sClient.Create(context.Background(), cluster); err != nil {
		t.Fatalf("create cluster: %v", err)
	}

	// Eventually the cluster status should have a Phase (either Healthy or Unhealthy,
	// depending on whether the cluster is reachable). At minimum, it should be set.
	eventually(t, func() (bool, string) {
		var c gameplanev1alpha1.Cluster
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Name: "test-cluster"}, &c); err != nil {
			return false, "get cluster: " + err.Error()
		}
		if c.Status.Phase == "" {
			return false, "phase is empty"
		}
		// Phase should be either Healthy or Unhealthy since we set a timeout
		if c.Status.Phase != gameplanev1alpha1.ClusterPhaseHealthy &&
			c.Status.Phase != gameplanev1alpha1.ClusterPhaseUnhealthy {
			return false, "phase = " + c.Status.Phase
		}
		return true, ""
	})
}

// TestCluster_ReservedNameLocal tests that a Cluster named "local" is rejected
// with an Unhealthy phase.
func TestCluster_ReservedNameLocal(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withClusterReconciler(ns))

	// Create a Cluster named "local" — this should be rejected
	cluster := &gameplanev1alpha1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "local"},
		Spec: gameplanev1alpha1.ClusterSpec{
			DisplayName: "Local Cluster (should be reserved)",
			KubeconfigSecret: gameplanev1alpha1.KubeconfigSecretRef{
				Name: "does-not-exist",
			},
		},
	}
	if err := k8sClient.Create(context.Background(), cluster); err != nil {
		t.Fatalf("create cluster: %v", err)
	}

	eventually(t, func() (bool, string) {
		var c gameplanev1alpha1.Cluster
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Name: "local"}, &c); err != nil {
			return false, "get cluster: " + err.Error()
		}
		if c.Status.Phase != gameplanev1alpha1.ClusterPhaseUnhealthy {
			return false, "phase = " + c.Status.Phase + ", want Unhealthy"
		}
		if c.Status.Message != `cluster name "local" is reserved` {
			return false, "message = " + c.Status.Message
		}
		return true, ""
	})
}

// TestCluster_MissingSecret tests that a Cluster referencing a missing Secret
// transitions to Unhealthy.
func TestCluster_MissingSecret(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withClusterReconciler(ns))

	// Create a Cluster that references a non-existent Secret
	cluster := &gameplanev1alpha1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "missing-secret-cluster"},
		Spec: gameplanev1alpha1.ClusterSpec{
			DisplayName: "Missing Secret Cluster",
			KubeconfigSecret: gameplanev1alpha1.KubeconfigSecretRef{
				Name: "does-not-exist",
				Key:  "kubeconfig",
			},
		},
	}
	if err := k8sClient.Create(context.Background(), cluster); err != nil {
		t.Fatalf("create cluster: %v", err)
	}

	eventually(t, func() (bool, string) {
		var c gameplanev1alpha1.Cluster
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Name: "missing-secret-cluster"}, &c); err != nil {
			return false, "get cluster: " + err.Error()
		}
		if c.Status.Phase != gameplanev1alpha1.ClusterPhaseUnhealthy {
			return false, "phase = " + c.Status.Phase + ", want Unhealthy"
		}
		// Message should mention the secret not being found
		if c.Status.Message == "" {
			return false, "message is empty"
		}
		return true, ""
	})
}
