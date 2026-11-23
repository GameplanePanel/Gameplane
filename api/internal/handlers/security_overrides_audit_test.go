package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/messages"
)

// T049 (unit): the reason builder of a change row, the template's risky
// names, and the refusal reasons (contracts/audit-events.md). The envtest half
// is in security_overrides_audit_envtest_test.go.

// secAuditReasonMap parses a security:{...} reason into a map. It fails the test
// when the prefix or the JSON is wrong.
func secAuditReasonMap(t *testing.T, reason string) map[string]any {
	t.Helper()
	raw, ok := strings.CutPrefix(reason, "security:")
	if !ok {
		t.Fatalf("reason %q does not start with security:", reason)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("reason %q is not JSON: %v", reason, err)
	}
	return out
}

func secAuditBlock(members map[string]any) map[string]any { return members }

func secAuditVal(v int64) map[string]any { return map[string]any{"value": v} }

func TestSecurityAuditReason_CreateHasEmptyBefore(t *testing.T) {
	after := secAuditBlock(map[string]any{"runAsUser": secAuditVal(2000)})
	reason := buildSecurityReason(nil, after, true, nil)
	got := secAuditReasonMap(t, reason)
	if before, ok := got["before"].(map[string]any); !ok || len(before) != 0 {
		t.Errorf("create before = %#v, want {}; reason=%s", got["before"], reason)
	}
	if a := got["after"].(map[string]any); a["runAsUser"] != float64(2000) {
		t.Errorf("create after = %#v, want runAsUser 2000", a)
	}
	if _, has := got["risky"]; has {
		t.Errorf("risky key present without risky settings: %s", reason)
	}
}

func TestSecurityAuditReason_ValueNotationPerSetting(t *testing.T) {
	cases := []struct {
		name       string
		before     map[string]any
		after      map[string]any
		wantBefore map[string]any
		wantAfter  map[string]any
	}{
		{
			name:       "not set is null",
			before:     nil,
			after:      map[string]any{"runAsUser": secAuditVal(2000)},
			wantBefore: map[string]any{"runAsUser": nil},
			wantAfter:  map[string]any{"runAsUser": float64(2000)},
		},
		{
			name:       "number to number",
			before:     map[string]any{"runAsUser": secAuditVal(1000)},
			after:      map[string]any{"runAsUser": secAuditVal(2000)},
			wantBefore: map[string]any{"runAsUser": float64(1000)},
			wantAfter:  map[string]any{"runAsUser": float64(2000)},
		},
		{
			name:       "image default is the word image",
			before:     map[string]any{"runAsGroup": secAuditVal(1000)},
			after:      map[string]any{"runAsGroup": map[string]any{"useImageDefault": true}},
			wantBefore: map[string]any{"runAsGroup": float64(1000)},
			wantAfter:  map[string]any{"runAsGroup": "image"},
		},
		{
			name:       "ownership check is its string",
			before:     nil,
			after:      map[string]any{"volumeOwnershipCheck": "OnlyWhenWrong"},
			wantBefore: map[string]any{"volumeOwnershipCheck": nil},
			wantAfter:  map[string]any{"volumeOwnershipCheck": "OnlyWhenWrong"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason := buildSecurityReason(tc.before, tc.after, false, nil)
			got := secAuditReasonMap(t, reason)
			wb := got["before"].(map[string]any)
			wa := got["after"].(map[string]any)
			for k, v := range tc.wantBefore {
				if wb[k] != v {
					t.Errorf("before[%s] = %#v, want %#v; reason=%s", k, wb[k], v, reason)
				}
			}
			for k, v := range tc.wantAfter {
				if wa[k] != v {
					t.Errorf("after[%s] = %#v, want %#v; reason=%s", k, wa[k], v, reason)
				}
			}
		})
	}
}

func TestSecurityAuditReason_OnlyChangedSettingsAppear(t *testing.T) {
	before := map[string]any{"runAsUser": secAuditVal(2000), "fsGroup": secAuditVal(1000)}
	after := map[string]any{"runAsUser": secAuditVal(2000), "fsGroup": secAuditVal(3000)}
	got := secAuditReasonMap(t, buildSecurityReason(before, after, false, nil))
	wb := got["before"].(map[string]any)
	wa := got["after"].(map[string]any)
	if _, has := wb["runAsUser"]; has {
		t.Errorf("unchanged runAsUser appears in before: %#v", wb)
	}
	if _, has := wa["runAsUser"]; has {
		t.Errorf("unchanged runAsUser appears in after: %#v", wa)
	}
	if wb["fsGroup"] != float64(1000) || wa["fsGroup"] != float64(3000) {
		t.Errorf("fsGroup change = %#v -> %#v, want 1000 -> 3000", wb, wa)
	}
}

func TestSecurityAuditReason_RiskyListNamesOnly(t *testing.T) {
	before := map[string]any{"runAsUser": secAuditVal(2000), "runAsGroup": secAuditVal(1000)}
	after := map[string]any{"runAsUser": secAuditVal(0), "runAsGroup": secAuditVal(2000)}

	t.Run("risky changed setting is listed, non-risky is not", func(t *testing.T) {
		got := secAuditReasonMap(t, buildSecurityReason(before, after, false, map[string]bool{"runAsUser": true}))
		risky, ok := got["risky"].([]any)
		if !ok || len(risky) != 1 || risky[0] != "runAsUser" {
			t.Errorf("risky = %#v, want [runAsUser]", got["risky"])
		}
	})

	t.Run("risky but unchanged setting is not listed", func(t *testing.T) {
		b := map[string]any{"runAsUser": secAuditVal(2000), "fsGroup": secAuditVal(1000)}
		a := map[string]any{"runAsUser": secAuditVal(2000), "fsGroup": secAuditVal(3000)}
		got := secAuditReasonMap(t, buildSecurityReason(b, a, false, map[string]bool{"runAsUser": true}))
		if _, has := got["risky"]; has {
			t.Errorf("unchanged risky setting listed: %s", buildSecurityReason(b, a, false, map[string]bool{"runAsUser": true}))
		}
	})

	t.Run("absent risky map adds no key", func(t *testing.T) {
		reason := buildSecurityReason(before, after, false, nil)
		if strings.Contains(reason, "risky") {
			t.Errorf("reason has a risky key with no risky map: %s", reason)
		}
	})

	t.Run("no change gives no reason", func(t *testing.T) {
		if reason := buildSecurityReason(before, before, false, map[string]bool{"runAsUser": true}); reason != "" {
			t.Errorf("unchanged block reason = %q, want empty", reason)
		}
	})
}

// The template's risky reason text is read by securityRiskyNames as names only,
// and never reaches the row.
func TestSecurityAuditRiskyNames_ReasonTextNeverCopied(t *testing.T) {
	const secretText = "SECRET-RISK-TEXT-DO-NOT-COPY"
	tmpl := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameTemplate",
		"metadata":   map[string]any{"name": "risky-tmpl"},
		"spec": map[string]any{"security": map[string]any{"risky": map[string]any{
			"runAsUser": map[string]any{"reason": secretText},
		}}},
	}}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{kube.GVRs["templates"]: "GameTemplateList"}, tmpl)
	k := &kube.Client{Dynamic: dyn}

	risky := securityRiskyNames(context.Background(), k, "risky-tmpl")
	if !risky["runAsUser"] || risky["fsGroup"] || risky["runAsGroup"] {
		t.Fatalf("risky names = %v, want only runAsUser", risky)
	}
	reason := buildSecurityReason(
		map[string]any{"runAsUser": secAuditVal(2000)},
		map[string]any{"runAsUser": secAuditVal(0)},
		false, risky)
	if strings.Contains(reason, secretText) {
		t.Errorf("module reason text leaked into the row: %s", reason)
	}
	if !strings.Contains(reason, `"risky":["runAsUser"]`) {
		t.Errorf("reason lacks the risky name: %s", reason)
	}
}

func TestSecurityAuditRiskyNames_UnreadableTemplateMarksNothing(t *testing.T) {
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{kube.GVRs["templates"]: "GameTemplateList"})
	if got := securityRiskyNames(context.Background(), &kube.Client{Dynamic: dyn}, "absent"); len(got) != 0 {
		t.Errorf("risky names for a missing template = %v, want none", got)
	}
	if got := securityRiskyNames(context.Background(), nil, "absent"); len(got) != 0 {
		t.Errorf("risky names without a client = %v, want none", got)
	}
}

func TestSecurityAuditChangeReason_CreateAndNoChange(t *testing.T) {
	tmpl := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameTemplate",
		"metadata":   map[string]any{"name": "minecraft"},
		"spec": map[string]any{"security": map[string]any{"risky": map[string]any{
			"runAsUser": map[string]any{"reason": "ignored"},
		}}},
	}}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{kube.GVRs["templates"]: "GameTemplateList"}, tmpl)
	k := &kube.Client{Dynamic: dyn}

	desired := secRootObj(map[string]any{"runAsUser": secAuditVal(0)})
	reason := securityChangeReason(context.Background(), k, desired, nil)
	got := secAuditReasonMap(t, reason)
	if before := got["before"].(map[string]any); len(before) != 0 {
		t.Errorf("create before = %#v, want {}", before)
	}
	if risky, _ := got["risky"].([]any); len(risky) != 1 || risky[0] != "runAsUser" {
		t.Errorf("create risky = %#v, want [runAsUser]", got["risky"])
	}

	same := secRootObj(map[string]any{"runAsUser": secAuditVal(2000)})
	if reason := securityChangeReason(context.Background(), k, same, same); reason != "" {
		t.Errorf("unchanged save reason = %q, want empty", reason)
	}
}

// Refusal reasons carry the setting name only, never the message text.
func TestSecurityAuditRefusalReason(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantReason string
		wantStatus int
		wantOK     bool
	}{
		{"permission", messages.New("server.security.forbidden", nil), "security_forbidden", http.StatusForbidden, true},
		{
			"root",
			messages.New("server.security.root_disabled", map[string]any{"setting": "runAsUser"}),
			"security_root_disabled:runAsUser", http.StatusForbidden, true,
		},
		{
			"invalid",
			messages.New("server.security.invalid", map[string]any{"setting": "fsGroup", "detail": "must be a whole number"}),
			"security_invalid:fsGroup", http.StatusUnprocessableEntity, true,
		},
		{"not a security refusal", errors.New("boom"), "", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason, status, ok := securityRefusalReason(tc.err)
			if ok != tc.wantOK || reason != tc.wantReason || status != tc.wantStatus {
				t.Errorf("securityRefusalReason = (%q, %d, %v), want (%q, %d, %v)",
					reason, status, ok, tc.wantReason, tc.wantStatus, tc.wantOK)
			}
		})
	}
}
