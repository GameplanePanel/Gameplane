package handlers

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// secAuditTemplate returns a minecraft template marking the given settings risky.
func secAuditTemplate(risky ...string) *unstructured.Unstructured {
	marks := map[string]any{}
	for _, name := range risky {
		marks[name] = map[string]any{"reason": "module reason"}
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameTemplate",
		"metadata":   map[string]any{"name": "minecraft"},
		"spec":       map[string]any{"security": map[string]any{"risky": marks}},
	}}
}

// On an update, the risky marker follows the template revision the server is
// pinned to, not the current catalog template (contracts/audit-events.md).
func TestSecurityAuditChangeReason_UpdateUsesPinnedRevision(t *testing.T) {
	// The catalog has moved on and marks only fsGroup; the pinned revision
	// marks only runAsUser.
	k := fakeKubeClient(secAuditTemplate("fsGroup"))
	live := secRootObj(map[string]any{"runAsUser": secAuditVal(2000), "fsGroup": secAuditVal(2000)})
	pinRedactionTemplate(t, k, live, secAuditTemplate("runAsUser"))

	desired := secRootObj(map[string]any{"runAsUser": secAuditVal(0), "fsGroup": secAuditVal(3000)})
	got := secAuditReasonMap(t, securityChangeReason(context.Background(), k, desired, live))
	if risky, _ := got["risky"].([]any); len(risky) != 1 || risky[0] != "runAsUser" {
		t.Fatalf("update risky = %#v, want [runAsUser] from the pinned revision", got["risky"])
	}
}

// Without a pinned revision, an update reads the catalog template.
func TestSecurityAuditChangeReason_UpdateWithoutPinUsesCatalog(t *testing.T) {
	k := fakeKubeClient(secAuditTemplate("fsGroup"))
	live := secRootObj(map[string]any{"fsGroup": secAuditVal(2000)})
	desired := secRootObj(map[string]any{"fsGroup": secAuditVal(3000)})
	got := secAuditReasonMap(t, securityChangeReason(context.Background(), k, desired, live))
	if risky, _ := got["risky"].([]any); len(risky) != 1 || risky[0] != "fsGroup" {
		t.Fatalf("update risky = %#v, want [fsGroup] from the catalog", got["risky"])
	}
}

// A pinned snapshot that cannot be read marks nothing; the row is still written.
func TestSecurityAuditChangeReason_UnreadablePinMarksNothing(t *testing.T) {
	k := fakeKubeClient(secAuditTemplate("runAsUser"))
	live := secRootObj(map[string]any{"runAsUser": secAuditVal(2000)})
	live.SetUID("uid-unreadable")
	live.SetAnnotations(map[string]string{"gameplane.local/effective-template-revision": "0000000000000000000000000000000000000000000000000000000000000000"})
	desired := secRootObj(map[string]any{"runAsUser": secAuditVal(0)})
	reason := securityChangeReason(context.Background(), k, desired, live)
	got := secAuditReasonMap(t, reason)
	if _, ok := got["risky"]; ok {
		t.Fatalf("risky = %#v, want no marker when the pinned snapshot is missing", got["risky"])
	}
}

// Moving the server to another template reads the new catalog template.
func TestSecurityAuditChangeReason_TemplateChangeUsesNewCatalog(t *testing.T) {
	other := secAuditTemplate("runAsGroup")
	other.SetName("other")
	k := fakeKubeClient(secAuditTemplate("runAsUser"), other)
	live := secRootObj(map[string]any{"runAsGroup": secAuditVal(2000)})
	pinRedactionTemplate(t, k, live, secAuditTemplate("runAsUser"))
	desired := secRootObj(map[string]any{"runAsGroup": secAuditVal(0)})
	desired.Object["spec"].(map[string]any)["templateRef"] = map[string]any{"name": "other"}
	got := secAuditReasonMap(t, securityChangeReason(context.Background(), k, desired, live))
	if risky, _ := got["risky"].([]any); len(risky) != 1 || risky[0] != "runAsGroup" {
		t.Fatalf("risky = %#v, want [runAsGroup] from the new template", got["risky"])
	}
}
