package handlers

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"

	"github.com/GameplanePanel/gameplane/api/internal/audit"
	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/notify"
	"github.com/GameplanePanel/gameplane/api/internal/rbac"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
	"github.com/GameplanePanel/gameplane/api/internal/ws"
)

// TestRejectRemoteCluster_TableDriven is the guard's core logic in
// isolation: a non-local ?cluster= selector must write a 501 (and report
// "rejected"); an absent, blank, or explicitly-local selector must let the
// caller proceed.
func TestRejectRemoteCluster_TableDriven(t *testing.T) {
	cases := []struct {
		name         string
		query        string
		wantRejected bool
	}{
		{"no cluster param", "", false},
		{"explicit local cluster", "?cluster=local", false},
		{"blank cluster param trims to empty", "?cluster=%20%20", false},
		{"remote cluster", "?cluster=remote-1", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/servers/alpha/mods/ids"+tc.query, nil)
			rr := httptest.NewRecorder()
			got := rejectRemoteCluster(rr, req)
			if got != tc.wantRejected {
				t.Errorf("rejectRemoteCluster() = %v, want %v", got, tc.wantRejected)
			}
			if tc.wantRejected && rr.Code != http.StatusNotImplemented {
				t.Errorf("code = %d, want 501", rr.Code)
			}
			if tc.wantRejected && !strings.Contains(rr.Body.String(), httperr.RemoteClusterNotImplemented) {
				t.Errorf("body = %q, want it to contain %q", rr.Body.String(), httperr.RemoteClusterNotImplemented)
			}
			if !tc.wantRejected && rr.Code != http.StatusOK {
				// httptest.ResponseRecorder defaults to 200 when nothing wrote
				// a status — a pass-through must not have touched w at all.
				t.Errorf("code = %d, want 200 (untouched)", rr.Code)
			}
		})
	}
}

// TestModIDs_RejectsNonLocalCluster proves the guard is wired onto
// MountModIDs' GET and PUT — the handlers hold a bare *kube.Client (the
// LOCAL cluster only), so a non-local ?cluster= must 501 rather than
// silently acting on the local, same-named GameServer.
func TestModIDs_RejectsNonLocalCluster(t *testing.T) {
	k := fakeKubeClient(
		newIDListTemplateObj("ark"),
		newModIDsServerObj("gameplane-games", "alpha", "ark", nil),
	)
	r := mountModIDsRouter(k)

	rr := do(t, r, "GET", "/servers/alpha/mods/ids?cluster=remote-1", nil)
	if rr.Code != http.StatusNotImplemented {
		t.Errorf("GET: got %d, want 501: %s", rr.Code, rr.Body)
	}
	if !strings.Contains(rr.Body.String(), httperr.RemoteClusterNotImplemented) {
		t.Errorf("GET: body = %q, want it to contain %q", rr.Body.String(), httperr.RemoteClusterNotImplemented)
	}
	rr = do(t, r, "PUT", "/servers/alpha/mods/ids?cluster=remote-1", []ModID{{ID: "1"}})
	if rr.Code != http.StatusNotImplemented {
		t.Errorf("PUT: got %d, want 501: %s", rr.Code, rr.Body)
	}
	if !strings.Contains(rr.Body.String(), httperr.RemoteClusterNotImplemented) {
		t.Errorf("PUT: body = %q, want it to contain %q", rr.Body.String(), httperr.RemoteClusterNotImplemented)
	}
}

// TestModIDs_LocalClusterStillWorks proves the guard doesn't regress the
// single-cluster (and explicit-local) case.
func TestModIDs_LocalClusterStillWorks(t *testing.T) {
	k := fakeKubeClient(
		newIDListTemplateObj("ark"),
		newModIDsServerObj("gameplane-games", "alpha", "ark", nil),
	)
	r := mountModIDsRouter(k)

	for _, q := range []string{"", "?cluster=local"} {
		rr := do(t, r, "GET", "/servers/alpha/mods/ids"+q, nil)
		if rr.Code != http.StatusOK {
			t.Errorf("query=%q: got %d, want 200: %s", q, rr.Code, rr.Body)
		}
	}
}

// TestModUpdates_RejectsNonLocalCluster is the mod-update-check twin of
// TestModIDs_RejectsNonLocalCluster.
func TestModUpdates_RejectsNonLocalCluster(t *testing.T) {
	lister := &fakeModLister{mods: nil}
	r := mountUpdatesRouter(updatesFixtureKube(), fakeSet{p: &fakeVersionsProvider{}}, lister)

	rr := do(t, r, "GET", "/servers/alpha/mods/updates?cluster=remote-1", nil)
	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("got %d, want 501: %s", rr.Code, rr.Body)
	}
	if !strings.Contains(rr.Body.String(), httperr.RemoteClusterNotImplemented) {
		t.Errorf("body = %q, want it to contain %q", rr.Body.String(), httperr.RemoteClusterNotImplemented)
	}
}

// TestModUpdates_LocalClusterStillWorks proves the guard doesn't regress
// the single-cluster (and explicit-local) case.
func TestModUpdates_LocalClusterStillWorks(t *testing.T) {
	lister := &fakeModLister{mods: nil}
	r := mountUpdatesRouter(updatesFixtureKube(), fakeSet{p: &fakeVersionsProvider{}}, lister)

	for _, q := range []string{"", "?cluster=local"} {
		rr := do(t, r, "GET", "/servers/alpha/mods/updates"+q, nil)
		if rr.Code != http.StatusOK {
			t.Errorf("query=%q: got %d, want 200: %s", q, rr.Code, rr.Body)
		}
	}
}

// kubeClientCalls returns how many API calls the fake dynamic and typed
// clients behind k have recorded.
func kubeClientCalls(t *testing.T, k *kube.Client) int {
	t.Helper()
	dyn, ok := k.Dynamic.(*dynamicfake.FakeDynamicClient)
	if !ok {
		t.Fatalf("dynamic client is %T, want *fake.FakeDynamicClient", k.Dynamic)
	}
	typed, ok := k.Typed.(*kubefake.Clientset)
	if !ok {
		t.Fatalf("typed client is %T, want *fake.Clientset", k.Typed)
	}
	return len(dyn.Actions()) + len(typed.Actions())
}

type modTestRoute struct {
	method, path string
	body         any
}

// registryRoutes is every route MountRegistry registers, with sample path
// values and a request body where the route takes one.
var registryRoutes = []modTestRoute{
	{http.MethodGet, "/servers/alpha/mods/registry/providers", nil},
	{http.MethodGet, "/servers/alpha/mods/registry/search", nil},
	{http.MethodGet, "/servers/alpha/mods/registry/projects/p1/versions", nil},
	{http.MethodGet, "/servers/alpha/mods/registry/projects/p1/modpack", nil},
	{http.MethodPost, "/servers/alpha/modpack", map[string]any{"ref": "pack"}},
}

// registryHomeClient returns a fake home-cluster client holding a template
// that declares an env-mode modpack provider and a server "alpha" that uses
// it, so every MountRegistry route succeeds against the home cluster.
func registryHomeClient() *kube.Client {
	versions := []any{map[string]any{"id": "1.21.4-fabric", "loader": "fabric", "gameVersion": "1.21.4", "default": true}}
	provider := map[string]any{
		"provider": "modrinth",
		"modpacks": map[string]any{"refEnv": "MODRINTH_MODPACK"},
	}
	return fakeKubeClient(
		newTemplateObj("minecraft", provider, versions),
		serverWithVersion(scope.DefaultNamespace, "alpha", "minecraft", ""),
	)
}

// TestRegistry_ServesHomeClusterOnly checks that every MountRegistry route
// answers 501 for a non-local ?cluster= selector without any call to the
// home-cluster client, and still serves the home cluster.
func TestRegistry_ServesHomeClusterOnly(t *testing.T) {
	for _, rt := range registryRoutes {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			k := registryHomeClient()
			r := mountRegistryRouter(k, fakeSet{p: &fakeProvider{}})

			rr := do(t, r, rt.method, rt.path+"?cluster=remote-1", rt.body)
			if rr.Code != http.StatusNotImplemented {
				t.Fatalf("cluster=remote-1: got %d, want 501: %s", rr.Code, rr.Body)
			}
			if !strings.Contains(rr.Body.String(), httperr.RemoteClusterNotImplemented) {
				t.Errorf("cluster=remote-1: body = %q, want it to contain %q", rr.Body.String(), httperr.RemoteClusterNotImplemented)
			}
			if n := kubeClientCalls(t, k); n != 0 {
				t.Errorf("cluster=remote-1: home client saw %d API calls, want 0", n)
			}

			for _, q := range []string{"", "?cluster=local"} {
				rr := do(t, r, rt.method, rt.path+q, rt.body)
				if rr.Code != http.StatusOK {
					t.Errorf("query=%q: got %d, want 200: %s", q, rr.Code, rr.Body)
				}
			}
		})
	}
}

// TestCaptureDownload_RequiresRemoteGateway verifies fail-closed remote routing
// when no gateway is configured, with no home namesake fallback.
func TestCaptureDownload_RequiresRemoteGateway(t *testing.T) {
	const server, captureID = "dl-home-only", "cap-home-only"
	fixtures := func() *kube.Client {
		return fakeCaptureClient(
			newCaptureServerObj(server, true),
			newCompletedNetworkCapture(t, captureID, server, time.Minute, 86400),
		)
	}
	home, remote := fixtures(), fixtures()
	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.Set(scope.DefaultCluster, home)
	reg.Set("remote-1", remote)
	store := newTestStore(t)
	r := chi.NewRouter()
	MountCapture(r, reg, audit.New(store), captureTestCfg, "", "", "")

	path := "/servers/" + server + ":capture-file?id=" + captureID
	rr := do(t, r, http.MethodGet, path+"&cluster=remote-1", nil)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("cluster=remote-1: got %d, want 503: %s", rr.Code, rr.Body)
	}
	if n := kubeClientCalls(t, home); n != 0 {
		t.Fatalf("remote download touched home client: %d", n)
	}
	if n := kubeClientCalls(t, remote); n == 0 {
		t.Fatal("remote client was not resolved")
	}
	var (
		reason string
		status int
	)
	if err := store.DB.QueryRowContext(t.Context(),
		`SELECT COALESCE(reason, ''), status FROM audit_events WHERE target = ? ORDER BY id DESC LIMIT 1`,
		server+":"+captureID).Scan(&reason, &status); err != nil {
		t.Fatalf("read audit row: %v", err)
	}
	if reason != "download_failed" || status != http.StatusServiceUnavailable {
		t.Errorf("audit row = (%q, %d), want (%q, %d)", reason, status, "download_failed", http.StatusServiceUnavailable)
	}

	// The home cluster is still served. With no mTLS client configured in
	// this unit test, the request reaches the proxy step and answers 503.
	rr = do(t, r, http.MethodGet, path, nil)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("home cluster: got %d, want 503 (proxy step reached): %s", rr.Code, rr.Body)
	}
}

// routeParam matches a chi route parameter such as {name}.
var routeParam = regexp.MustCompile(`\{[^}]*\}`)

// mountHomeClientRoutes mounts on r every handler that api/cmd/main.go
// builds with the bare home-cluster client instead of the cluster registry.
// api/cmd/mounts_test.go keeps this list in step with main.go.
func mountHomeClientRoutes(t *testing.T, r chi.Router, home *kube.Client, reg *kube.Registry) {
	t.Helper()
	const controlNS = "gameplane-system"
	store := newTestStore(t)
	MountNotifications(r, notify.New(store, home, controlNS), home, controlNS)
	MountAuthProviderSecrets(r, home, controlNS)
	MountClusterActions(r, home, true, "")
	MountClusters(r, reg, home, controlNS)
	MountSystemLogs(r, home, controlNS)
	MountRegistrySecrets(r, home, controlNS)
}

// TestHomeClientMounts_ServeHomeClusterOnly calls every route of the
// handlers that hold the home-cluster client, through the RBAC middleware,
// as a user whose only grant is on a registered non-local cluster, with
// ?cluster= naming that cluster. Server-scoped routes pass the namespaced
// permission check for that cluster and must answer 501 from the handler;
// the other routes need a cluster-wide grant and answer 403. Registry-backed
// Pod streams are mounted alongside them and must query only the remote
// client. Discovery may list central registration metadata; every other request
// leaves the home-cluster client untouched.
func TestHomeClientMounts_ServeHomeClusterOnly(t *testing.T) {
	const remote = "remote-1"
	home := fakeKubeClient()
	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.Set(scope.DefaultCluster, home)
	remoteClient := fakeKubeClient()
	remoteClient.Config = &rest.Config{}
	reg.Set(remote, remoteClient)

	r := chi.NewRouter()
	r.Use(rbac.Middleware(reg))
	mountHomeClientRoutes(t, r, home, reg)
	MountModulesWithRegistry(r, reg, "gameplane-system")
	MountCluster(r, reg, newTestStore(t), "test", true, "")
	MountRegistryWithRegistry(r, reg, fakeSet{p: &fakeProvider{}})
	MountModIDsWithRegistry(r, reg)
	ws.Mount(r, reg, "", "", "")

	remoteOnly := &auth.User{
		ID:       42,
		Username: "remote-only",
		Perms: map[string]map[string]map[string]struct{}{
			remote: {scope.DefaultNamespace: {"*": {}}},
		},
	}

	routes := 0
	err := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		routes++
		path := routeParam.ReplaceAllString(route, "alpha")
		want := http.StatusForbidden
		discovery := method == http.MethodGet && strings.TrimSuffix(path, "/") == "/clusters"
		if discovery {
			want = http.StatusOK
		}
		if strings.HasPrefix(path, "/servers/") || strings.HasPrefix(path, "/ws/servers/") {
			want = http.StatusNotImplemented
		}
		attach := path == "/ws/servers/alpha/console-pty"
		// Only the registry and ID-list handlers above use the selected client.
		// ws.Mount has no gateway options here, so install/upload keep their
		// legacy 501 guard rather than performing a remote target lookup.
		selectedModRoute := strings.HasPrefix(path, "/servers/alpha/mods/registry/") ||
			path == "/servers/alpha/mods/ids" || path == "/servers/alpha/modpack"
		if attach || selectedModRoute {
			// The registered remote cluster has no alpha GameServer.
			want = http.StatusNotFound
		} else if path == "/ws/servers/alpha/logs/pod" {
			// Pod logs preserve their upgrade-before-lookup retry protocol.
			// Full remote streaming is covered in ws/multicluster_test.go.
			want = http.StatusUpgradeRequired
		}
		before := kubeClientCalls(t, home)
		remoteBefore := kubeClientCalls(t, remoteClient)

		req := httptest.NewRequestWithContext(auth.WithUser(t.Context(), remoteOnly), method, path+"?cluster="+remote, nil)
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)

		if rr.Code != want {
			t.Errorf("%s %s?cluster=%s: got %d, want %d: %s", method, path, remote, rr.Code, want, rr.Body)
		}
		if want == http.StatusNotImplemented && !strings.Contains(rr.Body.String(), httperr.RemoteClusterNotImplemented) {
			t.Errorf("%s %s?cluster=%s: body = %q, want it to contain %q", method, path, remote, rr.Body.String(), httperr.RemoteClusterNotImplemented)
		}
		wantHomeCalls := 0
		if discovery {
			// Discovery may read central registration metadata, never inventory
			// or credentials. Namespace grants still cannot read node inventory.
			wantHomeCalls = 1
			actions := home.Dynamic.(*dynamicfake.FakeDynamicClient).Actions()
			last := actions[len(actions)-1]
			if last.GetVerb() != "list" || last.GetResource() != kube.GVRCluster {
				t.Errorf("discovery accessed %s %s, want only list clusters", last.GetVerb(), last.GetResource())
			}
		}
		if n := kubeClientCalls(t, home) - before; n != wantHomeCalls {
			t.Errorf("%s %s?cluster=%s: home client saw %d API calls, want %d", method, path, remote, n, wantHomeCalls)
		}
		if (attach || selectedModRoute) && kubeClientCalls(t, remoteClient) == remoteBefore {
			t.Errorf("%s %s?cluster=%s: remote client saw no lookup", method, path, remote)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk routes: %v", err)
	}
	if routes == 0 {
		t.Fatal("no routes were mounted")
	}
}
