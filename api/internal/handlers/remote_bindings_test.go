package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/rbac"
)

func remoteBindingFixture(t *testing.T) (http.Handler, *db.Store, *auth.SessionStore, int64) {
	t.Helper()
	store := newTestStore(t)
	sessions := auth.NewSessionStore(store)
	id := seedUser(t, store, "remote-reader", "viewer", "")
	if err := store.SetClusterRoleBinding(t.Context(), nil, id, "local", "viewer"); err != nil {
		t.Fatal(err)
	}
	reg := clusterTestRegistry(fakeKubeClient())
	reg.Set("remote", fakeKubeClient())
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			caller := inventoryUser("local", "*", "*")
			caller.ID, caller.Role = 999, "admin"
			next.ServeHTTP(w, req.WithContext(auth.WithUser(req.Context(), caller)))
		})
	})
	r.Use(rbac.Middleware(reg))
	MountUsers(r, store, sessions, reg)
	MountRoles(r, store)
	return r, store, sessions, id
}

func remoteBindingRequest(t *testing.T, r http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequestWithContext(t.Context(), method, path, bytes.NewReader(encoded))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	return rr
}

func createRemoteRole(t *testing.T, r http.Handler, name string, permissions []string) {
	t.Helper()
	rr := remoteBindingRequest(t, r, http.MethodPost, "/roles", map[string]any{"name": name, "permissions": permissions})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create role: status=%d body=%s", rr.Code, rr.Body)
	}
}

func loadedBindingUser(t *testing.T, sessions *auth.SessionStore, id int64) *auth.User {
	t.Helper()
	perms, err := sessions.LoadPerms(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return &auth.User{ID: id, Perms: perms}
}

func TestRemoteBindings_GrantAndRevokePreservePrimaryRole(t *testing.T) {
	r, store, sessions, id := remoteBindingFixture(t)
	createRemoteRole(t, r, "cluster-reader", []string{"cluster:read", "servers:read"})
	path := "/users/" + strconv.FormatInt(id, 10) + "/bindings"
	if _, _, err := sessions.Create(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	rr := remoteBindingRequest(t, r, http.MethodPost, path, addBindingReq{RoleName: "cluster-reader", Namespace: "*", Cluster: "remote"})
	if rr.Code != http.StatusCreated || sessionCount(t, store, id) != 0 {
		t.Fatalf("grant: status=%d body=%s", rr.Code, rr.Body)
	}
	u := loadedBindingUser(t, sessions, id)
	if !u.Can("cluster:read", true, "remote", "") || !u.Can("servers:read", true, "remote", "gameplane-games") || u.Can("cluster:read", true, "other", "") || u.Can("users:manage", false, "", "") {
		t.Fatal("scoped grant did not resolve to the intended remote permissions")
	}
	// The supported API grant must be sufficient for the actual inventory route.
	reg := clusterTestRegistry(fakeKubeClient())
	reg.Set("remote", inventoryClient("remote-node", "v1.31.2", "7Gi"))
	readRouter := chi.NewRouter()
	readRouter.Use(rbac.Middleware(reg))
	MountCluster(readRouter, reg, store, "test", false, "")
	if got := inventoryRequest(t, readRouter, u, http.MethodGet, "/cluster?cluster=remote"); got.Code != http.StatusOK {
		t.Fatalf("granted inventory: status=%d body=%s", got.Code, got.Body)
	}
	rr = remoteBindingRequest(t, r, http.MethodGet, path, nil)
	var bindings []bindingDTO
	if err := json.Unmarshal(rr.Body.Bytes(), &bindings); err != nil {
		t.Fatal(err)
	}
	if rr.Code != http.StatusOK || len(bindings) != 2 {
		t.Fatalf("bindings=%+v status=%d", bindings, rr.Code)
	}
	rr = remoteBindingRequest(t, r, http.MethodPost, path, addBindingReq{RoleName: "cluster-reader", Namespace: "*", Cluster: "remote"})
	if rr.Code != http.StatusConflict {
		t.Fatalf("duplicate status=%d", rr.Code)
	}
	for _, suffix := range []string{"", "?cluster=local"} {
		rr = remoteBindingRequest(t, r, http.MethodDelete, path+"/viewer/*"+suffix, nil)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("primary removal: status=%d", rr.Code)
		}
	}
	if _, _, err := sessions.Create(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	rr = remoteBindingRequest(t, r, http.MethodDelete, path+"/cluster-reader/*?cluster=remote", nil)
	if rr.Code != http.StatusNoContent || sessionCount(t, store, id) != 0 {
		t.Fatalf("revoke: status=%d body=%s", rr.Code, rr.Body)
	}
	u = loadedBindingUser(t, sessions, id)
	if u.Can("cluster:read", true, "remote", "") || !u.Can("cluster:read", true, "local", "") {
		t.Fatal("revocation changed the wrong cluster")
	}
	if got := inventoryRequest(t, readRouter, u, http.MethodGet, "/cluster?cluster=remote"); got.Code != http.StatusForbidden {
		t.Fatalf("revoked inventory status=%d", got.Code)
	}
	var primary string
	if err := store.DB.QueryRowContext(t.Context(), `SELECT role FROM users WHERE id = ?`, id).Scan(&primary); err != nil || primary != "viewer" {
		t.Fatalf("primary=%s err=%v", primary, err)
	}
}

func TestRemoteBindings_RejectGlobalRolesAndNonRemoteTargets(t *testing.T) {
	r, store, sessions, id := remoteBindingFixture(t)
	createRemoteRole(t, r, "cluster-reader", []string{"cluster:read"})
	for _, permission := range []string{"users:manage", "roles:manage", "config:manage", "cluster:manage"} {
		resource, _, _ := strings.Cut(permission, ":")
		createRemoteRole(t, r, "global-"+resource, []string{permission})
	}
	path := "/users/" + strconv.FormatInt(id, 10) + "/bindings"
	for _, tc := range []addBindingReq{
		{RoleName: "admin", Namespace: "*", Cluster: "remote"},
		{RoleName: "viewer", Namespace: "*", Cluster: "remote"},
		{RoleName: "global-users", Namespace: "*", Cluster: "remote"},
		{RoleName: "global-roles", Namespace: "*", Cluster: "remote"},
		{RoleName: "global-config", Namespace: "*", Cluster: "remote"},
		{RoleName: "global-cluster", Namespace: "*", Cluster: "remote"},
		{RoleName: "cluster-reader", Namespace: "*", Cluster: "local"},
		{RoleName: "cluster-reader", Namespace: "*", Cluster: ""},
		{RoleName: "cluster-reader", Namespace: "*", Cluster: "*"},
		{RoleName: "cluster-reader", Namespace: "*", Cluster: "missing"},
	} {
		rr := remoteBindingRequest(t, r, http.MethodPost, path, tc)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("%+v: status=%d body=%s", tc, rr.Code, rr.Body)
		}
	}
	if loadedBindingUser(t, sessions, id).Can("cluster:read", true, "remote", "") {
		t.Fatal("rejected grant persisted")
	}
	// Unsafe legacy remote-wide bindings were not removable before this change.
	if _, err := store.DB.ExecContext(t.Context(), `INSERT INTO user_role_bindings(user_id, role_name, cluster, namespace) VALUES (?, 'admin', 'remote', '*')`, id); err != nil {
		t.Fatal(err)
	}
	rr := remoteBindingRequest(t, r, http.MethodDelete, path+"/admin/*?cluster=remote", nil)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("legacy global removal status=%d", rr.Code)
	}
}

func TestRemoteBindings_ModuleAndTemplateRoleGrantsStayOnSelectedCluster(t *testing.T) {
	r, _, sessions, id := remoteBindingFixture(t)
	permissions := []string{"modules:read", "modules:manage", "templates:read", "templates:write"}
	before := loadedBindingUser(t, sessions, id)
	createRemoteRole(t, r, "remote-catalog", permissions)
	path := "/users/" + strconv.FormatInt(id, 10) + "/bindings"
	granted := remoteBindingRequest(t, r, http.MethodPost, path, addBindingReq{RoleName: "remote-catalog", Namespace: "*", Cluster: "remote"})
	if granted.Code != http.StatusCreated {
		t.Fatalf("remote catalog grant: %d %s", granted.Code, granted.Body)
	}
	u := loadedBindingUser(t, sessions, id)
	for _, permission := range permissions {
		if !u.Can(permission, false, "remote", "") {
			t.Errorf("remote role did not grant %s", permission)
		}
		if u.Can(permission, false, "other", "") {
			t.Errorf("remote role leaked %s to other cluster", permission)
		}
		if u.Can(permission, false, "local", "") != before.Can(permission, false, "local", "") {
			t.Errorf("remote role changed primary local grant for %s", permission)
		}
	}
	for _, permission := range []string{"users:manage", "roles:manage", "config:manage", "cluster:manage"} {
		if u.Can(permission, false, "", "") {
			t.Errorf("remote catalog role conferred central %s", permission)
		}
	}
	// A bound role can gain another target-scoped catalog permission while
	// central administration remains forbidden by the role-edit guards.
	updated := remoteBindingRequest(t, r, http.MethodPatch, "/roles/remote-catalog", map[string]any{"permissions": []string{"modules:read", "templates:read"}})
	if updated.Code != http.StatusOK {
		t.Fatalf("catalog role read-only edit: %d %s", updated.Code, updated.Body)
	}
	u = loadedBindingUser(t, sessions, id)
	if u.Can("modules:manage", false, "remote", "") || u.Can("templates:write", false, "remote", "") || !u.Can("templates:read", false, "remote", "") {
		t.Fatal("catalog role edit failed to revoke writes while preserving reads")
	}
	updated = remoteBindingRequest(t, r, http.MethodPatch, "/roles/remote-catalog", map[string]any{"permissions": permissions})
	if updated.Code != http.StatusOK {
		t.Fatalf("catalog role target-scoped edit: %d %s", updated.Code, updated.Body)
	}
	for _, permission := range []string{"users:manage", "roles:manage", "config:manage", "cluster:manage"} {
		denied := remoteBindingRequest(t, r, http.MethodPatch, "/roles/remote-catalog", map[string]any{"permissions": []string{"modules:read", permission}})
		if denied.Code != http.StatusBadRequest {
			t.Errorf("catalog role widened to %s: %d %s", permission, denied.Code, denied.Body)
		}
	}
	revoked := remoteBindingRequest(t, r, http.MethodDelete, path+"/remote-catalog/*?cluster=remote", nil)
	if revoked.Code != http.StatusNoContent {
		t.Fatalf("catalog role revoke: %d %s", revoked.Code, revoked.Body)
	}
	u = loadedBindingUser(t, sessions, id)
	for _, permission := range permissions {
		if u.Can(permission, false, "remote", "") {
			t.Errorf("revoked remote permission survived: %s", permission)
		}
	}
}

func TestRemoteBindings_RoleEditsCannotWidenButCanRevoke(t *testing.T) {
	r, _, sessions, id := remoteBindingFixture(t)
	createRemoteRole(t, r, "cluster-reader", []string{"cluster:read", "servers:read"})
	path := "/users/" + strconv.FormatInt(id, 10) + "/bindings"
	rr := remoteBindingRequest(t, r, http.MethodPost, path, addBindingReq{RoleName: "cluster-reader", Namespace: "*", Cluster: "remote"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("grant status=%d body=%s", rr.Code, rr.Body)
	}
	for _, permission := range []string{"*", "users:manage", "roles:manage", "config:manage", "cluster:manage"} {
		rr = remoteBindingRequest(t, r, http.MethodPatch, "/roles/cluster-reader", map[string]any{"permissions": []string{"cluster:read", permission}})
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("widen to %s: status=%d body=%s", permission, rr.Code, rr.Body)
		}
		if loadedBindingUser(t, sessions, id).Can("users:manage", false, "", "") {
			t.Fatal("role edit escalated remote reader")
		}
	}
	rr = remoteBindingRequest(t, r, http.MethodDelete, "/roles/cluster-reader", nil)
	if rr.Code != http.StatusConflict {
		t.Fatalf("in-use role deletion status=%d", rr.Code)
	}
	for _, permissions := range [][]string{{"servers:read"}, {}} {
		rr = remoteBindingRequest(t, r, http.MethodPatch, "/roles/cluster-reader", map[string]any{"permissions": permissions})
		if rr.Code != http.StatusOK {
			t.Fatalf("safe permission revoke status=%d body=%s", rr.Code, rr.Body)
		}
		if loadedBindingUser(t, sessions, id).Can("cluster:read", true, "remote", "") {
			t.Fatal("removed inventory permission still effective")
		}
	}
	rr = remoteBindingRequest(t, r, http.MethodDelete, path+"/cluster-reader/*?cluster=remote", nil)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("empty safe role removal status=%d", rr.Code)
	}
	rr = remoteBindingRequest(t, r, http.MethodPatch, "/roles/cluster-reader", map[string]any{"permissions": []string{"users:manage"}})
	if rr.Code != http.StatusOK {
		t.Fatalf("unbound custom role edit status=%d body=%s", rr.Code, rr.Body)
	}
}

func TestRemoteBindings_ConcurrentRoleChangesCannotEscalate(t *testing.T) {
	for _, action := range []string{"widen", "delete-recreate"} {
		t.Run(action, func(t *testing.T) {
			r, _, sessions, id := remoteBindingFixture(t)
			for iteration := range 8 {
				role := fmt.Sprintf("reader-%d", iteration)
				createRemoteRole(t, r, role, []string{"cluster:read"})
				path := "/users/" + strconv.FormatInt(id, 10) + "/bindings"
				start := make(chan struct{})
				var grant, change *httptest.ResponseRecorder
				var wg sync.WaitGroup
				wg.Add(2)
				go func() {
					defer wg.Done()
					<-start
					grant = remoteBindingRequest(t, r, http.MethodPost, path, addBindingReq{RoleName: role, Namespace: "*", Cluster: "remote"})
				}()
				go func() {
					defer wg.Done()
					<-start
					if action == "widen" {
						change = remoteBindingRequest(t, r, http.MethodPatch, "/roles/"+role, map[string]any{"permissions": []string{"users:manage"}})
					} else {
						change = remoteBindingRequest(t, r, http.MethodDelete, "/roles/"+role, nil)
					}
				}()
				close(start)
				wg.Wait()
				if grant.Code == http.StatusCreated {
					want := http.StatusBadRequest
					if action == "delete-recreate" {
						want = http.StatusConflict
					}
					if change.Code != want {
						t.Fatalf("grant succeeded alongside unsafe change: grant=%d change=%d", grant.Code, change.Code)
					}
				} else if grant.Code != http.StatusBadRequest {
					t.Fatalf("grant failed unexpectedly: status=%d body=%s", grant.Code, grant.Body)
				}
				if action == "delete-recreate" && change.Code == http.StatusNoContent {
					createRemoteRole(t, r, role, []string{"users:manage"})
				}
				if loadedBindingUser(t, sessions, id).Can("users:manage", false, "", "") {
					t.Fatal("concurrent role change escalated supplemental grant")
				}
			}
		})
	}
}
