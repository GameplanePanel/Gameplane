//go:build envtest

package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

func legacyServer(o *unstructured.Unstructured) {
	_ = unstructured.SetNestedSlice(o.Object, []any{
		map[string]any{"name": "MODRINTH_MODPACK", "value": "cobblemon"},
		map[string]any{"name": "TYPE", "value": "MODRINTH"},
		map[string]any{"name": "KEEP", "value": "me"},
	}, "spec", "env")
}

func TestModpackAdoption_SweepReadsVersionThroughAgent(t *testing.T) {
	e := newModpackEnv(t, "servers:read", "servers:write")
	e.files.files[".modrinth-modpack-manifest.json"] = `{"projectSlug":"cobblemon","versionId":"ver123"}`
	gs := e.server(t, "1.21.1-neoforge", legacyServer)
	setServerStatus(t, gs.GetName(), map[string]any{"phase": "Running"})

	sweepModpackAdoption(context.Background(), e.home, scope.DefaultCluster, e.files, slog.Default())

	got := getServerObj(t, gs.GetName())
	spec, _, _ := unstructured.NestedMap(got.Object, "spec", "modpack")
	if spec["provider"] != "modrinth" || spec["project"] != "cobblemon" || spec["version"] != "ver123" {
		t.Fatalf("spec.modpack = %v, want modrinth/cobblemon/ver123", spec)
	}
	if got.GetAnnotations()[modpackAdoptedAnnotation] != "true" {
		t.Errorf("adopted annotation missing: %v", got.GetAnnotations())
	}
	env, _, _ := unstructured.NestedSlice(got.Object, "spec", "env")
	if len(env) != 3 {
		t.Errorf("spec.env changed by adoption: %v", env)
	}
	// Second sweep is a no-op (not a candidate any more): resourceVersion stays.
	rv := got.GetResourceVersion()
	sweepModpackAdoption(context.Background(), e.home, scope.DefaultCluster, e.files, slog.Default())
	if after := getServerObj(t, gs.GetName()); after.GetResourceVersion() != rv {
		t.Errorf("second sweep rewrote the server (%s -> %s)", rv, after.GetResourceVersion())
	}
}

func TestModpackAdoption_UnreadableLeavesPackUnpinned(t *testing.T) {
	e := newModpackEnv(t, "servers:read", "servers:write")
	// Stopped server: no pod, so the agent must not even be asked. The lazy
	// retry on GET does the adoption.
	gs := e.server(t, "1.21.1-neoforge", legacyServer)
	setServerStatus(t, gs.GetName(), map[string]any{"phase": "Stopped"})

	rr := modpackReq(t, e.router, http.MethodGet, "/servers/"+gs.GetName()+"/modpack", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET status %d: %s", rr.Code, rr.Body)
	}
	v := decodeModpackView(t, rr)
	if v.Active == nil || v.Active.Project != "cobblemon" || v.Active.Pinned || v.Active.Version != "" {
		t.Errorf("active = %+v, want cobblemon, pinned=false, no version", v.Active)
	}
	if len(e.files.calls) != 0 {
		t.Errorf("agent read for a stopped server: %v", e.files.calls)
	}
	got := getServerObj(t, gs.GetName())
	if _, has := got.Object["spec"].(map[string]any)["modpack"].(map[string]any)["version"]; has {
		t.Errorf("version must be absent")
	}
	if got.GetAnnotations()[modpackAdoptedAnnotation] != "true" {
		t.Errorf("adopted annotation missing")
	}
}

func TestModpackAdoption_BadManifestAndManagedServersAreSkipped(t *testing.T) {
	e := newModpackEnv(t, "servers:read", "servers:write")
	e.files.files[".modrinth-modpack-manifest.json"] = `not json`
	bad := e.server(t, "1.21.1-neoforge", legacyServer)
	setServerStatus(t, bad.GetName(), map[string]any{"phase": "Running"})
	managed := e.server(t, "1.21.1-neoforge", func(o *unstructured.Unstructured) {
		legacyServer(o)
		_ = unstructured.SetNestedMap(o.Object, map[string]any{"provider": "modrinth", "project": "other", "version": "v9"}, "spec", "modpack")
	})
	removed := e.server(t, "1.21.1-neoforge", legacyServer)
	setServerStatus(t, removed.GetName(), map[string]any{"phase": "Running", "modpack": map[string]any{"phase": "Applied"}})

	sweepModpackAdoption(context.Background(), e.home, scope.DefaultCluster, e.files, slog.Default())

	if spec, _, _ := unstructured.NestedMap(getServerObj(t, bad.GetName()).Object, "spec", "modpack"); spec["project"] != "cobblemon" || spec["version"] != nil {
		t.Errorf("unparsable manifest: spec.modpack = %v, want cobblemon without version", spec)
	}
	if spec, _, _ := unstructured.NestedMap(getServerObj(t, managed.GetName()).Object, "spec", "modpack"); spec["project"] != "other" {
		t.Errorf("already managed server rewritten: %v", spec)
	}
	if _, found, _ := unstructured.NestedMap(getServerObj(t, removed.GetName()).Object, "spec", "modpack"); found {
		t.Errorf("a server whose pack was removed (status.modpack set) must not be re-adopted")
	}
}

func TestModpackInstall_BackupFirstNeedsDestination(t *testing.T) {
	e := newModpackEnv(t, "servers:read", "servers:write")
	gs := e.server(t, "1.21.1-neoforge", nil)
	path := "/servers/" + gs.GetName() + "/modpack"
	body := map[string]any{"provider": "modrinth", "project": "cobblemon", "version": "v1", "backupFirst": true}

	rr := modpackReq(t, e.router, http.MethodPost, path, body)
	if rr.Code != http.StatusBadRequest || problemID(t, rr) != "server.modpack.backup_unavailable" {
		t.Fatalf("status %d id %q, want 400 server.modpack.backup_unavailable", rr.Code, problemID(t, rr))
	}
	e.addDestination(t)
	rr = modpackReq(t, e.router, http.MethodPost, path, body)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("with destination: status %d: %s", rr.Code, rr.Body)
	}
	spec, _, _ := unstructured.NestedMap(getServerObj(t, gs.GetName()).Object, "spec", "modpack")
	if spec["backupFirst"] != true {
		t.Errorf("spec.modpack = %v, want backupFirst=true", spec)
	}
}

func TestModpackInstall_ConflictWhileChangeInProgress(t *testing.T) {
	e := newModpackEnv(t, "servers:read", "servers:write")
	gs := e.server(t, "1.21.1-neoforge", func(o *unstructured.Unstructured) {
		_ = unstructured.SetNestedMap(o.Object, map[string]any{"provider": "modrinth", "project": "cobblemon", "version": "v1"}, "spec", "modpack")
	})
	path := "/servers/" + gs.GetName() + "/modpack"
	body := map[string]any{"provider": "modrinth", "project": "cobblemon", "version": "v2"}
	same := map[string]any{"provider": "modrinth", "project": "cobblemon", "version": "v1"}

	// Written, operator has not seen it yet (no status): Pending, so 409.
	if rr := modpackReq(t, e.router, http.MethodPost, path, body); rr.Code != http.StatusConflict || problemID(t, rr) != "server.modpack.change_in_progress" {
		t.Fatalf("pending: status %d id %q", rr.Code, problemID(t, rr))
	}
	for _, phase := range []string{"BackingUp", "Applying"} {
		setServerStatus(t, gs.GetName(), modpackStatus(phase, gs.GetGeneration(), nil))
		if rr := modpackReq(t, e.router, http.MethodPost, path, body); rr.Code != http.StatusConflict {
			t.Errorf("%s: status %d, want 409", phase, rr.Code)
		}
	}
	// BackupFailed for the current request and target: a new change is allowed.
	failed := modpackStatus("BackupFailed", gs.GetGeneration(), nil)
	failed["modpack"].(map[string]any)["target"] = same
	setServerStatus(t, gs.GetName(), failed)
	if rr := modpackReq(t, e.router, http.MethodPost, path, body); rr.Code != http.StatusAccepted {
		t.Errorf("BackupFailed: status %d: %s", rr.Code, rr.Body)
	}
	// After that write the generation moved: the change is pending again.
	if rr := modpackReq(t, e.router, http.MethodPost, path, same); rr.Code != http.StatusConflict {
		t.Errorf("after the retry write: status %d, want 409", rr.Code)
	}
	// Applied: free again.
	cur := getServerObj(t, gs.GetName())
	setServerStatus(t, gs.GetName(), modpackStatus("Applied", cur.GetGeneration(),
		map[string]any{"provider": "modrinth", "project": "cobblemon", "version": "v2"}))
	if rr := modpackReq(t, e.router, http.MethodPost, path, same); rr.Code != http.StatusAccepted {
		t.Errorf("Applied: status %d: %s", rr.Code, rr.Body)
	}
}
