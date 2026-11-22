//go:build envtest

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/GameplanePanel/gameplane/api/internal/audit"
	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/rbac"
	"github.com/GameplanePanel/gameplane/api/internal/registry"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

const modpackControlNS = "gameplane-system"

type modpackEnv struct {
	router  http.Handler
	home    *kube.Client // envtest dynamic + fake typed (secrets)
	files   *fakeAgentFiles
	auditor *audit.Auditor
	tmpl    string // template name
}

// newModpackEnv mounts the registry routes for a user holding perms in the
// default namespace, creates the template, and returns the harness.
func newModpackEnv(t *testing.T, perms ...string) *modpackEnv {
	t.Helper()
	home := &kube.Client{Dynamic: kubeC.Dynamic, Typed: k8sfake.NewSimpleClientset(), Config: cfg}
	clients := kube.NewRegistry(scope.DefaultCluster)
	clients.Set(scope.DefaultCluster, home)
	set := map[string]struct{}{}
	for _, p := range perms {
		set[p] = struct{}{}
	}
	user := &auth.User{ID: 99, Username: "modpack-tester",
		Perms: map[string]map[string]map[string]struct{}{scope.DefaultCluster: {scope.DefaultNamespace: set}}}
	env := &modpackEnv{home: home, files: &fakeAgentFiles{files: map[string]string{}}, auditor: audit.New(captureAuditStore)}
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(auth.WithUser(req.Context(), user)))
		})
	})
	r.Use(rbac.Middleware(clients))
	MountRegistryWithOptions(r, clients, fakeSet{p: &fakeProvider{}}, RegistryOptions{
		Audit: env.auditor, Home: home, ControlNS: modpackControlNS, Files: env.files,
	})
	env.router = r

	env.tmpl = uniqueResourceName("mp-tmpl")
	if _, err := kubeC.Dynamic.Resource(gvrTemplates()).Create(context.Background(), modpackTestTemplate(env.tmpl), metav1.CreateOptions{}); err != nil {
		t.Fatalf("create template: %v", err)
	}
	t.Cleanup(func() {
		_ = kubeC.Dynamic.Resource(gvrTemplates()).Delete(context.Background(), env.tmpl, metav1.DeleteOptions{})
	})
	return env
}

// server creates a GameServer on the env's template. mutate may add spec fields
// or annotations before create. Returns the stored object (generation set).
func (e *modpackEnv) server(t *testing.T, version string, mutate func(*unstructured.Unstructured)) *unstructured.Unstructured {
	t.Helper()
	name := uniqueResourceName("mp-srv")
	gs := serverWithVersion(scope.DefaultNamespace, name, e.tmpl, version)
	if mutate != nil {
		mutate(gs)
	}
	res := kubeC.Dynamic.Resource(gvrServers()).Namespace(scope.DefaultNamespace)
	created, err := res.Create(context.Background(), gs, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	t.Cleanup(func() { _ = res.Delete(context.Background(), name, metav1.DeleteOptions{}) })
	return created
}

func setServerStatus(t *testing.T, name string, status map[string]any) {
	t.Helper()
	res := kubeC.Dynamic.Resource(gvrServers()).Namespace(scope.DefaultNamespace)
	cur, err := res.Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get server: %v", err)
	}
	if err := unstructured.SetNestedField(cur.Object, status, "status"); err != nil {
		t.Fatalf("set status: %v", err)
	}
	if _, err := res.UpdateStatus(context.Background(), cur, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("update status: %v", err)
	}
}

func getServerObj(t *testing.T, name string) *unstructured.Unstructured {
	t.Helper()
	return getDynamic(t, gvrServers(), scope.DefaultNamespace, name)
}

// modpackStatus builds status{phase, modpack{phase, observedGeneration, applied}}.
func modpackStatus(phase string, observedGeneration int64, applied map[string]any) map[string]any {
	mp := map[string]any{"phase": phase, "observedGeneration": observedGeneration}
	if applied != nil {
		mp["applied"] = applied
	}
	return map[string]any{"phase": "Running", "modpack": mp}
}

func (e *modpackEnv) addDestination(t *testing.T) {
	t.Helper()
	sec := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "dest", Namespace: scope.DefaultNamespace,
		Labels: map[string]string{destinationLabel: "true"}}}
	if _, err := e.home.Typed.CoreV1().Secrets(scope.DefaultNamespace).Create(context.Background(), sec, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create destination: %v", err)
	}
}

// addKeySecret creates the managed CurseForge key Secret (Data is set directly:
// the fake clientset does not convert StringData).
func (e *modpackEnv) addKeySecret(t *testing.T, key string, shared bool) {
	t.Helper()
	sec := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: registry.DefaultKeySecretName("curseforge"), Namespace: modpackControlNS,
		Labels: map[string]string{registry.RegistryKeySecretLabel: "true"}}, Data: map[string][]byte{"apiKey": []byte(key)}}
	if shared {
		sec.Annotations = map[string]string{curseforgeShareAnnotation: "true"}
	}
	if _, err := e.home.Typed.CoreV1().Secrets(modpackControlNS).Create(context.Background(), sec, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create key secret: %v", err)
	}
}

// auditReasons returns the reason of every POST/DELETE audit row of path.
func auditReasons(t *testing.T, a *audit.Auditor, method, path string) []string {
	t.Helper()
	var out []string
	err := a.Stream(context.Background(), audit.StreamFilter{Method: method}, func(e audit.Event) error {
		if e.Path == path {
			out = append(out, e.Reason)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("stream audit: %v", err)
	}
	return out
}

// ----- T015 -----

func TestModpackInstall_WritesSpecAuditsWithoutKey(t *testing.T) {
	e := newModpackEnv(t, "servers:read", "servers:write")
	e.addKeySecret(t, "super-secret-cf-key", true)
	gs := e.server(t, "1.21.1-neoforge", nil)
	path := "/servers/" + gs.GetName() + "/modpack"

	rr := modpackReq(t, e.router, http.MethodPost, path, map[string]any{
		"provider": "curseforge", "project": "all-the-mods-10", "version": "6512345",
		"title": "All the Mods 10", "versionName": "ATM10-2.41"})
	if rr.Code != http.StatusAccepted {
		t.Fatalf("POST status %d: %s", rr.Code, rr.Body)
	}
	v := decodeModpackView(t, rr)
	if v.Active == nil || v.Active.Provider != "curseforge" || v.Active.Version != "6512345" || !v.Active.Pinned {
		t.Errorf("response active = %+v", v.Active)
	}
	got := getServerObj(t, gs.GetName())
	spec, _, _ := unstructured.NestedMap(got.Object, "spec", "modpack")
	if spec["provider"] != "curseforge" || spec["project"] != "all-the-mods-10" || spec["version"] != "6512345" ||
		spec["title"] != "All the Mods 10" || spec["versionName"] != "ATM10-2.41" {
		t.Errorf("spec.modpack = %v", spec)
	}
	if _, found, _ := unstructured.NestedSlice(got.Object, "spec", "env"); found {
		t.Errorf("spec.env must stay untouched (the operator renders the pack env)")
	}
	reasons := auditReasons(t, e.auditor, http.MethodPost, path)
	if len(reasons) != 1 || reasons[0] != "server.modpack.install provider=curseforge project=all-the-mods-10 version=6512345" {
		t.Errorf("audit reasons = %v", reasons)
	}
	for _, r := range reasons {
		if strings.Contains(r, "super-secret-cf-key") {
			t.Errorf("audit row leaks the key: %q", r)
		}
	}
}

func TestModpackInstall_SameProjectIsAChange(t *testing.T) {
	e := newModpackEnv(t, "servers:read", "servers:write")
	gs := e.server(t, "1.21.1-neoforge", func(o *unstructured.Unstructured) {
		_ = unstructured.SetNestedMap(o.Object, map[string]any{"provider": "modrinth", "project": "cobblemon", "version": "v1"}, "spec", "modpack")
	})
	setServerStatus(t, gs.GetName(), modpackStatus("Applied", gs.GetGeneration(),
		map[string]any{"provider": "modrinth", "project": "cobblemon", "version": "v1"}))
	path := "/servers/" + gs.GetName() + "/modpack"
	rr := modpackReq(t, e.router, http.MethodPost, path, map[string]any{"provider": "modrinth", "project": "cobblemon", "version": "v2"})
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status %d: %s", rr.Code, rr.Body)
	}
	if reasons := auditReasons(t, e.auditor, http.MethodPost, path); len(reasons) != 1 || !strings.HasPrefix(reasons[0], "server.modpack.change provider=modrinth project=cobblemon version=v2") {
		t.Errorf("audit reasons = %v", reasons)
	}
}

func TestModpackInstall_NeedsManagePermission(t *testing.T) {
	e := newModpackEnv(t, "servers:read")
	gs := e.server(t, "1.21.1-neoforge", nil)
	rr := modpackReq(t, e.router, http.MethodPost, "/servers/"+gs.GetName()+"/modpack",
		map[string]any{"provider": "modrinth", "project": "cobblemon", "version": "v1"})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("read-only user got %d, want 403", rr.Code)
	}
	if _, found, _ := unstructured.NestedMap(getServerObj(t, gs.GetName()).Object, "spec", "modpack"); found {
		t.Errorf("spec.modpack written despite 403")
	}
	if rr := modpackReq(t, e.router, http.MethodGet, "/servers/"+gs.GetName()+"/modpack", nil); rr.Code != http.StatusOK {
		t.Errorf("read user GET status %d", rr.Code)
	}
}

func TestModpackInstall_KeyRequiredWhileSharingOff(t *testing.T) {
	e := newModpackEnv(t, "servers:read", "servers:write")
	gs := e.server(t, "1.12.2-forge", nil) // java8-multiarch image: no built-in key
	path := "/servers/" + gs.GetName() + "/modpack"
	body := map[string]any{"provider": "curseforge", "project": "sky-factory-4", "version": "2915237"}

	rr := modpackReq(t, e.router, http.MethodPost, path, body)
	if rr.Code != http.StatusConflict || problemID(t, rr) != "server.modpack.key_required" {
		t.Fatalf("status %d id %q, want 409 server.modpack.key_required", rr.Code, problemID(t, rr))
	}
	if _, found, _ := unstructured.NestedMap(getServerObj(t, gs.GetName()).Object, "spec", "modpack"); found {
		t.Errorf("spec.modpack written despite 409")
	}
	// Sharing on (managed key + annotation): the same request is accepted.
	e.addKeySecret(t, "k", true)
	if rr := modpackReq(t, e.router, http.MethodPost, path, body); rr.Code != http.StatusAccepted {
		t.Fatalf("after sharing on: status %d: %s", rr.Code, rr.Body)
	}
}

func TestModpackInstall_VersionRequiredLegacyBodyStillAccepted(t *testing.T) {
	e := newModpackEnv(t, "servers:read", "servers:write")
	gs := e.server(t, "1.21.1-neoforge", nil)
	path := "/servers/" + gs.GetName() + "/modpack"

	rr := modpackReq(t, e.router, http.MethodPost, path, map[string]any{"provider": "modrinth", "project": "cobblemon"})
	if rr.Code != http.StatusBadRequest || problemID(t, rr) != "server.modpack.version_required" {
		t.Fatalf("status %d id %q, want 400 server.modpack.version_required", rr.Code, problemID(t, rr))
	}
	rr = modpackReq(t, e.router, http.MethodPost, path+"?provider=modrinth", map[string]any{"ref": "cobblemon"})
	if rr.Code != http.StatusAccepted {
		t.Fatalf("legacy {ref} body: status %d: %s", rr.Code, rr.Body)
	}
	spec, _, _ := unstructured.NestedMap(getServerObj(t, gs.GetName()).Object, "spec", "modpack")
	if spec["provider"] != "modrinth" || spec["project"] != "cobblemon" || spec["version"] != nil {
		t.Errorf("legacy spec.modpack = %v", spec)
	}
}

// ----- T038 -----

func appliedCF() map[string]any {
	return map[string]any{"provider": "curseforge", "project": "atm10", "version": "6512345"}
}

func TestModpackRemove_SetsAnnotationAndShowsPendingRemoval(t *testing.T) {
	e := newModpackEnv(t, "servers:read", "servers:write")
	e.addDestination(t)
	gs := e.server(t, "1.21.1-neoforge", func(o *unstructured.Unstructured) {
		_ = unstructured.SetNestedMap(o.Object, appliedCF(), "spec", "modpack")
	})
	setServerStatus(t, gs.GetName(), modpackStatus("Applied", gs.GetGeneration(), appliedCF()))
	path := "/servers/" + gs.GetName() + "/modpack"

	rr := modpackReq(t, e.router, http.MethodDelete, path+"?backupFirst=true", nil)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("DELETE status %d: %s", rr.Code, rr.Body)
	}
	v := decodeModpackView(t, rr)
	if v.Active == nil || !v.Active.PendingRemoval || v.Active.Project != "atm10" || v.Phase != "Pending" {
		t.Errorf("response = %+v active=%+v, want pendingRemoval active atm10 and phase Pending", v, v.Active)
	}
	got := getServerObj(t, gs.GetName())
	if _, found, _ := unstructured.NestedMap(got.Object, "spec", "modpack"); found {
		t.Errorf("spec.modpack still set")
	}
	var marker modpackRemoveRequest
	if err := json.Unmarshal([]byte(got.GetAnnotations()[modpackRemoveAnnotation]), &marker); err != nil || marker.RequestID == "" || !marker.BackupFirst {
		t.Errorf("annotation = %q (err %v), want requestId and backupFirst=true", got.GetAnnotations()[modpackRemoveAnnotation], err)
	}
	// GET shows the same pending removal.
	g := decodeModpackView(t, modpackReq(t, e.router, http.MethodGet, path, nil))
	if g.Active == nil || !g.Active.PendingRemoval || !g.BackupAvailable {
		t.Errorf("GET = %+v active=%+v", g, g.Active)
	}
	if reasons := auditReasons(t, e.auditor, http.MethodDelete, path); len(reasons) != 1 ||
		reasons[0] != "server.modpack.remove provider=curseforge project=atm10 version=6512345" {
		t.Errorf("audit reasons = %v", reasons)
	}
}

func TestModpackRemove_BackupFirstNeedsDestination(t *testing.T) {
	e := newModpackEnv(t, "servers:read", "servers:write")
	gs := e.server(t, "1.21.1-neoforge", func(o *unstructured.Unstructured) {
		_ = unstructured.SetNestedMap(o.Object, appliedCF(), "spec", "modpack")
	})
	setServerStatus(t, gs.GetName(), modpackStatus("Applied", gs.GetGeneration(), appliedCF()))
	path := "/servers/" + gs.GetName() + "/modpack"
	rr := modpackReq(t, e.router, http.MethodDelete, path+"?backupFirst=true", nil)
	if rr.Code != http.StatusBadRequest || problemID(t, rr) != "server.modpack.backup_unavailable" {
		t.Fatalf("status %d id %q, want 400 server.modpack.backup_unavailable", rr.Code, problemID(t, rr))
	}
	if rr := modpackReq(t, e.router, http.MethodDelete, path+"?backupFirst=maybe", nil); rr.Code != http.StatusBadRequest {
		t.Errorf("invalid backupFirst status %d, want 400", rr.Code)
	}
	if _, found, _ := unstructured.NestedMap(getServerObj(t, gs.GetName()).Object, "spec", "modpack"); !found {
		t.Errorf("a rejected DELETE must not remove spec.modpack")
	}
}

func TestModpackRemove_RetryAfterFailedBackup(t *testing.T) {
	e := newModpackEnv(t, "servers:read", "servers:write")
	gs := e.server(t, "1.21.1-neoforge", func(o *unstructured.Unstructured) {
		o.SetAnnotations(map[string]string{modpackRemoveAnnotation: `{"requestId":"old","backupFirst":true}`})
	})
	setServerStatus(t, gs.GetName(), map[string]any{"phase": "Running", "modpack": map[string]any{
		"phase": "BackupFailed", "observedGeneration": gs.GetGeneration(), "message": "backup failed", "applied": appliedCF()}})
	path := "/servers/" + gs.GetName() + "/modpack"

	g := decodeModpackView(t, modpackReq(t, e.router, http.MethodGet, path, nil))
	if g.Phase != "BackupFailed" || g.Message != "backup failed" || g.Active == nil || !g.Active.PendingRemoval {
		t.Errorf("GET before retry = %+v active=%+v", g, g.Active)
	}
	rr := modpackReq(t, e.router, http.MethodDelete, path+"?backupFirst=false", nil)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("retry status %d: %s", rr.Code, rr.Body)
	}
	var marker modpackRemoveRequest
	if err := json.Unmarshal([]byte(getServerObj(t, gs.GetName()).GetAnnotations()[modpackRemoveAnnotation]), &marker); err != nil ||
		marker.RequestID == "" || marker.RequestID == "old" || marker.BackupFirst {
		t.Errorf("retry annotation = %+v (err %v), want a new requestId and backupFirst=false", marker, err)
	}
}

func TestModpackRemove_NotFoundOnlyWithoutSpecAndApplied(t *testing.T) {
	e := newModpackEnv(t, "servers:read", "servers:write")
	none := e.server(t, "1.21.1-neoforge", nil)
	rr := modpackReq(t, e.router, http.MethodDelete, "/servers/"+none.GetName()+"/modpack", nil)
	if rr.Code != http.StatusNotFound || problemID(t, rr) != "server.modpack.not_found" {
		t.Fatalf("no pack: status %d id %q, want 404 server.modpack.not_found", rr.Code, problemID(t, rr))
	}
	// spec.modpack alone (never applied yet) is removable.
	specOnly := e.server(t, "1.21.1-neoforge", func(o *unstructured.Unstructured) {
		_ = unstructured.SetNestedMap(o.Object, appliedCF(), "spec", "modpack")
	})
	setServerStatus(t, specOnly.GetName(), modpackStatus("Applied", specOnly.GetGeneration(), appliedCF()))
	if rr := modpackReq(t, e.router, http.MethodDelete, "/servers/"+specOnly.GetName()+"/modpack", nil); rr.Code != http.StatusAccepted {
		t.Errorf("spec+applied: status %d: %s", rr.Code, rr.Body)
	}
	// status.modpack.applied alone (spec already gone, no removal in flight) is removable too.
	appliedOnly := e.server(t, "1.21.1-neoforge", nil)
	setServerStatus(t, appliedOnly.GetName(), modpackStatus("Applied", appliedOnly.GetGeneration(), appliedCF()))
	if rr := modpackReq(t, e.router, http.MethodDelete, "/servers/"+appliedOnly.GetName()+"/modpack", nil); rr.Code != http.StatusAccepted {
		t.Errorf("applied only: status %d: %s", rr.Code, rr.Body)
	}
}

func TestModpackRemove_ConflictWhileApplying(t *testing.T) {
	e := newModpackEnv(t, "servers:read", "servers:write")
	gs := e.server(t, "1.21.1-neoforge", func(o *unstructured.Unstructured) {
		_ = unstructured.SetNestedMap(o.Object, appliedCF(), "spec", "modpack")
	})
	setServerStatus(t, gs.GetName(), modpackStatus("Applying", gs.GetGeneration(), appliedCF()))
	rr := modpackReq(t, e.router, http.MethodDelete, "/servers/"+gs.GetName()+"/modpack", nil)
	if rr.Code != http.StatusConflict || problemID(t, rr) != "server.modpack.change_in_progress" {
		t.Fatalf("status %d id %q, want 409 server.modpack.change_in_progress", rr.Code, problemID(t, rr))
	}
}
