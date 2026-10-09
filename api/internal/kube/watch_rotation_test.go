package kube

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
)

type boundedPublicationStore struct {
	ClusterStore
	t     *testing.T
	reads int
}

func (s *boundedPublicationStore) Get(ctx context.Context, name string, opts metav1.GetOptions, subresources ...string) (*unstructured.Unstructured, error) {
	s.reads++
	if s.reads == 2 {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 5*time.Second {
			s.t.Fatal("registry publication holds its lock without a bounded management read")
		}
		return nil, context.DeadlineExceeded
	}
	return s.ClusterStore.Get(ctx, name, opts, subresources...)
}

func TestClusterRefreshBoundsReadWhileHoldingRegistryLock(t *testing.T) {
	registration := newTestCluster("remote")
	registration.SetUID("original")
	dynamic := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{GVRCluster: "ClusterList"}, registration)
	typed := kubefake.NewClientset(&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "cluster-remote-kubeconfig", Namespace: "panel", Labels: map[string]string{ClusterKubeconfigLabel: "true"}}, Data: map[string][]byte{"kubeconfig": kubeconfig()}})
	home := &Client{Dynamic: dynamic, Typed: typed}
	home.ClusterStore = &boundedPublicationStore{ClusterStore: home.Clusters(), t: t}
	reg := NewRegistry("local")
	if err := RefreshRegisteredCluster(t.Context(), home, reg, "panel", "remote", registration.GetUID()); err == nil {
		t.Fatal("publication ignored a timed-out management read")
	}
	if _, ok := reg.Get("remote"); ok {
		t.Fatal("failed publication retained a client")
	}
}

type rotationSecretStore struct {
	SecretStore
	afterRead func()
}

func (s rotationSecretStore) Get(ctx context.Context, name string, opts metav1.GetOptions) (*corev1.Secret, error) {
	secret, err := s.SecretStore.Get(ctx, name, opts)
	if err == nil {
		s.afterRead()
	}
	return secret, err
}

func TestClusterRefreshDoesNotPublishCredentialAcrossRotationOrDeletion(t *testing.T) {
	for _, action := range []string{"rotate", "delete", "recreate"} {
		t.Run(action, func(t *testing.T) {
			registration := newTestCluster("remote")
			registration.SetUID("original")
			registration.SetResourceVersion("1")
			dynamic := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{GVRCluster: "ClusterList"}, registration)
			typed := kubefake.NewClientset(&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "cluster-remote-kubeconfig", Namespace: "panel", Labels: map[string]string{ClusterKubeconfigLabel: "true"}}, Data: map[string][]byte{"kubeconfig": kubeconfig()}})
			home := &Client{Dynamic: dynamic, Typed: typed}
			reg := NewRegistry("local")
			home.SecretStore = func(ns string) SecretStore {
				return rotationSecretStore{SecretStore: typed.CoreV1().Secrets(ns), afterRead: func() {
					switch action {
					case "rotate":
						current := registration.DeepCopy()
						current.SetResourceVersion("2")
						if _, err := home.Clusters().Update(t.Context(), current, metav1.UpdateOptions{}); err != nil {
							t.Fatal(err)
						}
					default:
						if err := home.Clusters().Delete(t.Context(), "remote", metav1.DeleteOptions{}); err != nil {
							t.Fatal(err)
						}
						if action == "recreate" {
							current := registration.DeepCopy()
							current.SetUID("replacement")
							if _, err := home.Clusters().Create(t.Context(), current, metav1.CreateOptions{}); err != nil {
								t.Fatal(err)
							}
						}
					}
				}}
			}
			err := RefreshRegisteredCluster(t.Context(), home, reg, "panel", "remote", registration.GetUID())
			if !apierrors.IsConflict(err) && !apierrors.IsNotFound(err) {
				t.Fatalf("stale publication must be rejected: %v", err)
			}
			if _, ok := reg.Get("remote"); ok {
				t.Fatal("stale refresh published an old client")
			}
		})
	}
}
