package rbac

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

const faLocal = "local"

// faFakeCluster is an in-memory ServerFetcher and ServerLister for one cluster.
type faFakeCluster struct {
	ids     []string
	servers map[string]*unstructured.Unstructured // key: namespace/name
}

func (f *faFakeCluster) GetServer(_ context.Context, _, ns, name string) (*unstructured.Unstructured, error) {
	obj, ok := f.servers[ns+"/"+name]
	if !ok {
		return nil, errors.New("not found")
	}
	return obj, nil
}

func (f *faFakeCluster) IDs() []string { return f.ids }

func (f *faFakeCluster) ListServers(_ context.Context, _, ns string) ([]unstructured.Unstructured, error) {
	var out []unstructured.Unstructured
	for key, obj := range f.servers {
		if strings.HasPrefix(key, ns+"/") {
			out = append(out, *obj)
		}
	}
	return out, nil
}

// faServer builds a GameServer carrying the ownership annotations ownershipRole reads.
func faServer(ns, name string, owner int64, collaborators string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetNamespace(ns)
	obj.SetName(name)
	obj.SetUID(types.UID(name + "-uid"))
	ann := map[string]string{"gameplane.local/owner-id": fmt.Sprint(owner)}
	if collaborators != "" {
		ann["gameplane.local/collaborators"] = collaborators
	}
	obj.SetAnnotations(ann)
	return obj
}

func faCluster(ids []string, servers ...*unstructured.Unstructured) *faFakeCluster {
	f := &faFakeCluster{ids: ids, servers: map[string]*unstructured.Unstructured{}}
	for _, obj := range servers {
		f.servers[obj.GetNamespace()+"/"+obj.GetName()] = obj
	}
	return f
}

// faUser returns a user holding perms on cluster/ns. With no perms it holds nothing.
func faUser(id int64, cluster, ns string, perms ...string) *auth.User {
	u := &auth.User{ID: id, Username: fmt.Sprintf("user%d", id)}
	if len(perms) > 0 {
		set := map[string]struct{}{}
		for _, p := range perms {
			set[p] = struct{}{}
		}
		u.Perms = map[string]map[string]map[string]struct{}{cluster: {ns: set}}
	}
	return u
}

// faBare returns a user holding no permissions (only ownership grants can apply).
func faBare(id int64) *auth.User {
	return &auth.User{ID: id, Username: fmt.Sprintf("user%d", id)}
}

// faFixture: srv is owned by user 7 with user 8 as collaborator; other is owned by user 99.
func faFixture() *faFakeCluster {
	return faCluster([]string{faLocal},
		faServer(scope.DefaultNamespace, "srv", 7, "8"),
		faServer(scope.DefaultNamespace, "other", 99, ""),
	)
}

func faTarget() FileTarget {
	return FileTarget{Cluster: faLocal, Namespace: scope.DefaultNamespace, Server: "srv"}
}

func TestFileAccessPermissionMatrix(t *testing.T) {
	fetch := faFixture()
	files := NewFileAccess(fetch, fetch)
	ns := scope.DefaultNamespace
	readActions := []FileAction{FileList, FileStat, FileRead, FileDownload}
	writeActions := []FileAction{FileCreate, FileWrite, FileUpload, FileMkdir, FileDelete}
	cases := []struct {
		name        string
		user        *auth.User
		read, write bool
	}{
		{"read-only", faUser(1, faLocal, ns, "servers:read"), true, false},
		{"write-only", faUser(2, faLocal, ns, "servers:write"), false, true},
		{"no grant", faBare(9), false, false},
		{"other namespace", faUser(3, faLocal, "other-ns", "servers:read", "servers:write"), false, false},
		{"cluster-wide wildcard", faUser(4, "*", "*", "servers:read", "servers:write"), true, true},
		{"owner", faBare(7), true, true},
		{"collaborator", faBare(8), true, true},
	}
	for _, tc := range cases {
		for _, action := range readActions {
			if err := files.Allow(t.Context(), tc.user, action, faTarget()); (err == nil) != tc.read {
				t.Errorf("%s %s: allowed=%v, want %v (err %v)", tc.name, action, err == nil, tc.read, err)
			}
		}
		for _, action := range writeActions {
			if err := files.Allow(t.Context(), tc.user, action, faTarget()); (err == nil) != tc.write {
				t.Errorf("%s %s: allowed=%v, want %v (err %v)", tc.name, action, err == nil, tc.write, err)
			}
		}
	}
}

func TestFileAccessNilUserAndUnknownAction(t *testing.T) {
	fetch := faFixture()
	files := NewFileAccess(fetch, fetch)
	if err := files.Allow(t.Context(), nil, FileList, faTarget()); !errors.Is(err, ErrFileAccessDenied) {
		t.Errorf("nil user: err = %v, want ErrFileAccessDenied", err)
	}
	user := faUser(1, faLocal, scope.DefaultNamespace, "servers:read", "servers:write")
	if err := files.Allow(t.Context(), user, FileAction("bogus"), faTarget()); !errors.Is(err, ErrFileAccessDenied) {
		t.Errorf("unknown action: err = %v, want ErrFileAccessDenied", err)
	}
}

func TestFileAccessRenameNeedsBothPaths(t *testing.T) {
	fetch := faFixture()
	files := NewFileAccess(fetch, fetch)
	ctx := t.Context()
	writer := faUser(2, faLocal, scope.DefaultNamespace, "servers:write")
	reader := faUser(1, faLocal, scope.DefaultNamespace, "servers:read")

	// Route-level check: no paths yet, so write-class access decides.
	if err := files.Allow(ctx, writer, FileRename, faTarget()); err != nil {
		t.Errorf("writer route-level rename: %v", err)
	}
	if err := files.Allow(ctx, reader, FileRename, faTarget()); err == nil {
		t.Error("reader route-level rename allowed, want denied")
	}

	two := faTarget()
	two.Paths = []string{"a.txt", "b.txt"}
	if err := files.Allow(ctx, writer, FileRename, two); err != nil {
		t.Errorf("writer rename with two paths: %v", err)
	}
	if err := files.Allow(ctx, reader, FileRename, two); err == nil {
		t.Error("reader rename with two paths allowed, want denied")
	}

	one := faTarget()
	one.Paths = []string{"a.txt"}
	if err := files.Allow(ctx, writer, FileRename, one); !errors.Is(err, ErrFileAccessDenied) {
		t.Errorf("rename with one path: err = %v, want ErrFileAccessDenied", err)
	}
}

func TestFileAccessBoundGrantContext(t *testing.T) {
	fetch := faFixture()
	files := NewFileAccess(fetch, fetch)

	ctx, err := files.AllowBound(t.Context(), faBare(7), FileWrite, faTarget())
	if err != nil {
		t.Fatalf("owner AllowBound: %v", err)
	}
	id, ok := BoundServerIdentity(ctx)
	if !ok || id.Cluster != faLocal || id.Namespace != scope.DefaultNamespace || id.Name != "srv" || id.UID != "srv-uid" {
		t.Errorf("owner bound identity = %+v, %v", id, ok)
	}

	// A grant from the namespace permission binds no server identity.
	ctx, err = files.AllowBound(t.Context(), faUser(1, faLocal, scope.DefaultNamespace, "servers:read"), FileRead, faTarget())
	if err != nil {
		t.Fatalf("reader AllowBound: %v", err)
	}
	if _, ok := BoundServerIdentity(ctx); ok {
		t.Error("permission grant bound a server identity, want none")
	}
}

func TestFileAccessRemoteClusterIsNotGranted(t *testing.T) {
	fetch := faFixture()
	files := NewFileAccess(fetch, fetch)
	reader := faUser(1, faLocal, scope.DefaultNamespace, "servers:read")
	remote := faTarget()
	remote.Cluster = "remote"
	if err := files.Allow(t.Context(), reader, FileList, remote); err == nil {
		t.Error("grant on local allowed a file operation on remote")
	}
	if _, err := files.VisibleServers(t.Context(), reader, "remote"); !errors.Is(err, scope.ErrForbiddenCluster) {
		t.Errorf("VisibleServers(remote) err = %v, want ErrForbiddenCluster", err)
	}
}

func TestFileAccessOtherNamespaceGrantDoesNotCrossOver(t *testing.T) {
	fetch := faFixture()
	files := NewFileAccess(fetch, fetch)
	grantedElsewhere := faUser(1, faLocal, "other-ns", "servers:read", "servers:write")
	if err := files.Allow(t.Context(), grantedElsewhere, FileList, faTarget()); err == nil {
		t.Error("grant in other-ns allowed a file operation in the default namespace")
	}
}

func TestVisibleServers(t *testing.T) {
	ns := scope.DefaultNamespace
	fetch := faCluster([]string{faLocal},
		faServer(ns, "srv", 7, "8"),
		faServer(ns, "other", 99, ""),
		faServer(ns, "alpha", 7, ""),
	)
	files := NewFileAccess(fetch, fetch)
	cases := []struct {
		name string
		user *auth.User
		want string
	}{
		{"namespace reader sees the whole namespace", faUser(1, faLocal, ns, "servers:read"), "alpha,other,srv"},
		{"owner sees owned servers", faBare(7), "alpha,srv"},
		{"collaborator sees collaborated servers", faBare(8), "srv"},
		{"no grant sees nothing", faBare(9), ""},
	}
	for _, tc := range cases {
		refs, err := files.VisibleServers(t.Context(), tc.user, faLocal)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		var names []string
		for _, r := range refs {
			if r.Cluster != faLocal || r.Namespace != ns {
				t.Errorf("%s: ref = %+v", tc.name, r)
			}
			names = append(names, r.Name)
		}
		if got := strings.Join(names, ","); got != tc.want {
			t.Errorf("%s: names = %q, want %q", tc.name, got, tc.want)
		}
	}
	if _, err := files.VisibleServers(t.Context(), nil, faLocal); !errors.Is(err, ErrFileAccessDenied) {
		t.Errorf("nil user: err = %v, want ErrFileAccessDenied", err)
	}
}

func TestVisibleServersRequiresLister(t *testing.T) {
	fetch := faFixture()
	files := NewFileAccess(fetch, nil)
	if _, err := files.VisibleServers(t.Context(), faUser(1, faLocal, scope.DefaultNamespace, "servers:read"), faLocal); err == nil {
		t.Error("VisibleServers without a lister returned no error")
	}
}

func TestFilterEntries(t *testing.T) {
	fetch := faFixture()
	files := NewFileAccess(fetch, fetch)
	names := []string{"world", "server.properties"}
	reader := faUser(1, faLocal, scope.DefaultNamespace, "servers:read")
	got, err := files.FilterEntries(t.Context(), reader, faTarget(), names)
	if err != nil || len(got) != len(names) || got[0] != "world" || got[1] != "server.properties" {
		t.Errorf("FilterEntries(reader) = %v, %v", got, err)
	}
	if _, err := files.FilterEntries(t.Context(), faBare(9), faTarget(), names); !errors.Is(err, ErrFileAccessDenied) {
		t.Errorf("FilterEntries(no grant) err = %v, want ErrFileAccessDenied", err)
	}
}

func TestFileActionForRoute(t *testing.T) {
	cases := []struct {
		method, path string
		want         FileAction
		ok           bool
	}{
		{http.MethodGet, "/servers/srv/files/list", FileList, true},
		{http.MethodGet, "/servers/srv/files/stat", FileStat, true},
		{http.MethodGet, "/servers/srv/files/read", FileRead, true},
		{http.MethodGet, "/servers/srv/files/download", FileDownload, true},
		{http.MethodGet, "/servers/srv/files/archive", FileDownload, true},
		{http.MethodPost, "/servers/srv/files/archive", FileWrite, true},
		{http.MethodPost, "/servers/srv/files/write", FileWrite, true},
		{http.MethodPost, "/servers/srv/files/create", FileCreate, true},
		{http.MethodPost, "/servers/srv/files/upload", FileUpload, true},
		{http.MethodPost, "/servers/srv/files/mkdir", FileMkdir, true},
		{http.MethodPost, "/servers/srv/files/rename", FileRename, true},
		{http.MethodDelete, "/servers/srv/files/delete", FileDelete, true},
		{http.MethodPut, "/servers/srv/files/stream", FileUpload, true},
		// Not file routes: the rule table keeps deciding these.
		{http.MethodPut, "/servers/srv/files/create", "", false},
		{http.MethodGet, "/servers/srv/files/stream", "", false},
		{http.MethodDelete, "/servers/srv/files/list", "", false},
		{http.MethodGet, "/servers/srv", "", false},
		{http.MethodGet, "/servers/srv/players", "", false},
		{http.MethodGet, "/servers/srv/files/list/extra", "", false},
	}
	for _, tc := range cases {
		action, server, ok := FileActionForRoute(tc.method, tc.path)
		if ok != tc.ok || action != tc.want {
			t.Errorf("%s %s = (%q, %v), want (%q, %v)", tc.method, tc.path, action, ok, tc.want, tc.ok)
		}
		if ok && server != "srv" {
			t.Errorf("%s %s server = %q, want srv", tc.method, tc.path, server)
		}
	}
}

// TestFileAccessMatchesRuleTable checks that every file route gives the same answer as the rule
// table with the owner/collaborator fallback that rbac.Middleware applied before this change.
func TestFileAccessMatchesRuleTable(t *testing.T) {
	fetch := faFixture()
	files := NewFileAccess(fetch, fetch)
	ns := scope.DefaultNamespace
	routes := []struct{ method, path string }{
		{http.MethodGet, "/servers/srv/files/list"},
		{http.MethodGet, "/servers/srv/files/stat"},
		{http.MethodGet, "/servers/srv/files/read"},
		{http.MethodGet, "/servers/srv/files/download"},
		{http.MethodGet, "/servers/srv/files/archive"},
		{http.MethodPost, "/servers/srv/files/archive"},
		{http.MethodPost, "/servers/srv/files/write"},
		{http.MethodPost, "/servers/srv/files/create"},
		{http.MethodPost, "/servers/srv/files/upload"},
		{http.MethodPost, "/servers/srv/files/mkdir"},
		{http.MethodPost, "/servers/srv/files/rename"},
		{http.MethodDelete, "/servers/srv/files/delete"},
		{http.MethodPut, "/servers/srv/files/stream"},
	}
	users := []*auth.User{
		faUser(1, faLocal, ns, "servers:read"),
		faUser(2, faLocal, ns, "servers:write"),
		faBare(7),
		faBare(8),
		faBare(9),
		faUser(5, "*", "*", "servers:read", "servers:write"),
	}
	for _, rt := range routes {
		action, server, ok := FileActionForRoute(rt.method, rt.path)
		if !ok {
			t.Fatalf("%s %s is not a file route", rt.method, rt.path)
		}
		target := FileTarget{Cluster: faLocal, Namespace: ns, Server: server}
		for _, u := range users {
			old := allow(u, rt.method, rt.path, faLocal, ns)
			if !old {
				_, old = bindServerGrant(t.Context(), fetch, faLocal, ns, server, u.ID, false)
			}
			got := files.Allow(t.Context(), u, action, target) == nil
			if old != got {
				t.Errorf("%s %s user %d: rule table %v, FileAccess %v", rt.method, rt.path, u.ID, old, got)
			}
		}
	}
}

func TestMiddlewareWithFilesRoutes(t *testing.T) {
	fetch := faFixture()
	ns := scope.DefaultNamespace
	mw := MiddlewareWithFiles(fetch, NewFileAccess(fetch, fetch))
	called := false
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	serve := func(u *auth.User, method, path string) bool {
		t.Helper()
		called = false
		req := httptest.NewRequestWithContext(t.Context(), method, path, nil)
		req = req.WithContext(auth.WithUser(req.Context(), u))
		h.ServeHTTP(httptest.NewRecorder(), req)
		return called
	}
	if !serve(faUser(1, faLocal, ns, "servers:read"), http.MethodGet, "/servers/srv/files/list") {
		t.Error("reader GET list was refused")
	}
	if serve(faUser(1, faLocal, ns, "servers:read"), http.MethodPost, "/servers/srv/files/write") {
		t.Error("reader POST write was allowed")
	}
	if !serve(faBare(7), http.MethodPost, "/servers/srv/files/write") {
		t.Error("owner POST write was refused")
	}
	if serve(faBare(9), http.MethodGet, "/servers/srv/files/list") {
		t.Error("user with no grant GET list was allowed")
	}
}
