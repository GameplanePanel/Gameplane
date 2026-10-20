package ws

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/rbac"
)

// mockStreamCluster speaks the Kubernetes API on a distinct HTTP endpoint.
// Same-named objects exist in each fake cluster, but logs and bearer tokens
// differ, proving transport dispatch rather than merely selector parsing.
func mockStreamCluster(t *testing.T, id string) (*kube.Client, *atomic.Int64, *atomic.Int64) {
	t.Helper()
	hits, attaches := &atomic.Int64{}, &atomic.Int64{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// RBAC may read a server to check ownership even when access is denied.
		// Count workload/stream access separately from that authorization lookup.
		if !strings.Contains(r.URL.Path, "/gameservers/") {
			hits.Add(1)
		}
		if r.Header.Get("Authorization") != "Bearer "+id {
			t.Errorf("%s upstream received wrong credentials", id)
			http.Error(w, "wrong credentials", http.StatusUnauthorized)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/log"):
			_, _ = w.Write([]byte(id + " logs\n"))
		case strings.HasSuffix(r.URL.Path, "/attach"):
			attaches.Add(1)
			if r.Method != http.MethodPost || r.URL.Query().Get("container") != "game" {
				t.Errorf("unexpected attach request: %s %s", r.Method, r.URL.Path)
			}
			// A real SPDY round trip is exercised by the two-cluster e2e. Here
			// failure proves that both the URL and executor credentials reached
			// the selected API and that its internal message is not leaked.
			http.Error(w, "private upstream diagnostic", http.StatusForbidden)
		case strings.Contains(r.URL.Path, "/gameservers/"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"apiVersion": "gameplane.local/v1alpha1", "kind": "GameServer",
				"metadata": map[string]any{"name": "alpha", "namespace": "gameplane-games", "uid": "gs-uid"},
			})
		case strings.Contains(r.URL.Path, "/statefulsets/"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(&appsv1.StatefulSet{
				TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "StatefulSet"},
				ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "gameplane-games", UID: "ss-uid",
					OwnerReferences: []metav1.OwnerReference{{APIVersion: "gameplane.local/v1alpha1", Kind: "GameServer", Name: "alpha", UID: "gs-uid", Controller: boolPtr(true)}}},
			})
		case strings.HasSuffix(r.URL.Path, "/pods/alpha-0"):
			w.Header().Set("Content-Type", "application/json")
			pod := newPod("alpha-0", nil, false)
			pod.TypeMeta = metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"}
			_ = json.NewEncoder(w).Encode(pod)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	client, err := kube.New(&rest.Config{Host: srv.URL, BearerToken: id})
	if err != nil {
		t.Fatal(err)
	}
	return client, hits, attaches
}

func streamRouter(reg *kube.Registry, user *auth.User) http.Handler {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(auth.WithUser(req.Context(), user)))
		})
	})
	r.Use(rbac.Middleware(reg))
	Mount(r, reg, "", "", "")
	return r
}

func remoteStreamUser() *auth.User {
	return &auth.User{ID: 42, Perms: map[string]map[string]map[string]struct{}{
		"remote": {"gameplane-games": {"servers:read": {}, "servers:console": {}}},
	}}
}

func TestMultiClusterStreams_SelectRemoteClientAndCredentials(t *testing.T) {
	t.Parallel()
	for _, route := range []string{"logs/pod?from=end", "logs/pod?from=start", "console-pty?"} {
		t.Run(route, func(t *testing.T) {
			local, localHits, _ := mockStreamCluster(t, "local")
			remote, _, remoteAttaches := mockStreamCluster(t, "remote")
			reg := kube.NewRegistry("local")
			reg.Set("local", local)
			reg.Set("remote", remote)
			srv := httptest.NewServer(streamRouter(reg, remoteStreamUser()))
			defer srv.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			conn, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/ws/servers/alpha/"+route+"&cluster=remote", nil)
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
			if err != nil {
				t.Fatal(err)
			}
			defer conn.CloseNow()
			if resp == nil || resp.StatusCode != http.StatusSwitchingProtocols {
				t.Fatalf("expected successful WebSocket upgrade, got response %+v", resp)
			}
			_, data, err := conn.Read(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(route, "logs") && string(data) != "remote logs\n" {
				t.Errorf("wrong cluster output: %q", data)
			}
			if strings.HasPrefix(route, "console") {
				var env ptyEnvelope
				if err := json.Unmarshal(data, &env); err != nil {
					t.Fatal(err)
				}
				if env.Kind != "err" || env.Body != "attach stream ended" {
					t.Errorf("unsafe/unexpected attach error: %+v", env)
				}
				if remoteAttaches.Load() != 1 {
					t.Errorf("remote attach count=%d", remoteAttaches.Load())
				}
			}
			if localHits.Load() != 0 {
				t.Fatalf("remote user accessed local cluster %d times", localHits.Load())
			}
		})
	}
}

func TestMultiClusterStreams_FailClosed(t *testing.T) {
	t.Parallel()
	local, localHits, _ := mockStreamCluster(t, "local")
	remote, _, _ := mockStreamCluster(t, "remote")
	reg := kube.NewRegistry("local")
	reg.Set("local", local)
	reg.Set("remote", remote)
	router := streamRouter(reg, remoteStreamUser())
	for _, route := range []string{"logs/pod", "console-pty"} {
		for _, tc := range []struct {
			query  string
			status int
		}{
			{"", http.StatusForbidden}, {"?cluster=local", http.StatusForbidden},
			{"?cluster=unknown", http.StatusBadRequest}, {"?cluster=remote&namespace=not-allowed", http.StatusBadRequest},
		} {
			rr := httptest.NewRecorder()
			router.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/ws/servers/alpha/"+route+tc.query, nil))
			if rr.Code != tc.status {
				t.Errorf("%s%s: got %d want %d", route, tc.query, rr.Code, tc.status)
			}
		}
	}
	if localHits.Load() != 0 {
		t.Errorf("rejected remote-only caller reached local cluster")
	}
	reg.Set("remote", nil)
	for _, route := range []string{"logs/pod", "console-pty"} {
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/ws/servers/alpha/"+route+"?cluster=remote", nil))
		if rr.Code != http.StatusServiceUnavailable {
			t.Errorf("unavailable remote: got %d", rr.Code)
		}
	}
	// Removing the remote registration cannot cause local fallback.
	reg.Remove("remote")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/ws/servers/alpha/console-pty?cluster=remote", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("removed cluster: got %d", rr.Code)
	}
	if localHits.Load() != 0 {
		t.Error("removed cluster fell back locally")
	}
}

func TestServerPod_RejectsStaleOrUnrelatedWorkloads(t *testing.T) {
	t.Parallel()
	for _, change := range []string{"server-recreated", "statefulset-recreated", "unrelated-pod", "missing-pod-uid"} {
		t.Run(change, func(t *testing.T) {
			k := &kube.Client{}
			_ = streamTestRegistry(k)
			ctx := t.Context()
			if _, err := serverPod(ctx, k, "gameplane-games", "alpha"); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "server-recreated":
				gs, _ := k.GetServer(ctx, "gameplane-games", "alpha")
				gs.SetUID("new-server-uid")
				_, _ = k.Dynamic.Resource(kube.GVRs["servers"]).Namespace("gameplane-games").Update(ctx, gs, metav1.UpdateOptions{})
			case "statefulset-recreated":
				ss, _ := k.Typed.AppsV1().StatefulSets("gameplane-games").Get(ctx, "alpha", metav1.GetOptions{})
				ss.UID = "new-ss-uid"
				_, _ = k.Typed.AppsV1().StatefulSets("gameplane-games").Update(ctx, ss, metav1.UpdateOptions{})
			default:
				pod, _ := k.Typed.CoreV1().Pods("gameplane-games").Get(ctx, "alpha-0", metav1.GetOptions{})
				if change == "unrelated-pod" {
					pod.OwnerReferences = nil
				} else {
					pod.UID = ""
				}
				_, _ = k.Typed.CoreV1().Pods("gameplane-games").Update(ctx, pod, metav1.UpdateOptions{})
			}
			if _, err := serverPod(ctx, k, "gameplane-games", "alpha"); err == nil {
				t.Fatal("unrelated/stale workload accepted")
			}
		})
	}
}

func TestPodLogs_StopsWhenPodIsReplaced(t *testing.T) {
	k := &kube.Client{}
	_ = streamTestRegistry(k)
	proxy := &podLogProxy{k: k, podUID: "old-pod-uid"}
	if _, err := proxy.readPod(t.Context(), "gameplane-games", "alpha-0"); err == nil {
		t.Fatal("replacement pod accepted by old stream")
	}
}
