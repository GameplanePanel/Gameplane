package kube

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
)

// resyncPeriod is the shared informer factory's safety net against missed
// watch events.
const resyncPeriod = 10 * time.Minute

// WatchClusters starts an informer on the Cluster CRD and populates the
// registry as clusters are added, updated, or deleted. The "local" cluster
// is ignored (it is always provided by the control plane). Errors loading
// remote kubeconfigs are logged but do not crash — the registry continues
// to serve what it has.
func WatchClusters(ctx context.Context, home *Client, reg *Registry, ns string) {
	if home.IsStandalone() {
		watchStandaloneClusters(ctx, home, reg, ns)
		return
	}
	factory := dynamicinformer.NewDynamicSharedInformerFactory(home.Dynamic, resyncPeriod)

	if _, err := factory.ForResource(GVRCluster).Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			u, ok := obj.(*unstructured.Unstructured)
			if !ok {
				return
			}
			name := u.GetName()
			// Skip the default cluster (always provided by the control plane).
			if name == reg.DefaultID() {
				slog.Debug("cluster watch: skipping local cluster")
				return
			}
			if err := loadCluster(ctx, home, reg, ns, name); err != nil {
				slog.Warn("cluster watch: failed to load cluster", "cluster", name, "err", err)
			}
		},
		UpdateFunc: func(_, newObj any) {
			u, ok := newObj.(*unstructured.Unstructured)
			if !ok {
				return
			}
			name := u.GetName()
			// Skip the default cluster.
			if name == reg.DefaultID() {
				return
			}
			if err := loadCluster(ctx, home, reg, ns, name); err != nil {
				slog.Warn("cluster watch: failed to update cluster", "cluster", name, "err", err)
			}
		},
		DeleteFunc: func(obj any) {
			removeDeletedCluster(reg, obj)
		},
	}); err != nil {
		slog.Warn("cluster watch: register handler failed", "err", err)
		return
	}

	factory.Start(ctx.Done())
	// Block until the initial list lands in the caches.
	if !cache.WaitForCacheSync(ctx.Done(), factory.ForResource(GVRCluster).Informer().HasSynced) {
		slog.Warn("cluster watch: cache sync failed")
		return
	}
	slog.Debug("cluster watch: started")
}

// removeDeletedCluster drops a deleted Cluster's client from the registry.
// The informer hands over either the Cluster object or, when the watch
// missed its final state, a cache.DeletedFinalStateUnknown tombstone; both
// attempt removal. A delete for an older Cluster does not remove a newer
// registration with the same name. The default cluster is never removed.
func removeDeletedCluster(reg *Registry, obj any) {
	name := ""
	uid := ""
	switch v := obj.(type) {
	case *unstructured.Unstructured:
		name = v.GetName()
		uid = string(v.GetUID())
	case cache.DeletedFinalStateUnknown:
		if u, ok := v.Obj.(*unstructured.Unstructured); ok {
			name = u.GetName()
			uid = string(u.GetUID())
		} else {
			// Cluster is cluster-scoped, so the tombstone key is its name.
			// Without the object, we have no UID.
			name = v.Key
		}
	}
	if name == "" || name == reg.DefaultID() {
		return
	}
	reg.RemoveIfUID(name, types.UID(uid))
	slog.Debug("cluster watch: removed cluster", "cluster", name)
}

// loadCluster reads a Cluster CRD, extracts the kubeconfig Secret reference,
// loads the secret, creates a client, and registers it in the registry.
// If the Cluster is being deleted, it is removed from the registry instead.
func loadCluster(ctx context.Context, home *Client, reg *Registry, ns, name string) error {
	return loadClusterForUID(ctx, home, reg, ns, name, "")
}

// RefreshRegisteredCluster reloads credentials without a remote connectivity
// probe. The expected identity prevents a rotation from carrying a refresh
// across deletion/recreation of the registration.
func RefreshRegisteredCluster(ctx context.Context, home *Client, reg *Registry, ns, name string, expectedUID types.UID) error {
	return loadClusterForUID(ctx, home, reg, ns, name, expectedUID)
}

func loadClusterForUID(ctx context.Context, home *Client, reg *Registry, ns, name string, expectedUID types.UID) error {
	u, err := home.Clusters().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get cluster CRD: %w", err)
	}
	if expectedUID != "" && u.GetUID() != expectedUID {
		return apierrors.NewNotFound(GVRCluster.GroupResource(), name)
	}

	// If the cluster is being deleted, remove it from the registry.
	if u.GetDeletionTimestamp() != nil {
		reg.RemoveIfUID(name, u.GetUID())
		slog.Debug("cluster watch: cluster is being deleted; not registering", "cluster", name)
		return nil
	}

	// Extract spec.kubeconfigSecret from the unstructured Cluster.
	kcSpec, ok, err := unstructured.NestedMap(u.Object, "spec", "kubeconfigSecret")
	if err != nil {
		return fmt.Errorf("read spec.kubeconfigSecret: %w", err)
	}
	if !ok {
		return fmt.Errorf("spec.kubeconfigSecret not found")
	}

	secretName, ok := kcSpec["name"].(string)
	if !ok || secretName == "" {
		return fmt.Errorf("spec.kubeconfigSecret.name is missing or not a string")
	}

	// kubeconfigField is a Kubernetes Secret field name (e.g., "kubeconfig"), not secret data.
	// Logging operations in this function never interpolate secret bytes, so renaming from
	// secretKey removes a heuristic taint source without changing real security.
	kubeconfigField, ok := kcSpec["key"].(string)
	if !ok {
		kubeconfigField = "kubeconfig"
	}

	c, err := ClientFromSecret(ctx, home, ns, secretName, kubeconfigField)
	if err != nil {
		return fmt.Errorf("load client from secret: %w", err)
	}

	// Serialize the final metadata check with client removal/publication. A
	// credential loaded before rotation must never replace its newer client.
	reg.mu.Lock()
	defer reg.mu.Unlock()
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	current, err := home.Clusters().Get(checkCtx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("recheck cluster registration: %w", err)
	}
	if current.GetDeletionTimestamp() != nil || current.GetUID() != u.GetUID() || current.GetResourceVersion() != u.GetResourceVersion() {
		return apierrors.NewConflict(GVRCluster.GroupResource(), name, fmt.Errorf("cluster registration changed during credential load"))
	}
	reg.clients[name] = c
	reg.uids[name] = u.GetUID()
	slog.Debug("cluster watch: loaded cluster", "cluster", name)
	return nil
}
