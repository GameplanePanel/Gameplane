package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/go-chi/chi/v5"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/watch"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/rbac"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// MountEvents exposes /events as a Server-Sent Events stream mirroring
// Kubernetes watch events on the Gameplane CRDs, for clients that want
// cache-freshness without polling.
func MountEvents(r chi.Router, reg *kube.Registry) {
	r.Get("/events", eventsHandler(reg))
}

func eventsHandler(reg *kube.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		k, ok := resolveCluster(w, req, reg)
		if !ok {
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		// Scope the namespaced watches to the caller's permitted namespace
		// (viewers are pinned to the default). Previously this watched
		// metav1.NamespaceAll and streamed every namespace's objects —
		// including Secret refs / repo URLs — to any authenticated caller,
		// bypassing the scope check every other read path enforces.
		ns, err := scope.Resolve(req)
		if err != nil {
			httperr.Write(w, req, err)
			return
		}
		// The route itself needs servers:read. Each kind is then streamed
		// only when the caller holds that kind's read permission (the one
		// its GET route needs) in this cluster and namespace.
		clusterID, err := scope.ResolveCluster(req, reg)
		if err != nil {
			httperr.Write(w, req, err)
			return
		}
		caller := auth.UserFromContext(req.Context())

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		ctx, cancel := context.WithCancel(req.Context())
		defer cancel()

		// Fan-in: per-CRD watch goroutines send framed events to one
		// writer. A single writer means no concurrent writes to the
		// ResponseWriter (the old code raced 4+ goroutines on it), and a
		// write error cancels ctx so every watcher stops (the old code
		// ignored write errors and leaked watchers on client disconnect).
		events := make(chan []byte, 32)
		var wg sync.WaitGroup
		for path, gvr := range kube.GVRs {
			path, gvr := path, gvr
			if !canReadKind(caller, path, clusterID, ns) {
				continue
			}
			ri := k.Dynamic.Resource(gvr)
			// Watch from the collection's current resourceVersion: an empty
			// one makes the API server replay every existing object as a
			// synthetic ADDED event, which floods the dashboard with one
			// refetch and one notification per object on every connect.
			// Limit 1 keeps the list cheap; only its resourceVersion is used.
			// If the list fails, fall back to the old replaying watch.
			var list *unstructured.UnstructuredList
			var listErr error
			if cluster(gvr) {
				list, listErr = ri.List(ctx, metav1.ListOptions{Limit: 1})
			} else {
				list, listErr = ri.Namespace(ns).List(ctx, metav1.ListOptions{Limit: 1})
			}
			watchOpts := metav1.ListOptions{}
			if listErr == nil {
				watchOpts.ResourceVersion = list.GetResourceVersion()
			}
			var watcher watch.Interface
			if cluster(gvr) {
				watcher, err = ri.Watch(ctx, watchOpts)
			} else {
				watcher, err = ri.Namespace(ns).Watch(ctx, watchOpts)
			}
			if err != nil {
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer watcher.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case ev, ok := <-watcher.ResultChan():
						if !ok {
							return
						}
						if ev.Type == "" {
							continue
						}
						u, ok := ev.Object.(*unstructured.Unstructured)
						if !ok {
							continue
						}
						if path == "servers" {
							// Watch objects are shared with the client's cache: redact a copy.
							u = u.DeepCopy()
							// Fresh cache per event: a template edited since the previous
							// event must take effect immediately.
							newConfigRuleCache(k).redact(ctx, u)
						}
						b, mErr := json.Marshal(map[string]any{
							"kind":      path,
							"eventType": ev.Type,
							"object":    u.Object,
						})
						if mErr != nil {
							continue
						}
						select {
						case events <- b:
						case <-ctx.Done():
							return
						}
					}
				}
			}()
		}
		// Closes the channel once all watchers have exited so the writer
		// loop below terminates cleanly.
		go func() { wg.Wait(); close(events) }()

		for b := range events {
			if _, werr := fmt.Fprintf(w, "data: %s\n\n", b); werr != nil {
				cancel() // client gone → stop the watchers
				break
			}
			flusher.Flush()
		}
	}
}

// canReadKind reports whether u may read the resource kind served at
// /<path> in the given cluster and namespace, using the read permission
// the rbac rule table requires for a GET of that path. An unknown path is
// never readable.
func canReadKind(u *auth.User, path, clusterID, ns string) bool {
	perm, ok := rbac.ReadPermission(path)
	if !ok {
		return false
	}
	if perm == "" {
		return u != nil
	}
	return u.Can(perm, rbac.Namespaced(perm), clusterID, ns)
}
