package notify

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// resyncPeriod is the shared informer factory's safety net against missed
// watch events. Transition detection keys on old≠new, and a resync replays
// old==new, so resyncs can never re-emit an event.
const resyncPeriod = 10 * time.Minute

// runWatchers starts informers on the notifiable CRDs. Only UpdateFunc is
// registered: the informer's initial list fires AddFunc, so existing state
// seeds silently and only transitions observed while the API runs notify —
// which is also the whole restart story (a failure that completes while
// the API is down is missed here but still visible in the dashboard).
func (n *Notifier) runWatchers(ctx context.Context) {
	n.runClusterWatchers(ctx, n.k, "")
}

func (n *Notifier) runClusterWatchers(ctx context.Context, k *kube.Client, cluster string) {
	if k == nil || k.Dynamic == nil {
		return
	}
	factory := dynamicinformer.NewDynamicSharedInformerFactory(k.Dynamic, resyncPeriod)

	// alerted tracks GameServers with an un-recovered unhealthy alert, so
	// recovery events pair with an outage instead of firing on every start.
	var (
		mu      sync.Mutex
		alerted = map[string]bool{}
	)

	if _, err := factory.ForResource(kube.GVRs["servers"]).Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: func(oldObj, newObj any) {
			oldU, ok1 := oldObj.(*unstructured.Unstructured)
			newU, ok2 := newObj.(*unstructured.Unstructured)
			if !ok1 || !ok2 || !scope.Allowed(newU.GetNamespace()) {
				return
			}
			key := newU.GetNamespace() + "/" + newU.GetName()
			mu.Lock()
			events, nowAlerted := serverEvents(oldU, newU, alerted[key])
			if nowAlerted {
				alerted[key] = true
			} else {
				delete(alerted, key)
			}
			mu.Unlock()
			for _, e := range events {
				e.Cluster = cluster
				n.Enqueue(stampTS(e))
			}
		},
		DeleteFunc: func(obj any) {
			key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
			if err != nil {
				return
			}
			mu.Lock()
			delete(alerted, key)
			mu.Unlock()
		},
	}); err != nil {
		slog.Warn("notify: registering gameserver watcher failed", "err", err)
	}

	for _, res := range []struct{ gvr, kind string }{
		{"backups", "Backup"},
		{"restores", "Restore"},
	} {
		kind := res.kind
		if _, err := factory.ForResource(kube.GVRs[res.gvr]).Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
			UpdateFunc: func(oldObj, newObj any) {
				oldU, ok1 := oldObj.(*unstructured.Unstructured)
				newU, ok2 := newObj.(*unstructured.Unstructured)
				if !ok1 || !ok2 || !scope.Allowed(newU.GetNamespace()) {
					return
				}
				for _, e := range phaseEvents(kind, oldU, newU) {
					e.Cluster = cluster
					n.Enqueue(stampTS(e))
				}
			},
		}); err != nil {
			slog.Warn("notify: registering watcher failed", "kind", kind, "err", err)
		}
	}

	factory.Start(ctx.Done())
	// Block until the initial list lands in the caches: transitions can
	// only be observed against a synced baseline, and returning here lets
	// callers sequence "watching" deterministically.
	factory.WaitForCacheSync(ctx.Done())
}

func stampTS(e Event) Event {
	e.TS = time.Now().UTC().Format(time.RFC3339)
	return e
}

// statusPhase returns status.phase, or "" when unset.
func statusPhase(u *unstructured.Unstructured) string {
	p, _, _ := unstructured.NestedString(u.Object, "status", "phase")
	return p
}

// condition returns the status/reason/message of the named condition, or
// empty strings when it isn't present.
func condition(u *unstructured.Unstructured, typ string) (status, reason, message string) {
	conds, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	for _, c := range conds {
		m, ok := c.(map[string]any)
		if !ok || m["type"] != typ {
			continue
		}
		s, _ := m["status"].(string)
		r, _ := m["reason"].(string)
		msg, _ := m["message"].(string)
		return s, r, msg
	}
	return "", "", ""
}

// serverEvents computes the notification events for one observed
// GameServer update, given whether an unhealthy alert is already
// outstanding. It returns the events to emit and the new outstanding
// state. Pure — the informer plumbing stays out so transitions are
// table-testable.
//
// Two triggers mean "unhealthy": the phase escalating to Failed (terminal
// startup failure — bad image, crash-loop, non-zero exit), and a
// previously-healthy server losing its agent heartbeat, which the operator
// expresses as Healthy True→False with reason AgentStale while the phase
// slides back to Starting (it never reaches Failed on that path). User-
// intended transitions (Stopping/Stopped/Suspended) match neither trigger.
func serverEvents(oldU, newU *unstructured.Unstructured, alerted bool) ([]Event, bool) {
	oldPhase, newPhase := statusPhase(oldU), statusPhase(newU)
	oldHealthy, _, _ := condition(oldU, "Healthy")
	newHealthy, newHealthyReason, _ := condition(newU, "Healthy")

	base := Event{Kind: "GameServer", Namespace: newU.GetNamespace(), Name: newU.GetName()}

	if !alerted {
		if newPhase == "Failed" && oldPhase != "Failed" {
			e := base
			e.Type = EventServerUnhealthy
			// The Ready condition carries the specific startup-failure
			// reason (ImagePullFailed, CrashLoopBackOff, ContainerExited).
			_, e.Reason, e.Message = condition(newU, "Ready")
			if e.Reason == "" {
				e.Reason = "Failed"
			}
			return []Event{e}, true
		}
		if oldHealthy == "True" && newHealthy == "False" && newHealthyReason == "AgentStale" {
			e := base
			e.Type = EventServerUnhealthy
			e.Reason = "AgentStale"
			e.Message = "the agent stopped reporting heartbeats; the server may have hung or crashed"
			return []Event{e}, true
		}
	}
	if alerted && newHealthy == "True" && oldHealthy != "True" {
		e := base
		e.Type = EventServerRecovered
		e.Reason = "AgentFresh"
		e.Message = "the server is running and the agent is reporting heartbeats again"
		return []Event{e}, false
	}
	return nil, alerted
}

// phaseEvents computes the events for a Backup or Restore update: exactly
// the transitions into Succeeded or Failed, carrying status.message (the
// operator's human-readable failure reason).
func phaseEvents(kind string, oldU, newU *unstructured.Unstructured) []Event {
	oldPhase, newPhase := statusPhase(oldU), statusPhase(newU)
	if newPhase == oldPhase {
		return nil
	}
	var t EventType
	switch {
	case kind == "Backup" && newPhase == "Failed":
		t = EventBackupFailed
	case kind == "Backup" && newPhase == "Succeeded":
		t = EventBackupSucceeded
	case kind == "Restore" && newPhase == "Failed":
		t = EventRestoreFailed
	case kind == "Restore" && newPhase == "Succeeded":
		t = EventRestoreSucceeded
	default:
		return nil
	}
	msg, _, _ := unstructured.NestedString(newU.Object, "status", "message")
	return []Event{{Type: t, Kind: kind, Namespace: newU.GetNamespace(), Name: newU.GetName(), Message: msg}}
}
