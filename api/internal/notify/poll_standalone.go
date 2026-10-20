package notify

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/validation"
)

const remoteNotificationObjectLimit = 1024

type clusterSnapshot struct {
	previous    map[string]map[string]*unstructured.Unstructured
	alerted     map[string]bool
	lastWarning map[string]time.Time
}

func newClusterSnapshot() *clusterSnapshot {
	return &clusterSnapshot{previous: map[string]map[string]*unstructured.Unstructured{}, alerted: map[string]bool{}, lastWarning: map[string]time.Time{}}
}

func (n *Notifier) runClusterPoller(ctx context.Context, k *kube.Client, cluster string) {
	state := newClusterSnapshot()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		n.pollClusterSnapshot(ctx, k, cluster, state)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Poll finite, bounded snapshots instead of caching remote watch inventories.
// Failed/oversized snapshots never replace a baseline or manufacture transitions.
func (n *Notifier) pollClusterSnapshot(ctx context.Context, k *kube.Client, cluster string, state *clusterSnapshot) {
	for _, kind := range []string{"servers", "backups", "restores"} {
		probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		current, err := notificationSnapshot(probeCtx, k, kind)
		cancel()
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			if time.Since(state.lastWarning[kind]) >= time.Minute {
				// Never log upstream errors or object data: either may carry
				// credentials. The warning identifies the affected observer.
				slog.Warn("standalone notification snapshot unavailable or exceeds observation limit; retaining baseline", "cluster", cluster, "resource", kind)
				state.lastWarning[kind] = time.Now()
			}
			continue
		}
		previous := state.previous[kind]
		for key, obj := range current {
			old := previous[key]
			if old == nil || old.GetUID() != obj.GetUID() {
				if kind == "servers" {
					delete(state.alerted, key)
				}
				if previous == nil {
					continue
				} // First successful snapshot is silent.
				// An object created and completed between polls still represents
				// a new terminal event. Never pair it with a replaced object's state.
				old = &unstructured.Unstructured{Object: map[string]any{}}
			}
			var events []Event
			if kind == "servers" {
				var alerted bool
				events, alerted = serverEvents(old, obj, state.alerted[key])
				if alerted {
					state.alerted[key] = true
				} else {
					delete(state.alerted, key)
				}
			} else {
				resourceKind := "Backup"
				if kind == "restores" {
					resourceKind = "Restore"
				}
				events = phaseEvents(resourceKind, old, obj)
			}
			for _, e := range events {
				e.Cluster = cluster
				n.Enqueue(stampTS(e))
			}
		}
		if kind == "servers" {
			for key := range state.alerted {
				if current[key] == nil {
					delete(state.alerted, key)
				}
			}
		}
		state.previous[kind] = current
	}
}

func notificationSnapshot(ctx context.Context, k *kube.Client, kind string) (map[string]*unstructured.Unstructured, error) {
	current := map[string]*unstructured.Unstructured{}
	count := 0
	seen := map[string]bool{}
	for _, ns := range scope.AllowedNamespaces {
		if seen[ns] {
			continue
		}
		seen[ns] = true
		// Scope remotely before applying the aggregate object budget. Objects
		// in namespaces the panel ignores cannot exhaust the retained baseline.
		list, err := k.Dynamic.Resource(kube.GVRs[kind]).Namespace(ns).List(ctx, metav1.ListOptions{Limit: int64(remoteNotificationObjectLimit - count + 1)})
		if err != nil {
			return nil, err
		}
		count += len(list.Items)
		if count > remoteNotificationObjectLimit || list.GetContinue() != "" {
			return nil, errors.New("remote notification snapshot exceeds observation limit")
		}
		for i := range list.Items {
			obj := &list.Items[i]
			if obj.GetNamespace() != ns || len(validation.IsDNS1123Subdomain(obj.GetName())) != 0 || len(obj.GetUID()) > 128 {
				continue
			}
			current[ns+"/"+obj.GetName()] = notificationProjection(obj)
		}
	}
	return current, nil
}

// Retain only fields needed to detect alerts, with fixed text limits. Remote spec
// data and arbitrarily many conditions do not belong in notification baselines.
func notificationProjection(obj *unstructured.Unstructured) *unstructured.Unstructured {
	out := &unstructured.Unstructured{Object: map[string]any{}}
	out.SetName(boundedNotificationText(obj.GetName(), 253))
	out.SetNamespace(boundedNotificationText(obj.GetNamespace(), 63))
	out.SetUID(obj.GetUID())
	phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")
	message, _, _ := unstructured.NestedString(obj.Object, "status", "message")
	conditions := []any{}
	for _, typ := range []string{"Healthy", "Ready"} {
		status, reason, detail := condition(obj, typ)
		conditions = append(conditions, map[string]any{"type": typ, "status": boundedNotificationText(status, 64), "reason": boundedNotificationText(reason, 256), "message": boundedNotificationText(detail, 4096)})
	}
	out.Object["status"] = map[string]any{"phase": boundedNotificationText(phase, 64), "message": boundedNotificationText(message, 4096), "conditions": conditions}
	return out
}

func boundedNotificationText(value string, limit int) string {
	if len(value) > limit {
		return strings.Clone(value[:limit])
	}
	return value
}
