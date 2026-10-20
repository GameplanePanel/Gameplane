package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/go-chi/chi/v5"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/rbac"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

func fleetTestClient(objects ...runtime.Object) *kube.Client {
	kinds := map[schema.GroupVersionResource]string{
		kube.GVRCluster: "ClusterList", kube.GVRs["servers"]: "GameServerList",
		kube.GVRs["backups"]: "BackupList", kube.GVRs["schedules"]: "BackupScheduleList",
		kube.GVRs["restores"]: "RestoreList", kube.GVRs["templates"]: "GameTemplateList",
	}
	return &kube.Client{Dynamic: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), kinds, objects...), Typed: kubefake.NewClientset()}
}

func fleetObject(kind, namespace, name, uid string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1", "kind": kind,
		"metadata": map[string]any{"name": name, "namespace": namespace, "uid": uid},
	}}
	return obj
}

func fleetRouter(reg *kube.Registry) http.Handler {
	r := chi.NewRouter()
	r.Use(rbac.Middleware(reg))
	MountFleet(r, reg, nil)
	return r
}

func TestFleetReadBudgetStopsBeforeDiscovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		home, remote := fleetTestClient(), fleetTestClient()
		reg := clusterTestRegistry(home)
		reg.Set("remote", remote)
		router := fleetRouter(reg)
		user := &auth.User{ID: 42}
		for i := range 10 {
			if rr := inventoryRequest(t, router, user, http.MethodGet, "/fleet/servers"); rr.Code != http.StatusOK {
				t.Fatalf("initial request %d: status=%d body=%s", i, rr.Code, rr.Body)
			}
		}
		homeBefore, remoteBefore := kubeClientCalls(t, home), kubeClientCalls(t, remote)
		for _, route := range []string{"servers", "backups", "schedules", "restores", "inventory", "placements"} {
			// A fresh request/user context models a different session for the same account.
			rr := inventoryRequest(t, router, &auth.User{ID: 42}, http.MethodGet, "/fleet/"+route)
			if rr.Code != http.StatusTooManyRequests || rr.Header().Get("Retry-After") != "1" {
				t.Fatalf("%s escaped shared user budget: status=%d retry=%q", route, rr.Code, rr.Header().Get("Retry-After"))
			}
		}
		if kubeClientCalls(t, home) != homeBefore || kubeClientCalls(t, remote) != remoteBefore {
			t.Fatal("rate-limited request reached Kubernetes discovery or resource listing")
		}
		// Same httptest client IP, different authenticated account.
		if rr := inventoryRequest(t, router, &auth.User{ID: 43}, http.MethodGet, "/fleet/servers"); rr.Code != http.StatusOK {
			t.Fatalf("another user inherited the exhausted budget: %d", rr.Code)
		}
		homeBefore, remoteBefore = kubeClientCalls(t, home), kubeClientCalls(t, remote)
		if rr := inventoryRequest(t, router, nil, http.MethodGet, "/fleet/servers"); rr.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated status=%d", rr.Code)
		}
		if kubeClientCalls(t, home) != homeBefore || kubeClientCalls(t, remote) != remoteBefore {
			t.Fatal("unauthenticated fleet request reached Kubernetes")
		}
		time.Sleep(time.Second)
		if rr := inventoryRequest(t, router, user, http.MethodGet, "/fleet/servers"); rr.Code != http.StatusOK {
			t.Fatalf("budget did not refill: %d", rr.Code)
		}
	})
}

func TestFleetReadBudgetAllowsDashboardPolling(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		router := fleetRouter(clusterTestRegistry(fleetTestClient()))
		user := &auth.User{ID: 42}
		for range 12 {
			for _, route := range []string{"servers", "backups", "inventory"} {
				if rr := inventoryRequest(t, router, user, http.MethodGet, "/fleet/"+route); rr.Code != http.StatusOK {
					t.Fatalf("five-second polling denied on %s: %d", route, rr.Code)
				}
			}
			time.Sleep(5 * time.Second)
		}
	})
}

func decodeFleet[T any](t *testing.T, router http.Handler, user *auth.User, path string) fleetResult[T] {
	t.Helper()
	rr := inventoryRequest(t, router, user, http.MethodGet, path)
	if rr.Code != http.StatusOK {
		t.Fatalf("%s: status=%d body=%s", path, rr.Code, rr.Body)
	}
	var result fleetResult[T]
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.TotalReturned != len(result.Items) {
		t.Fatalf("inconsistent result count: %+v", result)
	}
	return result
}

func TestFleetServers_PermissionsOwnershipAndIdentity(t *testing.T) {
	previous := scope.AllowedNamespaces
	scope.AllowedNamespaces = []string{scope.DefaultNamespace, "extra-games", scope.DefaultNamespace}
	t.Cleanup(func() { scope.AllowedNamespaces = previous })
	local := fleetObject("GameServer", scope.DefaultNamespace, "same", "local-uid")
	remote := fleetObject("GameServer", scope.DefaultNamespace, "same", "remote-uid")
	owned := fleetObject("GameServer", "extra-games", "owned", "owner-uid")
	owned.SetAnnotations(map[string]string{ownerIDAnnotation: "42"})
	collab := fleetObject("GameServer", "extra-games", "shared", "collaborator-uid")
	collab.SetAnnotations(map[string]string{collaboratorsAnnotation: "7, 42,99"})
	hidden := fleetObject("GameServer", "extra-games", "private", "private-uid")
	outside := fleetObject("GameServer", "outside-allowlist", "outside", "outside-uid")
	outside.SetAnnotations(map[string]string{ownerIDAnnotation: "42"})
	reg := clusterTestRegistry(fleetTestClient(local, newCluster("remote", nil, nil)))
	reg.Set("remote", fleetTestClient(remote, owned, collab, hidden, outside))
	u := inventoryUser("local", scope.DefaultNamespace, "*")
	u.Perms["remote"] = map[string]map[string]struct{}{scope.DefaultNamespace: {"servers:read": {}}}
	got := decodeFleet[fleetResource](t, fleetRouter(reg), u, "/fleet/servers")
	if got.Partial || len(got.Items) != 4 {
		t.Fatalf("wrong visible set: %+v", got)
	}
	byUID := map[string]fleetResource{}
	for _, item := range got.Items {
		byUID[item.Target.UID] = item
		if item.Target.Namespace != item.Resource.GetNamespace() || item.Target.Name != item.Resource.GetName() {
			t.Fatal("target does not match resource")
		}
	}
	if byUID["local-uid"].Target.Cluster != "local" || byUID["remote-uid"].Target.Cluster != "remote" {
		t.Fatal("same-name objects were conflated")
	}
	if !byUID["local-uid"].Access.CanDelete || byUID["remote-uid"].Access.CanControl || slices.Contains(byUID["remote-uid"].Permissions, "servers:write") {
		t.Fatal("permissions leaked across clusters")
	}
	if a := byUID["owner-uid"].Access; !a.IsOwner || !a.CanDelete || !a.CanControl || a.CanWrite {
		t.Fatalf("owner access widened/lost: %+v", a)
	}
	if a := byUID["collaborator-uid"].Access; !a.IsCollaborator || !a.CanConsole || a.CanDelete || a.CanWrite {
		t.Fatalf("collaborator access widened/lost: %+v", a)
	}
	filtered := decodeFleet[fleetResource](t, fleetRouter(reg), u, "/fleet/servers?cluster=remote&namespace=extra-games")
	if len(filtered.Items) != 2 {
		t.Fatalf("filter=%+v", filtered)
	}
	for _, item := range filtered.Items {
		if item.Target.Cluster != "remote" || item.Target.Namespace != "extra-games" {
			t.Fatal("filter returned another scope")
		}
	}
}

func TestFleetResources_ExactReadPermissions(t *testing.T) {
	for _, tc := range []struct{ path, kind, permission string }{
		{"backups", "Backup", "backups:read"}, {"schedules", "BackupSchedule", "schedules:read"}, {"restores", "Restore", "backups:read"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			obj := fleetObject(tc.kind, scope.DefaultNamespace, "same", "remote-uid")
			obj.SetAnnotations(map[string]string{ownerIDAnnotation: "42"})
			home := fleetTestClient(obj.DeepCopy(), newCluster("remote", nil, nil))
			remote := fleetTestClient(obj)
			reg := clusterTestRegistry(home)
			reg.Set("remote", remote)
			u := inventoryUser("remote", scope.DefaultNamespace, tc.permission)
			u.Perms["local"] = map[string]map[string]struct{}{scope.DefaultNamespace: {"servers:read": {}}}
			got := decodeFleet[fleetResource](t, fleetRouter(reg), u, "/fleet/"+tc.path)
			if got.Partial || len(got.Items) != 1 || got.Items[0].Target.Cluster != "remote" || got.Items[0].Access != nil {
				t.Fatalf("wrong permitted resource set: %+v", got)
			}
			if !slices.Contains(got.Items[0].Permissions, tc.permission) || slices.Contains(got.Items[0].Permissions, "servers:read") {
				t.Fatal("resource permissions merged from another cluster")
			}
			for _, action := range home.Dynamic.(*dynamicfake.FakeDynamicClient).Actions() {
				if action.GetResource() == kube.GVRs[tc.path] {
					t.Fatal("unauthorized home scope was queried")
				}
			}
			ownerOnly := decodeFleet[fleetResource](t, fleetRouter(reg), &auth.User{ID: 42}, "/fleet/"+tc.path)
			if len(ownerOnly.Items) != 0 || ownerOnly.Partial {
				t.Fatalf("server ownership granted unrelated resource reads: %+v", ownerOnly)
			}
		})
	}
}

func TestFleetPartialFailures_RetainHealthyDataWithoutLeakingErrors(t *testing.T) {
	local := fleetObject("GameServer", scope.DefaultNamespace, "healthy", "local-uid")
	home := fleetTestClient(local, newCluster("offline", nil, nil), newCluster("secret-cluster", nil, nil), newCluster("remote", nil, nil))
	remote := fleetTestClient()
	remote.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("list", "gameservers", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(kube.GVRs["servers"].GroupResource(), "private-resource", errors.New("credential=must-not-leak"))
	})
	reg := clusterTestRegistry(home)
	reg.Set("remote", remote)
	u := inventoryUser("local", scope.DefaultNamespace, "servers:read")
	u.Perms["remote"] = map[string]map[string]struct{}{scope.DefaultNamespace: {"servers:read": {}}}
	u.Perms["offline"] = map[string]map[string]struct{}{scope.DefaultNamespace: {"servers:read": {}}}
	rr := inventoryRequest(t, fleetRouter(reg), u, http.MethodGet, "/fleet/servers")
	if strings.Contains(rr.Body.String(), "credential=") || strings.Contains(rr.Body.String(), "secret-cluster") || strings.Contains(rr.Body.String(), "private-resource") {
		t.Fatalf("leaked error/hidden registration: %s", rr.Body)
	}
	var got fleetResult[fleetResource]
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Partial || len(got.Items) != 1 || got.Items[0].Target.Cluster != "local" {
		t.Fatalf("lost healthy result: %+v", got)
	}
	codes := map[string]string{}
	for _, issue := range got.Issues {
		codes[issue.Cluster] = issue.Code
	}
	if codes["remote"] != "forbidden" || codes["offline"] != "unavailable" {
		t.Fatalf("issues=%+v", got.Issues)
	}
}

func TestFleetPaginationLimitsAndStaleProjection(t *testing.T) {
	home := fleetTestClient()
	calls := 0
	home.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("list", "gameservers", func(action clienttesting.Action) (bool, runtime.Object, error) {
		calls++
		opts := action.(interface{ GetListOptions() metav1.ListOptions }).GetListOptions()
		if opts.Limit != fleetPageSize {
			t.Fatalf("unbounded page: %+v", opts)
		}
		list := &unstructured.UnstructuredList{}
		switch opts.Continue {
		case "":
			obj := fleetObject("GameServer", scope.DefaultNamespace, "a", "uid-a")
			obj.Object["status"] = map[string]any{"agent": map[string]any{"lastHeartbeat": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339), "playersOnline": int64(20), "cpuMillicores": int64(500)}}
			list.Items = []unstructured.Unstructured{*obj}
			list.SetContinue("next")
		case "next":
			list.Items = []unstructured.Unstructured{*fleetObject("GameServer", scope.DefaultNamespace, "b", "uid-b")}
		default:
			t.Fatalf("bad continuation: %q", opts.Continue)
		}
		return true, list, nil
	})
	reg := clusterTestRegistry(home)
	u := inventoryUser("local", scope.DefaultNamespace, "servers:read")
	got := decodeFleet[fleetResource](t, fleetRouter(reg), u, "/fleet/servers?limit=1")
	if calls != 2 || !got.Partial || len(got.Items) != 1 || got.Issues[0].Code != "limit" {
		t.Fatalf("pagination/limit: calls=%d result=%+v", calls, got)
	}
	if _, found, _ := unstructured.NestedInt64(got.Items[0].Resource.Object, "status", "agent", "playersOnline"); found {
		t.Fatal("stale player count survived aggregate")
	}
	if stale, _, _ := unstructured.NestedBool(got.Items[0].Resource.Object, "status", "agent", "stale"); !stale {
		t.Fatal("missing stale marker")
	}
	budget := atomic.Int64{}
	budget.Store(0)
	limited := (fleetHandler{reg: reg}).readScope(t.Context(), u, "servers", fleetScope{cluster: fleetCluster{id: "local", k: home}, namespace: scope.DefaultNamespace, granted: true}, &budget)
	if !limited.partial || limited.issue.Code != "limit" || calls != 2 {
		t.Fatal("scan budget did not stop before API read")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	budget.Store(fleetMaxScan)
	stopped := (fleetHandler{reg: reg}).readScope(ctx, u, "servers", fleetScope{cluster: fleetCluster{id: "local", k: home}, namespace: scope.DefaultNamespace, granted: true}, &budget)
	if !stopped.partial || stopped.issue.Code != "unavailable" || calls != 2 {
		t.Fatal("canceled scope continued reading")
	}
}

func TestFleetInventory_SeparateAuthorityAndUnknownMetrics(t *testing.T) {
	home := fleetTestClient(newCluster("remote", map[string]any{"displayName": "Remote site"}, nil), newCluster("offline", nil, nil))
	home.Typed = kubefake.NewClientset(readyNode("local-node", true, "4", "8Gi"), boundPV("local-pv", "2Gi", true))
	remote := fleetTestClient()
	remote.Typed = kubefake.NewClientset(readyNode("remote-node", true, "8", "16Gi"), boundPV("remote-pv", "7Gi", true))
	reg := clusterTestRegistry(home)
	reg.Set("remote", remote)
	u := inventoryUser("remote", "*", "cluster:read")
	u.Perms["local"] = map[string]map[string]struct{}{scope.DefaultNamespace: {"*": {}}}
	u.Perms["offline"] = map[string]map[string]struct{}{"*": {"cluster:read": {}}}
	got := decodeFleet[fleetInventory](t, fleetRouter(reg), u, "/fleet/inventory")
	if !got.Partial || len(got.Items) != 1 || got.Items[0].Cluster != "remote" || got.Items[0].Name != "Remote site" {
		t.Fatalf("inventory=%+v", got)
	}
	item := got.Items[0]
	if item.View.Nodes[0].Name != "remote-node" || item.Stats.UsedStorageBytes != 7<<30 || item.View.Nodes[0].CPU.Used != nil || item.View.Nodes[0].Memory.Used != nil {
		t.Fatalf("wrong scope/unknown usage: %+v", item)
	}
	if len(home.Typed.(*kubefake.Clientset).Actions()) != 0 {
		t.Fatal("namespace wildcard read home node inventory")
	}
	if got.Issues[0].Cluster != "offline" || got.Issues[0].Code != "unavailable" {
		t.Fatalf("offline inventory vanished: %+v", got.Issues)
	}
	denied := decodeFleet[fleetInventory](t, fleetRouter(reg), &auth.User{ID: 42}, "/fleet/inventory")
	if denied.Partial || len(denied.Items) != 0 {
		t.Fatalf("unrelated cluster failure leaked to caller: %+v", denied)
	}
}

func TestFleetPlacements_RequireCreateAndTemplateRead(t *testing.T) {
	template := fleetObject("GameTemplate", "", "demo-game", "remote-template")
	home := fleetTestClient(newCluster("remote", nil, nil), newCluster("offline", nil, nil), template.DeepCopy())
	reg := clusterTestRegistry(home)
	reg.Set("remote", fleetTestClient(template))
	u := inventoryUser("remote", "*", "templates:read")
	u.Perms["remote"][scope.DefaultNamespace] = map[string]struct{}{"servers:write": {}}
	u.Perms["offline"] = map[string]map[string]struct{}{"*": {"templates:read": {}}, scope.DefaultNamespace: {"servers:write": {}}}
	got := decodeFleet[fleetPlacement](t, fleetRouter(reg), u, "/fleet/placements")
	if !got.Partial || len(got.Items) != 1 || got.Items[0].Cluster != "remote" || got.Items[0].Namespace != scope.DefaultNamespace || string(got.Items[0].Templates[0].GetUID()) != "remote-template" {
		t.Fatalf("placements=%+v", got)
	}
	for _, action := range home.Dynamic.(*dynamicfake.FakeDynamicClient).Actions() {
		if action.GetResource() == kube.GVRs["templates"] {
			t.Fatal("templates read on ineligible home cluster")
		}
	}
	noRead := decodeFleet[fleetPlacement](t, fleetRouter(reg), inventoryUser("remote", scope.DefaultNamespace, "servers:write"), "/fleet/placements")
	if len(noRead.Items) != 0 || noRead.Partial {
		t.Fatalf("missing template permission granted placement: %+v", noRead)
	}
	noCreate := decodeFleet[fleetPlacement](t, fleetRouter(reg), inventoryUser("local", "*", "templates:read"), "/fleet/placements")
	if len(noCreate.Items) != 0 || noCreate.Partial {
		t.Fatalf("template reader gained create placement: %+v", noCreate)
	}
	wrongTarget := inventoryUser("local", "*", "templates:read")
	wrongTarget.Perms["remote"] = map[string]map[string]struct{}{scope.DefaultNamespace: {"servers:write": {}}}
	denied := decodeFleet[fleetPlacement](t, fleetRouter(reg), wrongTarget, "/fleet/placements")
	if len(denied.Items) != 0 || denied.Partial {
		t.Fatalf("local template grant authorized remote placement: %+v", denied)
	}
}

func TestFleetExactServerAccess_IsTargetScoped(t *testing.T) {
	obj := fleetObject("GameServer", scope.DefaultNamespace, "same", "remote-uid")
	obj.SetAnnotations(map[string]string{ownerIDAnnotation: "42"})
	reg := clusterTestRegistry(fleetTestClient(obj.DeepCopy(), newCluster("remote", nil, nil)))
	reg.Set("remote", fleetTestClient(obj))
	u := inventoryUser("local", "*", "*")
	rr := inventoryRequest(t, fleetRouter(reg), u, http.MethodGet, "/servers/same/access?cluster=remote&namespace="+scope.DefaultNamespace)
	if rr.Code != http.StatusOK {
		t.Fatalf("access status=%d body=%s", rr.Code, rr.Body)
	}
	var got struct {
		fleetServerAccess
		Target      fleetTarget
		Permissions []string
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.IsOwner || got.CanWrite || !got.CanDelete || got.Target.Cluster != "remote" || got.Target.UID != "remote-uid" || len(got.Permissions) != 0 {
		t.Fatalf("wrong target access: %+v", got)
	}
	u.ID = 99
	denied := inventoryRequest(t, fleetRouter(reg), u, http.MethodGet, "/servers/same/access?cluster=remote")
	if denied.Code != http.StatusForbidden {
		t.Fatalf("home admin accessed remote=%d", denied.Code)
	}
	reader := inventoryUser("remote", scope.DefaultNamespace, "servers:read")
	reader.ID = 99
	rr = inventoryRequest(t, fleetRouter(reg), reader, http.MethodGet, "/servers/same/access?cluster=remote")
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if rr.Code != http.StatusOK || got.CanControl || got.CanDelete || !reflect.DeepEqual(got.Permissions, []string{"servers:read"}) {
		t.Fatalf("read-only access=%+v status=%d", got, rr.Code)
	}
}

func TestFleetAuthFiltersAndDiscoveryFailures(t *testing.T) {
	reg := clusterTestRegistry(fleetTestClient())
	r := fleetRouter(reg)
	u := inventoryUser("local", "*", "*")
	for _, path := range []string{"servers", "backups", "schedules", "restores", "inventory", "placements"} {
		if rr := inventoryRequest(t, r, nil, http.MethodGet, "/fleet/"+path); rr.Code != http.StatusUnauthorized {
			t.Fatalf("unauth %s=%d", path, rr.Code)
		}
		if rr := inventoryRequest(t, r, u, http.MethodPost, "/fleet/"+path); rr.Code != http.StatusForbidden {
			t.Fatalf("fleet write %s=%d", path, rr.Code)
		}
	}
	for _, query := range []string{"limit=0", "limit=2001", "limit=no", "limit=1&limit=2", "cluster=*", "cluster=a&cluster=b", "namespace=outside"} {
		if rr := inventoryRequest(t, r, u, http.MethodGet, "/fleet/servers?"+query); rr.Code != http.StatusBadRequest {
			t.Fatalf("invalid filter %s=%d", query, rr.Code)
		}
	}
	if rr := inventoryRequest(t, r, u, http.MethodGet, "/fleet/inventory?namespace="+scope.DefaultNamespace); rr.Code != http.StatusBadRequest {
		t.Fatalf("namespaced inventory=%d", rr.Code)
	}
	unknown := decodeFleet[fleetResource](t, r, u, "/fleet/servers?cluster=unknown")
	if len(unknown.Items) != 0 || unknown.Partial {
		t.Fatalf("unknown filter=%+v", unknown)
	}
	for _, tc := range []struct {
		name    string
		err     error
		partial bool
	}{
		{"optional CRD absent", apierrors.NewNotFound(kube.GVRCluster.GroupResource(), ""), false},
		{"discovery forbidden", apierrors.NewForbidden(kube.GVRCluster.GroupResource(), "", errors.New("private")), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := fleetTestClient(fleetObject("GameServer", scope.DefaultNamespace, "local", "local-uid"))
			home.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("list", "clusters", func(clienttesting.Action) (bool, runtime.Object, error) { return true, nil, tc.err })
			got := decodeFleet[fleetResource](t, fleetRouter(clusterTestRegistry(home)), u, "/fleet/servers")
			if got.Partial != tc.partial || len(got.Items) != 1 {
				t.Fatalf("discovery fallback=%+v", got)
			}
		})
	}
}

func TestFleetBoundedPages_RejectsRepeatingContinuation(t *testing.T) {
	home := fleetTestClient()
	calls := 0
	home.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("list", "gameservers", func(clienttesting.Action) (bool, runtime.Object, error) {
		calls++
		list := &unstructured.UnstructuredList{Items: []unstructured.Unstructured{*fleetObject("GameServer", scope.DefaultNamespace, "same", "uid")}}
		list.SetContinue("repeated")
		return true, list, nil
	})
	got := decodeFleet[fleetResource](t, fleetRouter(clusterTestRegistry(home)), inventoryUser("local", "*", "servers:read"), "/fleet/servers")
	if calls != 2 || !got.Partial || len(got.Items) != 1 || got.Issues[0].Code != "limit" {
		t.Fatalf("repeating pagination not bounded: %+v calls=%d", got, calls)
	}
}

func TestFleetScopes_EnableFilteringBeyondTruncatedItems(t *testing.T) {
	home := fleetTestClient(fleetObject("GameServer", scope.DefaultNamespace, "local", "local-uid"), newCluster("remote", nil, nil), newCluster("empty", nil, nil), newCluster("hidden", nil, nil))
	reg := clusterTestRegistry(home)
	reg.Set("remote", fleetTestClient(fleetObject("GameServer", scope.DefaultNamespace, "remote", "remote-uid")))
	reg.Set("empty", fleetTestClient())
	u := inventoryUser("local", "*", "servers:read")
	u.Perms["remote"] = map[string]map[string]struct{}{"*": {"servers:read": {}}}
	u.Perms["empty"] = map[string]map[string]struct{}{"*": {"servers:read": {}}}
	r := fleetRouter(reg)
	all := decodeFleet[fleetResource](t, r, u, "/fleet/servers?limit=1")
	if !all.Partial || len(all.Items) != 1 || all.Items[0].Target.Cluster != "local" {
		t.Fatalf("initial cap=%+v", all)
	}
	want := []fleetScopeView{{Cluster: "empty", Namespace: scope.DefaultNamespace}, {Cluster: "local", Namespace: scope.DefaultNamespace}, {Cluster: "remote", Namespace: scope.DefaultNamespace}}
	if !reflect.DeepEqual(all.Scopes, want) {
		t.Fatalf("scopes=%+v want=%+v", all.Scopes, want)
	}
	filtered := decodeFleet[fleetResource](t, r, u, "/fleet/servers?cluster=remote&limit=1")
	if filtered.Partial || len(filtered.Items) != 1 || filtered.Items[0].Target.UID != "remote-uid" {
		t.Fatalf("filtered omitted result=%+v", filtered)
	}
	owner := fleetObject("GameServer", scope.DefaultNamespace, "owned", "owner-uid")
	owner.SetAnnotations(map[string]string{ownerIDAnnotation: "99"})
	reg.Set("remote", fleetTestClient(owner))
	owned := decodeFleet[fleetResource](t, r, &auth.User{ID: 99}, "/fleet/servers")
	if !reflect.DeepEqual(owned.Scopes, []fleetScopeView{{Cluster: "remote", Namespace: scope.DefaultNamespace}}) {
		t.Fatalf("owner scopes disclosed unrelated registrations: %+v", owned.Scopes)
	}
}

func TestFleetExactOwnerFilter_DoesNotRevealUnavailableRegistration(t *testing.T) {
	reg := clusterTestRegistry(fleetTestClient(newCluster("unrelated-offline", nil, nil)))
	r := fleetRouter(reg)
	u := &auth.User{ID: 42}
	unknown := inventoryRequest(t, r, u, http.MethodGet, "/fleet/servers?cluster=unknown")
	offline := inventoryRequest(t, r, u, http.MethodGet, "/fleet/servers?cluster=unrelated-offline")
	if unknown.Code != http.StatusOK || offline.Code != unknown.Code || offline.Body.String() != unknown.Body.String() {
		t.Fatalf("unrelated unavailable registration distinguishable: unknown=%d %s offline=%d %s", unknown.Code, unknown.Body, offline.Code, offline.Body)
	}
	granted := decodeFleet[fleetResource](t, r, inventoryUser("unrelated-offline", "*", "servers:read"), "/fleet/servers?cluster=unrelated-offline")
	if !granted.Partial || len(granted.Issues) != 1 || granted.Issues[0].Cluster != "unrelated-offline" {
		t.Fatalf("authorized failure was hidden: %+v", granted)
	}
	remote := fleetTestClient()
	remote.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("list", "gameservers", func(clienttesting.Action) (bool, runtime.Object, error) {
		owned := fleetObject("GameServer", scope.DefaultNamespace, "owned", "owned-uid")
		owned.SetAnnotations(map[string]string{ownerIDAnnotation: "42"})
		list := &unstructured.UnstructuredList{Items: []unstructured.Unstructured{*owned}}
		list.SetContinue("repeated")
		return true, list, nil
	})
	reg.Set("owned", remote)
	owned := decodeFleet[fleetResource](t, r, u, "/fleet/servers?cluster=owned")
	if !owned.Partial || len(owned.Items) != 1 || owned.Issues[0].Cluster != "owned" {
		t.Fatalf("owned partial result was hidden: %+v", owned)
	}
}

func TestFleetScopeLimitAndTemplatePagination(t *testing.T) {
	reg := clusterTestRegistry(fleetTestClient())
	for i := 0; i < fleetMaxScopes; i++ {
		reg.Set(fmt.Sprintf("remote-%03d", i), nil)
	}
	got := decodeFleet[fleetResource](t, fleetRouter(reg), inventoryUser("*", "*", "servers:read"), "/fleet/servers")
	if !got.Partial || len(got.Issues) == 0 || got.Issues[0].Code != "limit" {
		t.Fatalf("scope cap missing: %+v", got)
	}
	home := fleetTestClient()
	calls := 0
	home.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("list", "gametemplates", func(clienttesting.Action) (bool, runtime.Object, error) {
		calls++
		list := &unstructured.UnstructuredList{Items: []unstructured.Unstructured{*fleetObject("GameTemplate", "", fmt.Sprintf("template-%d", calls), "template-uid")}}
		if calls == 1 {
			list.SetContinue("next")
		}
		return true, list, nil
	})
	placements := decodeFleet[fleetPlacement](t, fleetRouter(clusterTestRegistry(home)), inventoryUser("local", "*", "*"), "/fleet/placements")
	if placements.Partial || len(placements.Items) != 1 || len(placements.Items[0].Templates) != 2 || calls != 2 {
		t.Fatalf("template pagination=%+v calls=%d", placements, calls)
	}
}
