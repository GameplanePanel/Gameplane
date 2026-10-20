package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
)

// wantShareNotFoundBody is the exact byte-for-byte body every "invalid"
// public share response must produce: unknown, expired, revoked, and
// canStart=false all collapse onto this single response so probing a
// token reveals nothing about why it failed.
var wantShareNotFoundBody = []byte(`{"error":"not found"}` + "\n")

// ptrTime returns a pointer to t, for passing an absolute expiry to
// store.CreateShareLink (which takes *time.Time; nil = never expires).
func ptrTime(t time.Time) *time.Time { return &t }

// mountSharesRouter wires both the authenticated (owner) and public share
// routes onto one router, mirroring how api/cmd/main.go mounts them (in
// different middleware groups, but against the same reg/store).
func mountSharesRouter(reg *kube.Registry, store *db.Store) http.Handler {
	r := chi.NewRouter()
	MountShareLinks(r, reg, store)
	MountPublicShares(r, reg, store, auth.ShareLimiter)
	return r
}

// insertShareTestUser inserts a minimal user row and returns its ID, so
// created_by / the owner-id annotation can reference a real user.
func insertShareTestUser(t *testing.T, store *db.Store, username string) int64 {
	t.Helper()
	res, err := store.DB.ExecContext(t.Context(), `INSERT INTO users(username, role) VALUES (?, ?)`, username, "admin")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}
	return id
}

// newShareTestServer builds a GameServer unstructured object in the default
// namespace, owned by ownerID, with one public endpoint and a player count.
func newShareTestServer(name string, ownerID int64) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata": map[string]any{
			"name":      name,
			"namespace": "gameplane-games",
			"annotations": map[string]any{
				"gameplane.local/owner-id": strconv.FormatInt(ownerID, 10),
			},
		},
		"status": map[string]any{
			"phase": "Running",
			"endpoints": []any{
				map[string]any{
					"name":     "game",
					"host":     "play.example.com",
					"port":     int64(25565),
					"private":  false,
					"protocol": "TCP",
				},
			},
			"agent": map[string]any{
				"playersOnline": int64(3),
			},
		},
	}}
}

// newShareTestServerPrivateOnly is like newShareTestServer but its only
// endpoint is a private/tailnet one, which must never be handed to an
// anonymous caller.
func newShareTestServerPrivateOnly(name string, ownerID int64) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata": map[string]any{
			"name":      name,
			"namespace": "gameplane-games",
			"annotations": map[string]any{
				"gameplane.local/owner-id": strconv.FormatInt(ownerID, 10),
			},
		},
		"status": map[string]any{
			"phase": "Running",
			"endpoints": []any{
				map[string]any{
					"name":     "tailscale",
					"host":     "gameplane-srv.user.ts.net",
					"port":     int64(25565),
					"private":  true,
					"protocol": "TCP",
				},
			},
		},
	}}
}

// shareReq issues an HTTP request against h, optionally carrying an
// authenticated user in the context and a distinct RemoteAddr (so each
// test gets its own bucket in the shared, package-level auth.ShareLimiter).
func shareReq(t *testing.T, h http.Handler, method, path string, body any, user *auth.User, remoteAddr string) (int, []byte) {
	t.Helper()
	var buf io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		buf = bytes.NewReader(b)
	}
	req := httptest.NewRequestWithContext(t.Context(), method, path, buf)
	if remoteAddr != "" {
		req.RemoteAddr = remoteAddr
	}
	if user != nil {
		req = req.WithContext(auth.WithUser(req.Context(), user))
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr.Code, rr.Body.Bytes()
}

// TestShareCreateReturnsTokenOnce verifies that the token is returned in the
// create response but never in list responses.
func TestShareCreateReturnsTokenOnce(t *testing.T) {
	store := newTestStore(t)
	ownerID := insertShareTestUser(t, store, "owner-create")
	reg := kube.NewRegistry("local")
	reg.Set("local", fakeKubeClient(newShareTestServer("srv-create", ownerID)))
	h := mountSharesRouter(reg, store)
	owner := &auth.User{ID: ownerID, Username: "owner-create", Role: "admin"}

	status, body := shareReq(t, h, "POST", "/servers/srv-create:shares", map[string]any{"canStart": false, "expiresIn": "1h"}, owner, "203.0.113.1:1")
	if status != http.StatusOK {
		t.Fatalf("create status = %d, want 200; body=%s", status, body)
	}
	var created map[string]any
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("unmarshal create response: %v; body=%s", err, body)
	}
	token, _ := created["token"].(string)
	if token == "" {
		t.Fatalf("create response missing non-empty token; body=%s", body)
	}

	status, body = shareReq(t, h, "GET", "/servers/srv-create:shares", nil, owner, "203.0.113.1:1")
	if status != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body=%s", status, body)
	}
	var listed []map[string]any
	if err := json.Unmarshal(body, &listed); err != nil {
		t.Fatalf("unmarshal list response: %v; body=%s", err, body)
	}
	if len(listed) != 1 {
		t.Fatalf("list length = %d, want 1; body=%s", len(listed), body)
	}
	for i, item := range listed {
		if v, ok := item["token"]; ok {
			t.Fatalf("list item %d carries a token field (%v); list must never include it", i, v)
		}
	}
}

// TestShareResolveValidToken verifies that a valid token resolves successfully
// and returns the server's public view, going through the real handler and
// cluster resolution (the path that a `reg.Get("")` bug would 404 on).
func TestShareResolveValidToken(t *testing.T) {
	store := newTestStore(t)
	ownerID := insertShareTestUser(t, store, "owner-resolve")
	reg := kube.NewRegistry("local")
	reg.Set("local", fakeKubeClient(newShareTestServer("srv-resolve", ownerID)))
	h := mountSharesRouter(reg, store)
	owner := &auth.User{ID: ownerID, Username: "owner-resolve", Role: "admin"}

	status, body := shareReq(t, h, "POST", "/servers/srv-resolve:shares", map[string]any{"canStart": false, "expiresIn": "1h"}, owner, "203.0.113.2:1")
	if status != http.StatusOK {
		t.Fatalf("create status = %d, want 200; body=%s", status, body)
	}
	var created map[string]any
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("unmarshal create response: %v", err)
	}
	token, _ := created["token"].(string)
	if token == "" {
		t.Fatalf("missing token in create response; body=%s", body)
	}

	status, body = shareReq(t, h, "GET", "/shares/"+token, nil, nil, "203.0.113.3:1")
	if status != http.StatusOK {
		t.Fatalf("resolve status = %d, want 200; body=%s", status, body)
	}
	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal resolve response: %v; body=%s", err, body)
	}
	if resp["serverName"] != "srv-resolve" {
		t.Fatalf("serverName = %v, want srv-resolve; body=%s", resp["serverName"], body)
	}
	if resp["status"] != "Running" {
		t.Fatalf("status = %v, want Running; body=%s", resp["status"], body)
	}
	addr, ok := resp["address"].(map[string]any)
	if !ok {
		t.Fatalf("address missing or wrong type; body=%s", body)
	}
	if addr["host"] != "play.example.com" {
		t.Fatalf("address.host = %v, want play.example.com; body=%s", addr["host"], body)
	}
	if pf, ok := addr["port"].(float64); !ok || int32(pf) != 25565 {
		t.Fatalf("address.port = %v, want 25565; body=%s", addr["port"], body)
	}
	if pl, ok := resp["playersOnline"].(float64); !ok || int32(pl) != 3 {
		t.Fatalf("playersOnline = %v, want 3; body=%s", resp["playersOnline"], body)
	}
}

// TestShareResolvePrivateEndpointOmitted verifies, through the full public
// handler (not just the getPublicAddress helper directly), that a
// private/tailnet-only endpoint is never handed to an anonymous caller.
func TestShareResolvePrivateEndpointOmitted(t *testing.T) {
	store := newTestStore(t)
	ownerID := insertShareTestUser(t, store, "owner-private")
	reg := kube.NewRegistry("local")
	reg.Set("local", fakeKubeClient(newShareTestServerPrivateOnly("srv-private", ownerID)))
	h := mountSharesRouter(reg, store)
	owner := &auth.User{ID: ownerID, Username: "owner-private", Role: "admin"}

	_, body := shareReq(t, h, "POST", "/servers/srv-private:shares", map[string]any{"canStart": false, "expiresIn": "1h"}, owner, "203.0.113.4:1")
	var created map[string]any
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("unmarshal create response: %v; body=%s", err, body)
	}
	token, _ := created["token"].(string)
	if token == "" {
		t.Fatalf("missing token; body=%s", body)
	}

	status, body := shareReq(t, h, "GET", "/shares/"+token, nil, nil, "203.0.113.5:1")
	if status != http.StatusOK {
		t.Fatalf("resolve status = %d, want 200; body=%s", status, body)
	}
	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, body)
	}
	if _, ok := resp["address"]; ok {
		t.Fatalf("address must be omitted for a private-only endpoint, got %v; body=%s", resp["address"], body)
	}
}

// TestShareResolveInvalidToken verifies that unknown, expired, and revoked
// tokens are indistinguishable: identical status code AND identical body.
func TestShareResolveInvalidToken(t *testing.T) {
	store := newTestStore(t)
	ownerID := insertShareTestUser(t, store, "owner-invalid")
	reg := kube.NewRegistry("local")
	reg.Set("local", fakeKubeClient(newShareTestServer("srv-invalid", ownerID)))
	h := mountSharesRouter(reg, store)

	ctx := context.Background()

	// Expired: mint a valid link, then rewrite its expiry into the past
	// (CreateShareLink itself refuses to mint an already-expired link).
	expiredToken, expiredLink, err := store.CreateShareLink(ctx, "local", "gameplane-games", "srv-invalid", ownerID, false, ptrTime(time.Now().UTC().Add(time.Hour)))
	if err != nil {
		t.Fatalf("create expired link: %v", err)
	}
	if _, err := store.DB.ExecContext(ctx, `UPDATE share_links SET expires_at = ? WHERE id = ?`,
		time.Now().UTC().Add(-time.Hour).Format(time.RFC3339), expiredLink.ID); err != nil {
		t.Fatalf("force-expire link: %v", err)
	}

	// Revoked: mint a valid link, then revoke it.
	revokedToken, revokedLink, err := store.CreateShareLink(ctx, "local", "gameplane-games", "srv-invalid", ownerID, false, ptrTime(time.Now().UTC().Add(time.Hour)))
	if err != nil {
		t.Fatalf("create link to revoke: %v", err)
	}
	if err := store.RevokeShareLink(ctx, "local", revokedLink.Namespace, revokedLink.ServerName, revokedLink.ID); err != nil {
		t.Fatalf("revoke link: %v", err)
	}

	cases := map[string]string{
		"unknown": "this-token-was-never-issued-abc123",
		"expired": expiredToken,
		"revoked": revokedToken,
	}

	var firstStatus int
	var firstBody []byte
	first := true
	for name, token := range cases {
		status, body := shareReq(t, h, "GET", "/shares/"+token, nil, nil, "203.0.113.6:1")
		if status != http.StatusNotFound {
			t.Fatalf("%s: status = %d, want 404; body=%s", name, status, body)
		}
		if !bytes.Equal(body, wantShareNotFoundBody) {
			t.Fatalf("%s: body = %q, want %q", name, body, wantShareNotFoundBody)
		}
		if first {
			firstStatus, firstBody = status, body
			first = false
			continue
		}
		if status != firstStatus || !bytes.Equal(body, firstBody) {
			t.Fatalf("%s: response (%d, %q) differs from baseline (%d, %q)", name, status, body, firstStatus, firstBody)
		}
	}
}

// TestShareStartCanStartGate verifies that canStart=false returns the same
// 404 as an invalid token (not 403, not a distinguishable body), while
// canStart=true wakes the server by stamping the idle-wake annotation.
func TestShareStartCanStartGate(t *testing.T) {
	store := newTestStore(t)
	ownerID := insertShareTestUser(t, store, "owner-start")
	reg := kube.NewRegistry("local")
	k := fakeKubeClient(newShareTestServer("srv-start", ownerID))
	reg.Set("local", k)
	h := mountSharesRouter(reg, store)
	ctx := context.Background()

	// canStart=false must 404, identically to an invalid token.
	noStartToken, _, err := store.CreateShareLink(ctx, "local", "gameplane-games", "srv-start", ownerID, false, ptrTime(time.Now().UTC().Add(time.Hour)))
	if err != nil {
		t.Fatalf("create canStart=false link: %v", err)
	}
	status, body := shareReq(t, h, "POST", "/shares/"+noStartToken+"/start", nil, nil, "203.0.113.7:1")
	if status != http.StatusNotFound {
		t.Fatalf("canStart=false: status = %d, want 404; body=%s", status, body)
	}
	if !bytes.Equal(body, wantShareNotFoundBody) {
		t.Fatalf("canStart=false: body = %q, want %q (must be indistinguishable from an invalid token)", body, wantShareNotFoundBody)
	}

	// canStart=true must wake the server: 202 + idle-wake annotation stamped,
	// and the API must not touch spec.suspend (the operator owns that).
	startToken, _, err := store.CreateShareLink(ctx, "local", "gameplane-games", "srv-start", ownerID, true, ptrTime(time.Now().UTC().Add(time.Hour)))
	if err != nil {
		t.Fatalf("create canStart=true link: %v", err)
	}
	status, body = shareReq(t, h, "POST", "/shares/"+startToken+"/start", nil, nil, "203.0.113.8:1")
	if status != http.StatusAccepted {
		t.Fatalf("canStart=true: status = %d, want 202; body=%s", status, body)
	}

	obj, err := k.Dynamic.Resource(kube.GVRs["servers"]).Namespace("gameplane-games").Get(ctx, "srv-start", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("fetch server after start: %v", err)
	}
	ann := obj.GetAnnotations()
	if ann[idleWakeRequestedAnnotation] == "" {
		t.Fatalf("expected %s annotation to be stamped, got annotations %+v", idleWakeRequestedAnnotation, ann)
	}
	if _, found, _ := unstructured.NestedString(obj.Object, "spec", "suspend"); found {
		t.Fatalf("API must not write spec.suspend directly; found value %v", obj.Object["spec"])
	}
}

// TestSharePublicPayloadSafe verifies that the public response contains no
// forbidden fields (no namespace, cluster, version, owner, etc.).
func TestSharePublicPayloadSafe(t *testing.T) {
	t.Parallel()
	// Create a mock GameServer object with all possible fields.
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "gameplane.local/v1alpha1",
			"kind":       "GameServer",
			"metadata": map[string]interface{}{
				"name":      "test-server",
				"namespace": "gameplane-games",
				"annotations": map[string]interface{}{
					"gameplane.local/owner-id": "42",
				},
			},
			"spec": map[string]interface{}{
				"templateRef": map[string]interface{}{
					"name": "minecraft-java",
				},
			},
			"status": map[string]interface{}{
				"phase": "Running",
				"endpoints": []interface{}{
					map[string]interface{}{
						"name":     "game",
						"host":     "192.0.2.1",
						"port":     float64(25565), // JSON unmarshals numbers as float64
						"private":  false,
						"protocol": "TCP",
					},
				},
				"agent": map[string]interface{}{
					"playersOnline": float64(5),
					"playersMax":    float64(20),
					"gameVersion":   "1.20.1",
				},
			},
		},
	}

	// Extract the public response fields.
	status := getPhase(obj)
	address := getPublicAddress(obj)
	players := getPlayersOnline(obj)

	// Verify status is present and safe.
	if status != "Running" {
		t.Fatalf("phase: want Running, got %s", status)
	}

	// Verify address is present and safe.
	if address == nil {
		t.Fatal("address must not be nil")
	}
	if address.Host != "192.0.2.1" || address.Port != 25565 {
		t.Fatalf("address: got %+v, want host=192.0.2.1 port=25565", address)
	}

	// Verify players count is present.
	if players == nil || *players != 5 {
		t.Fatalf("players: got %v, want 5", players)
	}

	// Marshal to JSON and verify no forbidden keys are present.
	resp := sharePublicResp{
		ServerName:    "test-server",
		Status:        status,
		Address:       address,
		PlayersOnline: players,
	}
	body, _ := json.Marshal(resp)
	var m map[string]interface{}
	json.Unmarshal(body, &m)

	// Forbidden fields that must not appear in the response.
	forbidden := []string{"namespace", "cluster", "version", "owner", "template", "gameVersion", "playersMax"}
	for _, key := range forbidden {
		if _, ok := m[key]; ok {
			t.Fatalf("forbidden field %q found in response", key)
		}
	}

	// The response should only have the safe fields.
	expectedKeys := map[string]bool{
		"serverName":    true,
		"status":        true,
		"address":       true,
		"playersOnline": true,
	}
	for key := range m {
		if !expectedKeys[key] {
			t.Fatalf("unexpected key %q in response", key)
		}
	}
}

// TestSharePrivateEndpointNotReturned verifies that private/tailnet-only endpoints
// are never returned to anonymous callers.
func TestSharePrivateEndpointNotReturned(t *testing.T) {
	t.Parallel()
	// Create a mock server with only private endpoints.
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"status": map[string]interface{}{
				"phase": "Running",
				"endpoints": []interface{}{
					map[string]interface{}{
						"name":     "tailscale",
						"host":     "gameplane-srv.user.ts.net",
						"port":     int64(25565),
						"private":  true,
						"protocol": "TCP",
					},
				},
			},
		},
	}

	// getPublicAddress should return nil because the only endpoint is private.
	address := getPublicAddress(obj)
	if address != nil {
		t.Fatalf("expected nil address for private-only endpoints, got %+v", address)
	}

	// Now add a public endpoint and verify it's returned.
	obj.Object["status"].(map[string]interface{})["endpoints"] = []interface{}{
		map[string]interface{}{
			"name":     "tailscale",
			"host":     "gameplane-srv.user.ts.net",
			"port":     int64(25565),
			"private":  true,
			"protocol": "TCP",
		},
		map[string]interface{}{
			"name":           "tunnel",
			"host":           "play.example.com",
			"port":           int64(25565),
			"private":        false,
			"protocol":       "TCP",
			"tunnelProvider": "frp",
		},
	}

	address = getPublicAddress(obj)
	if address == nil {
		t.Fatal("expected address for mixed endpoints, got nil")
	}
	if address.Host != "play.example.com" {
		t.Fatalf("expected public endpoint, got %s", address.Host)
	}
}

// TestShareRevokeOwnerOnly verifies that only the owner can revoke a share
// link, and that revocation actually takes effect: the token stops
// resolving afterwards.
func TestShareRevokeOwnerOnly(t *testing.T) {
	store := newTestStore(t)
	ownerID := insertShareTestUser(t, store, "owner-revoke")
	strangerID := insertShareTestUser(t, store, "stranger-revoke")
	reg := kube.NewRegistry("local")
	reg.Set("local", fakeKubeClient(newShareTestServer("srv-revoke", ownerID)))
	h := mountSharesRouter(reg, store)
	owner := &auth.User{ID: ownerID, Username: "owner-revoke", Role: "admin"}
	stranger := &auth.User{ID: strangerID, Username: "stranger-revoke", Role: "admin"}

	status, body := shareReq(t, h, "POST", "/servers/srv-revoke:shares", map[string]any{"canStart": false, "expiresIn": "1h"}, owner, "203.0.113.9:1")
	if status != http.StatusOK {
		t.Fatalf("create status = %d, want 200; body=%s", status, body)
	}
	var created map[string]any
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("unmarshal create response: %v", err)
	}
	token, _ := created["token"].(string)
	id, _ := created["id"].(string)
	if token == "" || id == "" {
		t.Fatalf("create response missing token/id; body=%s", body)
	}

	// A non-owner must not be able to revoke.
	status, _ = shareReq(t, h, "DELETE", "/servers/srv-revoke/shares/"+id, nil, stranger, "203.0.113.10:1")
	if status != http.StatusForbidden {
		t.Fatalf("non-owner revoke status = %d, want 403", status)
	}

	// The link must still resolve (revoke by a non-owner had no effect).
	status, _ = shareReq(t, h, "GET", "/shares/"+token, nil, nil, "203.0.113.11:1")
	if status != http.StatusOK {
		t.Fatalf("resolve after failed revoke: status = %d, want 200", status)
	}

	// The owner can revoke.
	status, body = shareReq(t, h, "DELETE", "/servers/srv-revoke/shares/"+id, nil, owner, "203.0.113.12:1")
	if status != http.StatusNoContent {
		t.Fatalf("owner revoke status = %d, want 204; body=%s", status, body)
	}

	// The token must stop resolving, with the same 404 body as any other
	// invalid token.
	status, body = shareReq(t, h, "GET", "/shares/"+token, nil, nil, "203.0.113.13:1")
	if status != http.StatusNotFound {
		t.Fatalf("resolve after revoke: status = %d, want 404; body=%s", status, body)
	}
	if !bytes.Equal(body, wantShareNotFoundBody) {
		t.Fatalf("resolve after revoke: body = %q, want %q", body, wantShareNotFoundBody)
	}
}

// TestShareRevokeUnknownID_404 is the F-082 regression test: revoking a
// share link id that does not exist must classify as 404 (via
// db.ErrShareLinkNotFound + httperr), not the opaque 500 the string-typed
// sentinel used to fall through to.
func TestShareRevokeUnknownID_404(t *testing.T) {
	store := newTestStore(t)
	ownerID := insertShareTestUser(t, store, "owner-revoke-404")
	reg := kube.NewRegistry("local")
	reg.Set("local", fakeKubeClient(newShareTestServer("srv-revoke-404", ownerID)))
	h := mountSharesRouter(reg, store)
	owner := &auth.User{ID: ownerID, Username: "owner-revoke-404", Role: "admin"}

	status, body := shareReq(t, h, "DELETE", "/servers/srv-revoke-404/shares/does-not-exist", nil, owner, "203.0.113.20:1")
	if status != http.StatusNotFound {
		t.Fatalf("unknown-id revoke status = %d, want 404; body=%s", status, body)
	}
}

// TestShareClusterScoping verifies that share links are strictly bound to the cluster
// where they were created. A link created for cluster A cannot resolve against cluster B,
// and listing/revoking in cluster B does not touch shares belonging to cluster A.
func TestShareClusterScoping(t *testing.T) {
	store := newTestStore(t)
	ownerID := insertShareTestUser(t, store, "owner-cluster")
	reg := kube.NewRegistry("local")

	localServer := newShareTestServer("srv-scoped", ownerID)
	remoteServer := newShareTestServer("srv-scoped", ownerID)

	localK := fakeKubeClient(localServer)
	remoteK := fakeKubeClient(remoteServer)

	reg.Set("local", localK)
	h := mountSharesRouter(reg, store)
	ctx := context.Background()

	// Mint a share link bound to "remote-cluster".
	token, link, err := store.CreateShareLink(ctx, "remote-cluster", "gameplane-games", "srv-scoped", ownerID, true, ptrTime(time.Now().UTC().Add(time.Hour)))
	if err != nil {
		t.Fatalf("create share link on remote cluster: %v", err)
	}

	// 1. Resolve when remote-cluster is not in registry -> returns 404.
	status, _ := shareReq(t, h, "GET", "/shares/"+token, nil, nil, "203.0.113.20:1")
	if status != http.StatusNotFound {
		t.Fatalf("resolve unknown cluster: status = %d, want 404", status)
	}

	// Now register remote-cluster.
	reg.Set("remote-cluster", remoteK)

	// 2. Resolve now succeeds and resolves to remote-cluster.
	status, body := shareReq(t, h, "GET", "/shares/"+token, nil, nil, "203.0.113.21:1")
	if status != http.StatusOK {
		t.Fatalf("resolve remote cluster: status = %d, want 200; body=%s", status, body)
	}

	// 3. List on "local" cluster must NOT return the link.
	localLinks, err := store.ListShareLinks(ctx, "local", "gameplane-games", "srv-scoped")
	if err != nil {
		t.Fatalf("list local: %v", err)
	}
	if len(localLinks) != 0 {
		t.Fatalf("expected 0 local links, got %d", len(localLinks))
	}

	// List on "remote-cluster" must return the link.
	remoteLinks, err := store.ListShareLinks(ctx, "remote-cluster", "gameplane-games", "srv-scoped")
	if err != nil {
		t.Fatalf("list remote: %v", err)
	}
	if len(remoteLinks) != 1 {
		t.Fatalf("expected 1 remote link, got %d", len(remoteLinks))
	}

	// 4. Revoke on "local" cluster must fail (link belongs to remote-cluster).
	err = store.RevokeShareLink(ctx, "local", link.Namespace, link.ServerName, link.ID)
	if err == nil {
		t.Fatalf("expected error revoking remote link with local cluster, got nil")
	}

	// 5. Revoke on "remote-cluster" succeeds.
	err = store.RevokeShareLink(ctx, "remote-cluster", link.Namespace, link.ServerName, link.ID)
	if err != nil {
		t.Fatalf("revoke with correct cluster failed: %v", err)
	}
}

// TestShareCreateExpiryNeitherFieldRejected verifies that a create request
// carrying none of expiresAt, neverExpires, or expiresIn is rejected with 400
// (OD-7): a missing field must never silently create a permanent or
// defaulted link outside the explicitly-deprecated expiresIn path.
func TestShareCreateExpiryNeitherFieldRejected(t *testing.T) {
	store := newTestStore(t)
	ownerID := insertShareTestUser(t, store, "owner-expiry-neither")
	reg := kube.NewRegistry("local")
	reg.Set("local", fakeKubeClient(newShareTestServer("srv-expiry-neither", ownerID)))
	h := mountSharesRouter(reg, store)
	owner := &auth.User{ID: ownerID, Username: "owner-expiry-neither", Role: "admin"}

	status, body := shareReq(t, h, "POST", "/servers/srv-expiry-neither:shares", map[string]any{"canStart": false}, owner, "203.0.113.30:1")
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", status, body)
	}
}

// TestShareCreateExpiryBothFieldsRejected verifies that a create request
// carrying both expiresAt and neverExpires:true is rejected with 400 (OD-1).
func TestShareCreateExpiryBothFieldsRejected(t *testing.T) {
	store := newTestStore(t)
	ownerID := insertShareTestUser(t, store, "owner-expiry-both")
	reg := kube.NewRegistry("local")
	reg.Set("local", fakeKubeClient(newShareTestServer("srv-expiry-both", ownerID)))
	h := mountSharesRouter(reg, store)
	owner := &auth.User{ID: ownerID, Username: "owner-expiry-both", Role: "admin"}

	future := time.Now().UTC().Add(48 * time.Hour).Format(time.RFC3339)
	status, body := shareReq(t, h, "POST", "/servers/srv-expiry-both:shares",
		map[string]any{"canStart": false, "expiresAt": future, "neverExpires": true}, owner, "203.0.113.31:1")
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", status, body)
	}
}

// TestShareCreateNeverExpires verifies that neverExpires:true creates a link
// whose response carries expiresAt: null, and that it resolves successfully
// (FR-002, SC-001).
func TestShareCreateNeverExpires(t *testing.T) {
	store := newTestStore(t)
	ownerID := insertShareTestUser(t, store, "owner-never-expires")
	reg := kube.NewRegistry("local")
	reg.Set("local", fakeKubeClient(newShareTestServer("srv-never-expires", ownerID)))
	h := mountSharesRouter(reg, store)
	owner := &auth.User{ID: ownerID, Username: "owner-never-expires", Role: "admin"}

	status, body := shareReq(t, h, "POST", "/servers/srv-never-expires:shares",
		map[string]any{"canStart": false, "neverExpires": true}, owner, "203.0.113.32:1")
	if status != http.StatusOK {
		t.Fatalf("create status = %d, want 200; body=%s", status, body)
	}
	var created map[string]any
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("unmarshal create response: %v; body=%s", err, body)
	}
	if v, ok := created["expiresAt"]; !ok || v != nil {
		t.Fatalf("expiresAt = %v, want explicit null; body=%s", v, body)
	}
	token, _ := created["token"].(string)
	if token == "" {
		t.Fatalf("missing token; body=%s", body)
	}

	status, body = shareReq(t, h, "GET", "/shares/"+token, nil, nil, "203.0.113.33:1")
	if status != http.StatusOK {
		t.Fatalf("resolve status = %d, want 200; body=%s", status, body)
	}
}

// TestShareCreateAbsoluteExpiresAt verifies that a request carrying an
// absolute RFC3339 expiresAt (including a custom date far beyond the former
// 90-day cap, SC-002) echoes back the exact requested instant with no clamp.
func TestShareCreateAbsoluteExpiresAt(t *testing.T) {
	store := newTestStore(t)
	ownerID := insertShareTestUser(t, store, "owner-abs-expiry")
	reg := kube.NewRegistry("local")
	reg.Set("local", fakeKubeClient(newShareTestServer("srv-abs-expiry", ownerID)))
	h := mountSharesRouter(reg, store)
	owner := &auth.User{ID: ownerID, Username: "owner-abs-expiry", Role: "admin"}

	// 200 days out: previously impossible under the 90-day cap (SC-002).
	want := time.Now().UTC().AddDate(0, 0, 200).Truncate(time.Second)
	status, body := shareReq(t, h, "POST", "/servers/srv-abs-expiry:shares",
		map[string]any{"canStart": false, "expiresAt": want.Format(time.RFC3339)}, owner, "203.0.113.34:1")
	if status != http.StatusOK {
		t.Fatalf("create status = %d, want 200; body=%s", status, body)
	}
	var created map[string]any
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("unmarshal create response: %v; body=%s", err, body)
	}
	gotStr, _ := created["expiresAt"].(string)
	got, err := time.Parse(time.RFC3339, gotStr)
	if err != nil {
		t.Fatalf("parse response expiresAt %q: %v", gotStr, err)
	}
	if !got.Equal(want) {
		t.Fatalf("expiresAt = %v, want exactly %v (no clamp)", got, want)
	}
}

// TestShareCreateExpiresAtMalformedRejected verifies that a malformed
// expiresAt value is rejected with 400 rather than silently defaulted.
func TestShareCreateExpiresAtMalformedRejected(t *testing.T) {
	store := newTestStore(t)
	ownerID := insertShareTestUser(t, store, "owner-bad-expiry")
	reg := kube.NewRegistry("local")
	reg.Set("local", fakeKubeClient(newShareTestServer("srv-bad-expiry", ownerID)))
	h := mountSharesRouter(reg, store)
	owner := &auth.User{ID: ownerID, Username: "owner-bad-expiry", Role: "admin"}

	status, body := shareReq(t, h, "POST", "/servers/srv-bad-expiry:shares",
		map[string]any{"canStart": false, "expiresAt": "not-a-date"}, owner, "203.0.113.35:1")
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", status, body)
	}
}

// TestShareCreateExpiresAtPastRejected verifies that an expiresAt one hour in
// the past is rejected with 400 (mapping db.ErrShareLinkExpiryInvalid), not a
// 500 from httperr.Write.
func TestShareCreateExpiresAtPastRejected(t *testing.T) {
	store := newTestStore(t)
	ownerID := insertShareTestUser(t, store, "owner-past-expiry")
	reg := kube.NewRegistry("local")
	reg.Set("local", fakeKubeClient(newShareTestServer("srv-past-expiry", ownerID)))
	h := mountSharesRouter(reg, store)
	owner := &auth.User{ID: ownerID, Username: "owner-past-expiry", Role: "admin"}

	past := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)
	status, body := shareReq(t, h, "POST", "/servers/srv-past-expiry:shares",
		map[string]any{"canStart": false, "expiresAt": past}, owner, "203.0.113.38:1")
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", status, body)
	}
}

// TestShareCreateDeprecatedExpiresInPastRejected verifies that the deprecated
// expiresIn path also maps a past/invalid computed expiry to 400, not 500.
func TestShareCreateDeprecatedExpiresInPastRejected(t *testing.T) {
	store := newTestStore(t)
	ownerID := insertShareTestUser(t, store, "owner-deprecated-past-expiry")
	reg := kube.NewRegistry("local")
	reg.Set("local", fakeKubeClient(newShareTestServer("srv-deprecated-past-expiry", ownerID)))
	h := mountSharesRouter(reg, store)
	owner := &auth.User{ID: ownerID, Username: "owner-deprecated-past-expiry", Role: "admin"}

	status, body := shareReq(t, h, "POST", "/servers/srv-deprecated-past-expiry:shares",
		map[string]any{"canStart": false, "expiresIn": "-1h"}, owner, "203.0.113.39:1")
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", status, body)
	}
}

// TestShareCreateDeprecatedExpiresIn verifies that the deprecated expiresIn
// path still works for one release (OD-1/OD-5): both an explicit duration and
// an empty/omitted value (7-day default, only on this path) still succeed,
// with no maximum-lifetime clamp applied (FR-005 removes the cap everywhere).
func TestShareCreateDeprecatedExpiresIn(t *testing.T) {
	store := newTestStore(t)
	ownerID := insertShareTestUser(t, store, "owner-deprecated-expiry")
	reg := kube.NewRegistry("local")
	reg.Set("local", fakeKubeClient(newShareTestServer("srv-deprecated-expiry", ownerID)))
	h := mountSharesRouter(reg, store)
	owner := &auth.User{ID: ownerID, Username: "owner-deprecated-expiry", Role: "admin"}

	// Explicit duration well beyond the old 90-day cap: must not be clamped.
	status, body := shareReq(t, h, "POST", "/servers/srv-deprecated-expiry:shares",
		map[string]any{"canStart": false, "expiresIn": "4320h"}, owner, "203.0.113.36:1") // 180 days
	if status != http.StatusOK {
		t.Fatalf("create status = %d, want 200; body=%s", status, body)
	}
	var created map[string]any
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("unmarshal create response: %v; body=%s", err, body)
	}
	gotStr, _ := created["expiresAt"].(string)
	got, err := time.Parse(time.RFC3339, gotStr)
	if err != nil {
		t.Fatalf("parse response expiresAt %q: %v", gotStr, err)
	}
	want := time.Now().UTC().AddDate(0, 0, 180)
	if diff := got.Sub(want); diff < -time.Minute || diff > time.Minute {
		t.Fatalf("expiresAt = %v, want approximately %v (180d, unclamped)", got, want)
	}

	// Empty/omitted expiresIn defaults to 7 days, only on this deprecated path.
	status, body = shareReq(t, h, "POST", "/servers/srv-deprecated-expiry:shares",
		map[string]any{"canStart": false, "expiresIn": ""}, owner, "203.0.113.37:1")
	if status != http.StatusOK {
		t.Fatalf("create (default) status = %d, want 200; body=%s", status, body)
	}
	var createdDefault map[string]any
	if err := json.Unmarshal(body, &createdDefault); err != nil {
		t.Fatalf("unmarshal create (default) response: %v; body=%s", err, body)
	}
	gotDefaultStr, _ := createdDefault["expiresAt"].(string)
	gotDefault, err := time.Parse(time.RFC3339, gotDefaultStr)
	if err != nil {
		t.Fatalf("parse response expiresAt %q: %v", gotDefaultStr, err)
	}
	wantDefault := time.Now().UTC().AddDate(0, 0, 7)
	if diff := gotDefault.Sub(wantDefault); diff < -time.Minute || diff > time.Minute {
		t.Fatalf("default expiresAt = %v, want approximately %v (7d default)", gotDefault, wantDefault)
	}
}

// TestShareRevokeScopedToPathServer verifies that a revoke is scoped to the
// server named in the path: a link of another server the caller also owns
// answers 404 and stays valid.
func TestShareRevokeScopedToPathServer(t *testing.T) {
	store := newTestStore(t)
	ownerID := insertShareTestUser(t, store, "owner-revoke-scope")
	reg := kube.NewRegistry("local")
	reg.Set("local", fakeKubeClient(
		newShareTestServer("srv-scope-a", ownerID),
		newShareTestServer("srv-scope-b", ownerID),
	))
	h := mountSharesRouter(reg, store)
	owner := &auth.User{ID: ownerID, Username: "owner-revoke-scope", Role: "admin"}

	status, body := shareReq(t, h, "POST", "/servers/srv-scope-a:shares", map[string]any{"canStart": false, "expiresIn": "1h"}, owner, "203.0.113.40:1")
	if status != http.StatusOK {
		t.Fatalf("create status = %d, want 200; body=%s", status, body)
	}
	var created map[string]any
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("unmarshal create response: %v", err)
	}
	token, _ := created["token"].(string)
	id, _ := created["id"].(string)
	if token == "" || id == "" {
		t.Fatalf("create response missing token/id; body=%s", body)
	}

	status, body = shareReq(t, h, "DELETE", "/servers/srv-scope-b/shares/"+id, nil, owner, "203.0.113.41:1")
	if status != http.StatusNotFound {
		t.Fatalf("revoke through another server: status = %d, want 404; body=%s", status, body)
	}
	status, _ = shareReq(t, h, "GET", "/shares/"+token, nil, nil, "203.0.113.42:1")
	if status != http.StatusOK {
		t.Fatalf("resolve after mismatched revoke: status = %d, want 200", status)
	}

	status, body = shareReq(t, h, "DELETE", "/servers/srv-scope-a/shares/"+id, nil, owner, "203.0.113.43:1")
	if status != http.StatusNoContent {
		t.Fatalf("revoke through its own server: status = %d, want 204; body=%s", status, body)
	}
}

// TestShareRevokeUnknownIDReturns404 verifies that revoking an id that does
// not exist answers 404, not a server error.
func TestShareRevokeUnknownIDReturns404(t *testing.T) {
	store := newTestStore(t)
	ownerID := insertShareTestUser(t, store, "owner-revoke-unknown")
	reg := kube.NewRegistry("local")
	reg.Set("local", fakeKubeClient(newShareTestServer("srv-revoke-unknown", ownerID)))
	h := mountSharesRouter(reg, store)
	owner := &auth.User{ID: ownerID, Username: "owner-revoke-unknown", Role: "admin"}

	status, body := shareReq(t, h, "DELETE", "/servers/srv-revoke-unknown/shares/no-such-id", nil, owner, "203.0.113.44:1")
	if status != http.StatusNotFound {
		t.Fatalf("revoke unknown id: status = %d, want 404; body=%s", status, body)
	}
}

// TestShareLinkStopsResolvingWhenCreatorDeleted verifies that deleting a
// user revokes the share links that user created: the token then gets the
// same uniform 404 as any other invalid token.
func TestShareLinkStopsResolvingWhenCreatorDeleted(t *testing.T) {
	store := newTestStore(t)
	ownerID := insertShareTestUser(t, store, "owner-deleted")
	reg := kube.NewRegistry("local")
	reg.Set("local", fakeKubeClient(newShareTestServer("srv-owner-deleted", ownerID)))
	h := mountSharesRouter(reg, store)
	owner := &auth.User{ID: ownerID, Username: "owner-deleted", Role: "admin"}

	status, body := shareReq(t, h, "POST", "/servers/srv-owner-deleted:shares", map[string]any{"canStart": true, "neverExpires": true}, owner, "203.0.113.45:1")
	if status != http.StatusOK {
		t.Fatalf("create status = %d, want 200; body=%s", status, body)
	}
	var created map[string]any
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("unmarshal create response: %v", err)
	}
	token, _ := created["token"].(string)
	if token == "" {
		t.Fatalf("missing token; body=%s", body)
	}
	if status, _ := shareReq(t, h, "GET", "/shares/"+token, nil, nil, "203.0.113.46:1"); status != http.StatusOK {
		t.Fatalf("resolve before delete: status = %d, want 200", status)
	}

	if err := store.DeleteUser(t.Context(), ownerID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	status, body = shareReq(t, h, "GET", "/shares/"+token, nil, nil, "203.0.113.47:1")
	if status != http.StatusNotFound {
		t.Fatalf("resolve after creator delete: status = %d, want 404; body=%s", status, body)
	}
	if !bytes.Equal(body, wantShareNotFoundBody) {
		t.Fatalf("resolve after creator delete: body = %q, want %q", body, wantShareNotFoundBody)
	}
	status, body = shareReq(t, h, "POST", "/shares/"+token+"/start", nil, nil, "203.0.113.48:1")
	if status != http.StatusNotFound {
		t.Fatalf("start after creator delete: status = %d, want 404; body=%s", status, body)
	}
}

// TestShareListIncludesRevokedStatus verifies that a revoked share link
// appears in the list with revokedAt set, and shows status "Revoked" to the UI.
func TestShareListIncludesRevokedStatus(t *testing.T) {
	store := newTestStore(t)
	ownerID := insertShareTestUser(t, store, "owner-list-revoked")
	reg := kube.NewRegistry("local")
	reg.Set("local", fakeKubeClient(newShareTestServer("srv-list-revoked", ownerID)))
	h := mountSharesRouter(reg, store)
	owner := &auth.User{ID: ownerID, Username: "owner-list-revoked", Role: "admin"}

	ctx := context.Background()

	// Create a link, revoke it, then list it.
	token, link, err := store.CreateShareLink(ctx, "local", "gameplane-games", "srv-list-revoked", ownerID, false, ptrTime(time.Now().UTC().Add(time.Hour)))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := store.RevokeShareLink(ctx, "local", link.Namespace, link.ServerName, link.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	// List the link.
	status, body := shareReq(t, h, "GET", "/servers/srv-list-revoked:shares", nil, owner, "203.0.113.50:1")
	if status != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body=%s", status, body)
	}
	var listed []map[string]any
	if err := json.Unmarshal(body, &listed); err != nil {
		t.Fatalf("unmarshal list response: %v; body=%s", err, body)
	}
	if len(listed) != 1 {
		t.Fatalf("list length = %d, want 1; body=%s", len(listed), body)
	}

	// Verify revokedAt is present in the list response.
	item := listed[0]
	revokedAt, ok := item["revokedAt"].(string)
	if !ok || revokedAt == "" {
		t.Fatalf("list response missing revokedAt field or not a string; got %v; body=%s", item["revokedAt"], body)
	}

	// Verify the revoked link no longer resolves publicly.
	status, _ = shareReq(t, h, "GET", "/shares/"+token, nil, nil, "203.0.113.51:1")
	if status != http.StatusNotFound {
		t.Fatalf("resolve revoked token: status = %d, want 404", status)
	}
}

// serverWithCreation returns a share-test server whose metadata carries the
// given creationTimestamp, as a real apiserver object always does.
func serverWithCreation(name string, ownerID int64, created time.Time) *unstructured.Unstructured {
	obj := newShareTestServer(name, ownerID)
	obj.SetCreationTimestamp(metav1.NewTime(created))
	return obj
}

// TestShareLinkRejectedWhenServerRecreated verifies that a link minted for an
// earlier GameServer does not resolve or start a later server that reuses the
// name (links are keyed by name, not UID): both public paths return the
// uniform 404. A server older than the link still resolves.
func TestShareLinkRejectedWhenServerRecreated(t *testing.T) {
	store := newTestStore(t)
	ownerID := insertShareTestUser(t, store, "owner-recreated")
	token, _, err := store.CreateShareLink(t.Context(), "local", "gameplane-games", "srv-recreated", ownerID, true, nil)
	if err != nil {
		t.Fatalf("CreateShareLink: %v", err)
	}

	// Control: server created an hour before the link, so the link is valid.
	reg := kube.NewRegistry("local")
	reg.Set("local", fakeKubeClient(serverWithCreation("srv-recreated", ownerID, time.Now().Add(-time.Hour))))
	h := mountSharesRouter(reg, store)
	if status, body := shareReq(t, h, "GET", "/shares/"+token, nil, nil, "203.0.113.61:1"); status != http.StatusOK {
		t.Fatalf("resolve against older server: status = %d, want 200; body=%s", status, body)
	}

	// The server was deleted and recreated after the link was minted.
	reg2 := kube.NewRegistry("local")
	reg2.Set("local", fakeKubeClient(serverWithCreation("srv-recreated", ownerID, time.Now().Add(time.Hour))))
	h2 := mountSharesRouter(reg2, store)
	status, body := shareReq(t, h2, "GET", "/shares/"+token, nil, nil, "203.0.113.62:1")
	if status != http.StatusNotFound {
		t.Fatalf("resolve against recreated server: status = %d, want 404; body=%s", status, body)
	}
	if !bytes.Equal(body, wantShareNotFoundBody) {
		t.Fatalf("resolve against recreated server: body = %q, want %q", body, wantShareNotFoundBody)
	}
	status, body = shareReq(t, h2, "POST", "/shares/"+token+"/start", nil, nil, "203.0.113.63:1")
	if status != http.StatusNotFound {
		t.Fatalf("start against recreated server: status = %d, want 404; body=%s", status, body)
	}
	if !bytes.Equal(body, wantShareNotFoundBody) {
		t.Fatalf("start against recreated server: body = %q, want %q", body, wantShareNotFoundBody)
	}
}

// TestServerDeleteRevokesShareLinks verifies that DELETE /servers/{name}
// revokes that server's share links, and only that server's.
func TestServerDeleteRevokesShareLinks(t *testing.T) {
	store := newTestStore(t)
	ownerID := insertShareTestUser(t, store, "owner-delete-revoke")
	reg := kube.NewRegistry("local")
	reg.Set("local", fakeKubeClient(
		newShareTestServer("srv-del", ownerID),
		newShareTestServer("srv-keep", ownerID),
	))
	r := chi.NewRouter()
	MountResources(r, reg, store)

	delToken, _, err := store.CreateShareLink(t.Context(), "local", "gameplane-games", "srv-del", ownerID, false, nil)
	if err != nil {
		t.Fatalf("CreateShareLink srv-del: %v", err)
	}
	keepToken, _, err := store.CreateShareLink(t.Context(), "local", "gameplane-games", "srv-keep", ownerID, false, nil)
	if err != nil {
		t.Fatalf("CreateShareLink srv-keep: %v", err)
	}

	if status, body := shareReq(t, r, "DELETE", "/servers/srv-del", nil, nil, "203.0.113.64:1"); status != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204; body=%s", status, body)
	}

	if _, err := store.LookupShareLink(t.Context(), delToken); !errors.Is(err, db.ErrShareLinkInvalid) {
		t.Fatalf("LookupShareLink after delete = %v, want ErrShareLinkInvalid", err)
	}
	if _, err := store.LookupShareLink(t.Context(), keepToken); err != nil {
		t.Fatalf("link of the other server must stay valid, got %v", err)
	}
}

// serverWithUID is serverWithCreation plus a metadata.uid.
func serverWithUID(name string, ownerID int64, created time.Time, uid string) *unstructured.Unstructured {
	obj := serverWithCreation(name, ownerID, created)
	obj.SetUID(types.UID(uid))
	return obj
}

// shareStatusFor mints a link via CreateShareLinkForServer (serverUID may be
// empty for a legacy-style link) and returns the GET /shares/{token} status
// against a registry holding live.
func shareStatusFor(t *testing.T, store *db.Store, ownerID int64, name, serverUID string, live *unstructured.Unstructured, addr string) int {
	t.Helper()
	token, _, err := store.CreateShareLinkForServer(t.Context(), "local", "gameplane-games", name, serverUID, ownerID, true, nil)
	if err != nil {
		t.Fatalf("CreateShareLinkForServer: %v", err)
	}
	reg := kube.NewRegistry("local")
	reg.Set("local", fakeKubeClient(live))
	status, _ := shareReq(t, mountSharesRouter(reg, store), "GET", "/shares/"+token, nil, nil, addr)
	return status
}

// TestShareLinkServerUIDMismatch404 covers the same-second delete+recreate:
// the live server is OLDER than the link (timestamp check would pass) but has
// a different UID, so both public paths must 404.
func TestShareLinkServerUIDMismatch404(t *testing.T) {
	store := newTestStore(t)
	ownerID := insertShareTestUser(t, store, "owner-uid-mismatch")
	token, _, err := store.CreateShareLinkForServer(t.Context(), "local", "gameplane-games", "srv-uid-mm", "uid-old", ownerID, true, nil)
	if err != nil {
		t.Fatalf("CreateShareLinkForServer: %v", err)
	}
	reg := kube.NewRegistry("local")
	reg.Set("local", fakeKubeClient(serverWithUID("srv-uid-mm", ownerID, time.Now().Add(-time.Hour), "uid-new")))
	h := mountSharesRouter(reg, store)
	status, body := shareReq(t, h, "GET", "/shares/"+token, nil, nil, "203.0.113.71:1")
	if status != http.StatusNotFound || !bytes.Equal(body, wantShareNotFoundBody) {
		t.Fatalf("resolve with UID mismatch: status=%d body=%q, want 404 %q", status, body, wantShareNotFoundBody)
	}
	status, body = shareReq(t, h, "POST", "/shares/"+token+"/start", nil, nil, "203.0.113.72:1")
	if status != http.StatusNotFound || !bytes.Equal(body, wantShareNotFoundBody) {
		t.Fatalf("start with UID mismatch: status=%d body=%q, want 404 %q", status, body, wantShareNotFoundBody)
	}
}

// TestShareLinkServerUIDMatch200 verifies the UID is authoritative: a matching
// UID resolves and starts even when the live server's creationTimestamp is
// later than the link's created_at.
func TestShareLinkServerUIDMatch200(t *testing.T) {
	store := newTestStore(t)
	ownerID := insertShareTestUser(t, store, "owner-uid-match")
	token, _, err := store.CreateShareLinkForServer(t.Context(), "local", "gameplane-games", "srv-uid-ok", "uid-same", ownerID, true, nil)
	if err != nil {
		t.Fatalf("CreateShareLinkForServer: %v", err)
	}
	reg := kube.NewRegistry("local")
	reg.Set("local", fakeKubeClient(serverWithUID("srv-uid-ok", ownerID, time.Now().Add(time.Hour), "uid-same")))
	h := mountSharesRouter(reg, store)
	if status, body := shareReq(t, h, "GET", "/shares/"+token, nil, nil, "203.0.113.73:1"); status != http.StatusOK {
		t.Fatalf("resolve with matching UID: status=%d body=%s, want 200", status, body)
	}
	if status, body := shareReq(t, h, "POST", "/shares/"+token+"/start", nil, nil, "203.0.113.74:1"); status != http.StatusAccepted {
		t.Fatalf("start with matching UID: status=%d body=%s, want 202", status, body)
	}
}

// TestShareLinkLegacyEmptyUIDUsesTimestamp verifies links without a stored
// UID (pre-013) keep the creationTimestamp validation, and that an empty UID
// never matches by itself: a live server that has a UID does not make a
// legacy link pass when it is newer than the link.
func TestShareLinkLegacyEmptyUIDUsesTimestamp(t *testing.T) {
	store := newTestStore(t)
	ownerID := insertShareTestUser(t, store, "owner-uid-legacy")

	older := serverWithUID("srv-legacy-old", ownerID, time.Now().Add(-time.Hour), "uid-live")
	if got := shareStatusFor(t, store, ownerID, "srv-legacy-old", "", older, "203.0.113.75:1"); got != http.StatusOK {
		t.Fatalf("legacy link vs older server: status=%d, want 200", got)
	}
	newer := serverWithUID("srv-legacy-new", ownerID, time.Now().Add(time.Hour), "uid-live")
	if got := shareStatusFor(t, store, ownerID, "srv-legacy-new", "", newer, "203.0.113.76:1"); got != http.StatusNotFound {
		t.Fatalf("legacy link vs newer server: status=%d, want 404", got)
	}
}

// TestShareCreateStoresServerUID verifies the create handler binds the link
// to the live GameServer's UID.
func TestShareCreateStoresServerUID(t *testing.T) {
	store := newTestStore(t)
	ownerID := insertShareTestUser(t, store, "owner-uid-create")
	reg := kube.NewRegistry("local")
	reg.Set("local", fakeKubeClient(serverWithUID("srv-uid-create", ownerID, time.Now().Add(-time.Hour), "uid-create")))
	h := mountSharesRouter(reg, store)

	status, body := shareReq(t, h, "POST", "/servers/srv-uid-create:shares?namespace=gameplane-games",
		map[string]any{"neverExpires": true}, &auth.User{ID: ownerID, Role: "admin"}, "203.0.113.77:1")
	if status != http.StatusOK {
		t.Fatalf("create: status=%d body=%s", status, body)
	}
	links, err := store.ListShareLinks(t.Context(), "local", "gameplane-games", "srv-uid-create")
	if err != nil {
		t.Fatalf("ListShareLinks: %v", err)
	}
	if len(links) != 1 || links[0].ServerUID != "uid-create" {
		t.Fatalf("links = %+v, want one link with ServerUID uid-create", links)
	}
}
