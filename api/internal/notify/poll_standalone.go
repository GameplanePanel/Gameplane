package notify

import (
	"context"
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
	previous map[string]map[string]*unstructured.Unstructured
	alerted  map[string]bool
}

func newClusterSnapshot() *clusterSnapshot {
	return &clusterSnapshot{previous: map[string]map[string]*unstructured.Unstructured{}, alerted: map[string]bool{}}
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
		list, err := k.Dynamic.Resource(kube.GVRs[kind]).List(probeCtx, metav1.ListOptions{Limit: remoteNotificationObjectLimit + 1})
		cancel()
		if ctx.Err() != nil {
			return
		}
		if err != nil || len(list.Items) > remoteNotificationObjectLimit || list.GetContinue() != "" {
			continue
		}
		current := map[string]*unstructured.Unstructured{}
		for i := range list.Items {
			obj := &list.Items[i]
			if !scope.Allowed(obj.GetNamespace()) || len(validation.IsDNS1123Subdomain(obj.GetName())) != 0 || len(obj.GetUID()) > 128 {
				continue
			}
			key := obj.GetNamespace() + "/" + obj.GetName()
			current[key] = notificationProjection(obj)
		}
		previous := state.previous[kind]
		for key, obj := range current {
			old := previous[key]
			if old == nil || old.GetUID() != obj.GetUID() {
				if kind == "servers" {
					delete(state.alerted, key)
				}
				continue
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
