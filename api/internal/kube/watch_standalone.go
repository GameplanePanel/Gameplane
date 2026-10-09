package kube

import (
	"context"
	"encoding/json"
	"log/slog"
	"regexp"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/version"
)

const standalonePollInterval = 30 * time.Second

var standaloneVersionRE = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$`)

func watchStandaloneClusters(ctx context.Context, home *Client, reg *Registry, ns string) {
	ticker := time.NewTicker(standalonePollInterval)
	defer ticker.Stop()
	for {
		syncStandaloneClusters(ctx, home, reg, ns)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// syncStandaloneClusters uses a fixed-size worker pool with cancellable remote
// probes. No network operation holds a database transaction or registry lock.
func syncStandaloneClusters(ctx context.Context, home *Client, reg *Registry, ns string) {
	list, err := home.Clusters().List(ctx, metav1.ListOptions{})
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("standalone cluster poll: registration store unavailable")
		}
		return
	}
	jobs := make(chan *unstructured.Unstructured)
	var workers sync.WaitGroup
	for range 4 {
		workers.Go(func() {
			for registration := range jobs {
				probeStandaloneCluster(ctx, home, reg, ns, registration)
			}
		})
	}
	for i := range list.Items {
		select {
		case <-ctx.Done():
			close(jobs)
			workers.Wait()
			return
		case jobs <- &list.Items[i]:
		}
	}
	close(jobs)
	workers.Wait()
	// Recheck under the same lock used to publish loaded clients. A registration
	// added after the list must not be evicted because it missed that snapshot.
	for _, name := range reg.IDs() {
		reg.mu.Lock()
		_, err := home.Clusters().Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			delete(reg.clients, name)
			delete(reg.uids, name)
		}
		reg.mu.Unlock()
	}
}

func probeStandaloneCluster(ctx context.Context, home *Client, reg *Registry, ns string, registration *unstructured.Unstructured) {
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	name := registration.GetName()
	secretName, _, _ := unstructured.NestedString(registration.Object, "spec", "kubeconfigSecret", "name")
	key, _, _ := unstructured.NestedString(registration.Object, "spec", "kubeconfigSecret", "key")
	phase, message, serverVersion := "Unhealthy", "Cluster credentials could not be loaded", ""
	client, err := ClientFromSecret(probeCtx, home, ns, secretName, key)
	if err == nil {
		// Keep a stable client identity while credentials are unchanged, so
		// consumers can retain their informers and transport connections.
		reg.mu.RLock()
		if existing := reg.clients[name]; existing != nil && reg.uids[name] == registration.GetUID() && existing.kubeconfigDigest == client.kubeconfigDigest {
			client = existing
		}
		reg.mu.RUnlock()
		message = "Cluster connection is unavailable"
		var data []byte
		data, err = client.Typed.Discovery().RESTClient().Get().AbsPath("/version").Do(probeCtx).Raw()
		if err == nil {
			var info version.Info
			if json.Unmarshal(data, &info) == nil && len(info.GitVersion) <= 128 && standaloneVersionRE.MatchString(info.GitVersion) {
				phase, message, serverVersion = "Healthy", "", info.GitVersion
			}
		}
	}
	if ctx.Err() != nil {
		return
	}
	// Never persist transport/parser error strings: they may contain credentials
	// or host details. The public status is deliberately a small safe projection.
	registration = registration.DeepCopy()
	registration.Object["status"] = map[string]any{
		"phase": phase, "message": message, "serverVersion": serverVersion,
		"lastCheckTime": time.Now().UTC().Format(time.RFC3339),
	}
	// A delete handler removes the persisted registration before Registry.Remove.
	// Holding this lock across the final read/CAS and publication prevents a slow
	// load from resurrecting a deleted or replaced registration in the registry.
	reg.mu.Lock()
	defer reg.mu.Unlock()
	latest, err := home.Clusters().Get(ctx, name, metav1.GetOptions{})
	if err != nil || latest.GetUID() != registration.GetUID() || latest.GetResourceVersion() != registration.GetResourceVersion() {
		return
	}
	if latest.GetDeletionTimestamp() != nil {
		delete(reg.clients, name)
		delete(reg.uids, name)
		return
	}
	if _, err := home.Clusters().Update(ctx, registration, metav1.UpdateOptions{}); err != nil {
		return
	}
	if client == nil {
		delete(reg.clients, name)
		delete(reg.uids, name)
		return
	}
	reg.clients[name] = client
	reg.uids[name] = registration.GetUID()
}
