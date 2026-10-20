package kube

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// SecretStore is the management credential operations shared by Kubernetes
// and the standalone encrypted database backend.
type SecretStore interface {
	Get(context.Context, string, metav1.GetOptions) (*corev1.Secret, error)
	List(context.Context, metav1.ListOptions) (*corev1.SecretList, error)
	Create(context.Context, *corev1.Secret, metav1.CreateOptions) (*corev1.Secret, error)
	Update(context.Context, *corev1.Secret, metav1.UpdateOptions) (*corev1.Secret, error)
	Delete(context.Context, string, metav1.DeleteOptions) error
}

// ClusterStore persists remote cluster registrations independently of workload
// clients. The signatures also accept the Kubernetes dynamic resource client.
type ClusterStore interface {
	Get(context.Context, string, metav1.GetOptions, ...string) (*unstructured.Unstructured, error)
	List(context.Context, metav1.ListOptions) (*unstructured.UnstructuredList, error)
	Create(context.Context, *unstructured.Unstructured, metav1.CreateOptions, ...string) (*unstructured.Unstructured, error)
	Update(context.Context, *unstructured.Unstructured, metav1.UpdateOptions, ...string) (*unstructured.Unstructured, error)
	Delete(context.Context, string, metav1.DeleteOptions, ...string) error
}

// ClusterRegistrar commits a registration and its credential together. Only
// standalone management implements this; Kubernetes uses its native stores.
type ClusterRegistrar func(context.Context, string, *corev1.Secret, *unstructured.Unstructured) (*unstructured.Unstructured, error)

// Secrets selects management credentials in a namespace.
func (c *Client) Secrets(ns string) SecretStore {
	if c == nil {
		return nil
	}
	if c.SecretStore != nil {
		return c.SecretStore(ns)
	}
	if c.Typed == nil {
		return nil
	}
	return c.Typed.CoreV1().Secrets(ns)
}

// Clusters selects the registration store.
func (c *Client) Clusters() ClusterStore {
	if c == nil {
		return nil
	}
	if c.ClusterStore != nil {
		return c.ClusterStore
	}
	if c.Dynamic == nil {
		return nil
	}
	return c.Dynamic.Resource(GVRCluster)
}

// IsStandalone reports whether management is independent of Kubernetes.
func (c *Client) IsStandalone() bool { return c != nil && c.ClusterStore != nil }
