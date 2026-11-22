//go:build envtest

package handlers

import (
	"net/http"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// TestModpackInstall_ClearsAdoptionPin asserts that installing a pack removes
// the legacy adoption pin (gameplane.local/modpack-adopted) so it no longer
// holds back the new pack, and that a pending removal request is cleared too.
func TestModpackInstall_ClearsAdoptionPin(t *testing.T) {
	e := newModpackEnv(t, "servers:read", "servers:write")
	gs := e.server(t, "1.21.1-neoforge", func(o *unstructured.Unstructured) {
		o.SetAnnotations(map[string]string{
			modpackAdoptedAnnotation: `{"version":"legacy"}`,
			modpackRemoveAnnotation:  `{"requestId":"old","backupFirst":true}`,
		})
	})
	path := "/servers/" + gs.GetName() + "/modpack"

	rr := modpackReq(t, e.router, http.MethodPost, path, map[string]any{
		"provider": "curseforge", "project": "all-the-mods-10", "version": "6512345",
		"title": "All the Mods 10", "versionName": "ATM10-2.41"})
	if rr.Code != http.StatusAccepted {
		t.Fatalf("POST status %d: %s", rr.Code, rr.Body)
	}
	ann := getServerObj(t, gs.GetName()).GetAnnotations()
	if v, ok := ann[modpackAdoptedAnnotation]; ok {
		t.Errorf("adoption pin still set after install: %q", v)
	}
	if v, ok := ann[modpackRemoveAnnotation]; ok {
		t.Errorf("removal request still set after install: %q", v)
	}
}
