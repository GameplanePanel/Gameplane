package handlers

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// sseRecorder is a goroutine-safe http.ResponseWriter and http.Flusher, so
// a test can read the stream while the handler is still writing it.
type sseRecorder struct {
	mu     sync.Mutex
	header http.Header
	buf    bytes.Buffer
}

func (s *sseRecorder) Header() http.Header { return s.header }

func (s *sseRecorder) WriteHeader(int) {}

func (s *sseRecorder) Flush() {}

func (s *sseRecorder) Write(b []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(b)
}

func (s *sseRecorder) body() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// eventsWatchRecorder answers every watch with a fake watcher that already
// holds one event, and records which resources were watched.
type eventsWatchRecorder struct {
	mu      sync.Mutex
	watched map[string]bool
}

func (e *eventsWatchRecorder) react(action clienttesting.Action) (bool, watch.Interface, error) {
	res := action.GetResource().Resource
	e.mu.Lock()
	e.watched[res] = true
	e.mu.Unlock()
	fw := watch.NewFakeWithChanSize(1, false)
	fw.Add(&unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "Probe",
		"metadata":   map[string]any{"name": "probe-" + res, "namespace": scope.DefaultNamespace},
	}})
	return true, fw, nil
}

func (e *eventsWatchRecorder) resources() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, 0, len(e.watched))
	for r := range e.watched {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

func eventsPermSet(perms ...string) map[string]struct{} {
	set := map[string]struct{}{}
	for _, p := range perms {
		set[p] = struct{}{}
	}
	return set
}

// TestEvents_StreamsOnlyReadableKinds checks that the event stream watches
// and sends only the resource kinds the caller may read in the resolved
// cluster and namespace.
func TestEvents_StreamsOnlyReadableKinds(t *testing.T) {
	type permMap = map[string]map[string]map[string]struct{}
	cases := []struct {
		name  string
		perms permMap
		want  []string // kube.GVRs keys the stream may carry
	}{
		{
			name:  "servers:read only",
			perms: permMap{scope.DefaultCluster: {"*": eventsPermSet("servers:read")}},
			want:  []string{"servers"},
		},
		{
			name:  "namespace binding with servers:read and backups:read",
			perms: permMap{scope.DefaultCluster: {scope.DefaultNamespace: eventsPermSet("servers:read", "backups:read")}},
			want:  []string{"backups", "restores", "servers"},
		},
		{
			name:  "cluster-wide servers:read and templates:read",
			perms: permMap{scope.DefaultCluster: {"*": eventsPermSet("servers:read", "templates:read")}},
			want:  []string{"servers", "templates"},
		},
		{
			// Templates, like namespaced workload kinds, require permission
			// on the selected cluster.
			name:  "bindings on another cluster only",
			perms: permMap{"other": {"*": eventsPermSet("servers:read", "backups:read", "schedules:read", "templates:read")}},
			want:  []string{},
		},
		{
			name: "local server reader cannot stream another cluster's templates",
			perms: permMap{
				scope.DefaultCluster: {"*": eventsPermSet("servers:read")},
				"other":              {"*": eventsPermSet("templates:read")},
			},
			want: []string{"servers"},
		},
		{
			name:  "admin",
			perms: permMap{scope.DefaultCluster: {"*": eventsPermSet("*")}},
			want:  []string{"backups", "restores", "schedules", "servers", "templates"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &eventsWatchRecorder{watched: map[string]bool{}}
			dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
				kube.GVRs["servers"]:   "GameServerList",
				kube.GVRs["templates"]: "GameTemplateList",
				kube.GVRs["backups"]:   "BackupList",
				kube.GVRs["schedules"]: "BackupScheduleList",
				kube.GVRs["restores"]:  "RestoreList",
			})
			dyn.PrependWatchReactor("*", rec.react)
			reg := kube.NewRegistry(scope.DefaultCluster)
			reg.Set(scope.DefaultCluster, &kube.Client{Dynamic: dyn, Typed: kubefake.NewClientset()})

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			u := &auth.User{ID: 9, Username: "events-reader", Perms: tc.perms}
			req := httptest.NewRequestWithContext(auth.WithUser(ctx, u), http.MethodGet, "/events", nil)
			w := &sseRecorder{header: http.Header{}}
			done := make(chan struct{})
			go func() {
				defer close(done)
				eventsHandler(reg)(w, req)
			}()

			// Every watched kind has one queued event: wait until the
			// readable ones have been written, then end the stream.
			deadline := time.Now().Add(5 * time.Second)
			for strings.Count(w.body(), "data: ") < len(tc.want) && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			cancel()
			<-done

			wantRes := make([]string, 0, len(tc.want))
			for _, k := range tc.want {
				wantRes = append(wantRes, kube.GVRs[k].Resource)
			}
			sort.Strings(wantRes)
			if got := rec.resources(); strings.Join(got, ",") != strings.Join(wantRes, ",") {
				t.Errorf("watched %v, want %v", got, wantRes)
			}
			body := w.body()
			for k := range kube.GVRs {
				sent := strings.Contains(body, `"kind":"`+k+`"`)
				if want := slices.Contains(tc.want, k); sent != want {
					t.Errorf("kind %q sent=%v, want %v; body=%s", k, sent, want, body)
				}
			}
		})
	}
}

// TestEvents_WatchesFromListResourceVersion pins that /events lists each
// kind first and watches from the list's resourceVersion, so opening the
// stream does not replay every existing object as a synthetic ADDED event.
func TestEvents_WatchesFromListResourceVersion(t *testing.T) {
	type permMap = map[string]map[string]map[string]struct{}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		kube.GVRs["servers"]:   "GameServerList",
		kube.GVRs["templates"]: "GameTemplateList",
		kube.GVRs["backups"]:   "BackupList",
		kube.GVRs["schedules"]: "BackupScheduleList",
		kube.GVRs["restores"]:  "RestoreList",
	})
	dyn.PrependReactor("list", "*", func(clienttesting.Action) (bool, runtime.Object, error) {
		l := &unstructured.UnstructuredList{}
		l.SetResourceVersion("4242")
		return true, l, nil
	})
	var mu sync.Mutex
	rvs := map[string]string{}
	dyn.PrependWatchReactor("*", func(action clienttesting.Action) (bool, watch.Interface, error) {
		if wa, ok := action.(clienttesting.WatchAction); ok {
			mu.Lock()
			rvs[action.GetResource().Resource] = wa.GetWatchRestrictions().ResourceVersion
			mu.Unlock()
		}
		return true, watch.NewFake(), nil
	})
	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.Set(scope.DefaultCluster, &kube.Client{Dynamic: dyn, Typed: kubefake.NewClientset()})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	u := &auth.User{ID: 9, Username: "events-reader", Perms: permMap{scope.DefaultCluster: {"*": eventsPermSet("*")}}}
	req := httptest.NewRequestWithContext(auth.WithUser(ctx, u), http.MethodGet, "/events", nil)
	w := &sseRecorder{header: http.Header{}}
	done := make(chan struct{})
	go func() {
		defer close(done)
		eventsHandler(reg)(w, req)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(rvs)
		mu.Unlock()
		if n == len(kube.GVRs) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	for _, gvr := range kube.GVRs {
		if got := rvs[gvr.Resource]; got != "4242" {
			t.Errorf("watch %s resourceVersion = %q, want %q (from the list)", gvr.Resource, got, "4242")
		}
	}
}
