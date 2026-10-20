package handlers

import (
	"net/http"
	"sort"
	"time"

	"github.com/go-chi/chi/v5"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
)

// MountPodEvents exposes GET /servers/{name}/events: a snapshot of the
// Kubernetes Events about a GameServer's pod (and its StatefulSet /
// GameServer objects), newest first. The dashboard polls this to explain
// why a server is still provisioning — image-pull progress, scheduling
// failures, crash-loops — alongside the container log, before the game
// ever writes a line of its own.
//
// It's a read: the route lives under /servers, so the RBAC middleware
// already gates it behind servers:read, and scope.Resolve pins the
// namespace. The API's ClusterRole already grants events get/list.
func MountPodEvents(r chi.Router, reg *kube.Registry) {
	r.Get("/servers/{name}/events", podEventsHandler(reg))
}

// maxPodEvents caps the snapshot so a long-running, churny pod can't return
// an unbounded list. Kubernetes TTLs events (~1h by default), so this is a
// recency window, not full history.
const maxPodEvents = 50

// PodEvent is the dashboard-facing shape of a Kubernetes Event. It mirrors
// the fields the Overview "Recent events" card renders.
type PodEvent struct {
	ID      string `json:"id"`
	Time    string `json:"time"` // RFC3339, UTC
	Type    string `json:"type"` // Normal | Warning
	Reason  string `json:"reason"`
	Message string `json:"message"`
	Source  string `json:"source"` // reporting component, e.g. kubelet
	Object  string `json:"object"` // involved object, e.g. Pod/alpha-0
	Count   int32  `json:"count"`
}

func podEventsHandler(reg *kube.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		k, ok := resolveCluster(w, req, reg)
		if !ok {
			return
		}
		name := chi.URLParam(req, "name")
		ns, ok := resolveNS(w, req)
		if !ok {
			return
		}
		// List the namespace's events and filter client-side: a field
		// selector can only match one involvedObject, but a server's
		// events span its Pod, StatefulSet and GameServer. Events are
		// namespace-scoped and TTL'd, so the list stays small.
		list, err := k.Typed.CoreV1().Events(ns).List(req.Context(), metav1.ListOptions{})
		if err != nil {
			httperr.Write(w, req, err)
			return
		}
		writeJSON(w, collectServerEvents(list.Items, name))
	}
}

// collectServerEvents keeps only the events about the named server's
// Pod/StatefulSet/GameServer, maps them to the DTO, and returns them
// newest-first capped at maxPodEvents. Pure, so it's unit-testable without
// a cluster.
func collectServerEvents(events []corev1.Event, name string) []PodEvent {
	want := map[string]bool{
		"Pod/" + name + "-0":  true,
		"StatefulSet/" + name: true,
		"GameServer/" + name:  true,
	}

	type sortable struct {
		ev *corev1.Event
		t  time.Time
	}
	matched := make([]sortable, 0, len(events))
	for i := range events {
		ev := &events[i]
		key := ev.InvolvedObject.Kind + "/" + ev.InvolvedObject.Name
		if want[key] {
			matched = append(matched, sortable{ev: ev, t: eventTime(ev)})
		}
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].t.After(matched[j].t) })

	out := make([]PodEvent, 0, len(matched))
	for _, m := range matched {
		out = append(out, toPodEvent(m.ev, m.t))
	}
	if len(out) > maxPodEvents {
		out = out[:maxPodEvents]
	}
	return out
}

func toPodEvent(ev *corev1.Event, t time.Time) PodEvent {
	source := ev.Source.Component
	if source == "" {
		source = ev.ReportingController
	}
	id := string(ev.UID)
	if id == "" {
		id = ev.Namespace + "/" + ev.Name
	}
	return PodEvent{
		ID:      id,
		Time:    t.UTC().Format(time.RFC3339),
		Type:    ev.Type,
		Reason:  ev.Reason,
		Message: ev.Message,
		Source:  source,
		Object:  ev.InvolvedObject.Kind + "/" + ev.InvolvedObject.Name,
		Count:   ev.Count,
	}
}

// eventTime picks the most recent timestamp an Event carries. The legacy
// core/v1 events kubelet and the scheduler still emit set LastTimestamp;
// newer event series set EventTime; fall back to the object's creation.
func eventTime(ev *corev1.Event) time.Time {
	if !ev.LastTimestamp.IsZero() {
		return ev.LastTimestamp.Time
	}
	if !ev.EventTime.IsZero() {
		return ev.EventTime.Time
	}
	return ev.CreationTimestamp.Time
}
