package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/rbac"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
	"github.com/GameplanePanel/gameplane/api/internal/ws"
)

type captureProbeFunc func(context.Context, string, string, string) (ws.CaptureGatewayCapabilities, error)

func (f captureProbeFunc) Capabilities(ctx context.Context, cluster, namespace, name string) (ws.CaptureGatewayCapabilities, error) {
	return f(ctx, cluster, namespace, name)
}

func TestServerCapabilities_SelectedSiteAndCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name, cluster string
		localEnabled  bool
		remote        ws.CaptureGatewayCapabilities
		err           error
		want          captureCapabilities
	}{
		{"local disabled retains downloads", "local", false, ws.CaptureGatewayCapabilities{}, nil, captureCapabilities{Enabled: false, Files: true, State: "ready"}},
		{"local enabled", "local", true, ws.CaptureGatewayCapabilities{}, nil, captureCapabilities{Enabled: true, Files: true, State: "ready"}},
		{"remote enabled despite central disabled", "remote", false, ws.CaptureGatewayCapabilities{Protocol: "v1", TargetUID: true, CaptureFiles: true, CaptureEnabled: true}, nil, captureCapabilities{Enabled: true, Files: true, State: "ready"}},
		{"remote disabled despite central enabled", "remote", true, ws.CaptureGatewayCapabilities{Protocol: "v1", TargetUID: true, CaptureFiles: true}, nil, captureCapabilities{Enabled: false, Files: true, State: "ready"}},
		{"old gateway", "remote", true, ws.CaptureGatewayCapabilities{Protocol: "v1", TargetUID: true}, nil, captureCapabilities{State: "unsupported"}},
		{"unknown protocol", "remote", true, ws.CaptureGatewayCapabilities{Protocol: "v9", TargetUID: true, CaptureFiles: true}, nil, captureCapabilities{State: "unsupported"}},
		{"no identity support", "remote", true, ws.CaptureGatewayCapabilities{Protocol: "v1", CaptureFiles: true}, nil, captureCapabilities{State: "unsupported"}},
		{"gateway down", "remote", true, ws.CaptureGatewayCapabilities{}, errors.New("private endpoint detail"), captureCapabilities{State: "unavailable"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg := clusterTestRegistry(fleetTestClient(fleetObject("GameServer", scope.DefaultNamespace, "same", "local-uid")))
			reg.Set("remote", fleetTestClient(fleetObject("GameServer", scope.DefaultNamespace, "same", "remote-uid")))
			calls := 0
			probe := captureProbeFunc(func(_ context.Context, cluster, namespace, name string) (ws.CaptureGatewayCapabilities, error) {
				calls++
				if cluster != "remote" || namespace != scope.DefaultNamespace || name != "same" {
					t.Fatalf("wrong probe target: %s/%s/%s", cluster, namespace, name)
				}
				return tc.remote, tc.err
			})
			r := chi.NewRouter()
			r.Use(rbac.Middleware(reg))
			MountServerCapabilities(r, reg, probe, CaptureConfig{FeatureEnabled: tc.localEnabled})
			rr := inventoryRequest(t, r, inventoryUser(tc.cluster, scope.DefaultNamespace, "servers:read"), http.MethodGet, "/servers/same/capabilities?cluster="+tc.cluster)
			if rr.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body)
			}
			var got serverCapabilities
			if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Capture != tc.want || got.Target.Cluster != tc.cluster || got.Target.UID != tc.cluster+"-uid" {
				t.Fatalf("unexpected capabilities: %+v", got)
			}
			if (tc.cluster == "remote" && calls != 1) || (tc.cluster == "local" && calls != 0) {
				t.Fatalf("probe calls=%d", calls)
			}
		})
	}
}

func TestServerCapabilities_DeniedOrMissingTargetNeverProbesGateway(t *testing.T) {
	for _, tc := range []struct {
		name, grant, path string
		status            int
	}{
		{"wrong cluster grant", "local", "/servers/same/capabilities?cluster=remote", http.StatusForbidden},
		{"namespace outside installation allowlist", "remote", "/servers/same/capabilities?cluster=remote&namespace=outside", http.StatusBadRequest},
		{"missing server", "remote", "/servers/missing/capabilities?cluster=remote", http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg := clusterTestRegistry(fleetTestClient())
			reg.Set("remote", fleetTestClient(fleetObject("GameServer", scope.DefaultNamespace, "same", "remote-uid")))
			probe := captureProbeFunc(func(context.Context, string, string, string) (ws.CaptureGatewayCapabilities, error) {
				t.Fatal("unauthorized or missing server probed gateway")
				return ws.CaptureGatewayCapabilities{}, nil
			})
			r := chi.NewRouter()
			r.Use(rbac.Middleware(reg))
			MountServerCapabilities(r, reg, probe, CaptureConfig{})
			rr := inventoryRequest(t, r, inventoryUser(tc.grant, scope.DefaultNamespace, "servers:read"), http.MethodGet, tc.path)
			if rr.Code != tc.status {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body)
			}
		})
	}
}

func TestServerCapabilities_UnconfiguredGatewayIsUnavailable(t *testing.T) {
	reg := clusterTestRegistry(fleetTestClient())
	reg.Set("remote", fleetTestClient(fleetObject("GameServer", scope.DefaultNamespace, "same", "remote-uid")))
	r := chi.NewRouter()
	r.Use(rbac.Middleware(reg))
	MountServerCapabilities(r, reg, nil, CaptureConfig{FeatureEnabled: true})
	rr := inventoryRequest(t, r, inventoryUser("remote", scope.DefaultNamespace, "servers:read"), http.MethodGet, "/servers/same/capabilities?cluster=remote")
	var got serverCapabilities
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Capture.State != "unavailable" || got.Capture.Enabled || got.Capture.Files {
		t.Fatalf("capabilities=%+v", got)
	}
}

var _ captureCapabilitiesProbe = (*ws.CaptureGatewayClient)(nil)

func TestServerCapabilities_RejectsReplacementAfterOwnerAuthorization(t *testing.T) {
	reg := modClusterClients(modClusterFixture("local"), modClusterFixture("remote"))
	previous := serverWithVersion(scope.DefaultNamespace, "alpha", "same-template", "selected")
	previous.SetUID("previous-uid")
	previous.SetAnnotations(map[string]string{"gameplane.local/owner-id": "42"})
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: 42})))
		})
	})
	r.Use(rbac.Middleware(modAuthorizationSnapshot{previous}))
	MountServerCapabilities(r, reg, captureProbeFunc(func(context.Context, string, string, string) (ws.CaptureGatewayCapabilities, error) {
		t.Fatal("replacement server must not probe the gateway")
		return ws.CaptureGatewayCapabilities{}, nil
	}), CaptureConfig{})
	rr := do(t, r, http.MethodGet, "/servers/alpha/capabilities?cluster=remote", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body)
	}
}

func TestServerCapabilities_ReportsSelectedSiteDefaults(t *testing.T) {
	reg := clusterTestRegistry(fleetTestClient(fleetObject("GameServer", scope.DefaultNamespace, "same", "local-uid")))
	reg.Set("remote", fleetTestClient(fleetObject("GameServer", scope.DefaultNamespace, "same", "remote-uid")))
	probe := captureProbeFunc(func(context.Context, string, string, string) (ws.CaptureGatewayCapabilities, error) {
		return ws.CaptureGatewayCapabilities{
			Protocol: "v1", TargetUID: true, CaptureFiles: true, CaptureEnabled: true,
			DefaultRetentionSeconds: 1200, MaxRetentionSeconds: 7200,
			DefaultMaxDurationSecs: 90, DefaultMaxSizeBytes: 32 << 20,
		}, nil
	})
	r := chi.NewRouter()
	r.Use(rbac.Middleware(reg))
	MountServerCapabilities(r, reg, probe, CaptureConfig{
		FeatureEnabled: true, DefaultRetentionSeconds: 600, MaxRetentionSeconds: 3600,
		DefaultMaxDurationSecs: 30, DefaultMaxSizeBytes: 16 << 20,
	})
	for _, tc := range []struct {
		cluster string
		want    captureCapabilities
	}{
		{"local", captureCapabilities{Enabled: true, Files: true, State: "ready", DefaultRetentionSeconds: 600, MaxRetentionSeconds: 3600, DefaultMaxDurationSecs: 30, DefaultMaxSizeBytes: 16 << 20}},
		{"remote", captureCapabilities{Enabled: true, Files: true, State: "ready", DefaultRetentionSeconds: 1200, MaxRetentionSeconds: 7200, DefaultMaxDurationSecs: 90, DefaultMaxSizeBytes: 32 << 20}},
	} {
		t.Run(tc.cluster, func(t *testing.T) {
			rr := inventoryRequest(t, r, inventoryUser(tc.cluster, scope.DefaultNamespace, "servers:read"), http.MethodGet, "/servers/same/capabilities?cluster="+tc.cluster)
			var got serverCapabilities
			if rr.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body)
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Capture != tc.want {
				t.Fatalf("selected site settings=%+v, want %+v", got.Capture, tc.want)
			}
		})
	}
}
