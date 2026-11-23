//go:build envtest

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// T028 envtest for the shape step (case 4) of spec.security
// (contracts/api-server-security.md). It reuses the dedicated RBAC router of
// security_overrides_envtest_test.go (secOvRouter) and the case list of
// security_overrides_shape_test.go. Each bad shape must be refused with 422
// server.security.invalid, name the setting in args, and create nothing.

// shapeProblemArgs decodes the args of an RFC 9457 refusal.
func shapeProblemArgs(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var doc struct {
		Args map[string]any `json:"args"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode problem body: %v", err)
	}
	return doc.Args
}

// TestSecurityOverrideShape_CreateInvalidRefused posts each bad shape as an
// admin and checks the 422, the args and that no server was created.
func TestSecurityOverrideShape_CreateInvalidRefused(t *testing.T) {
	r := secOvRouter(t)
	for _, tc := range shapeInvalidCases() {
		t.Run(tc.name, func(t *testing.T) {
			name := uniqueResourceName("sec-shape")
			t.Cleanup(func() {
				_ = kubeC.Dynamic.Resource(gvrServers()).
					Namespace(scope.DefaultNamespace).
					Delete(context.Background(), name, metav1.DeleteOptions{})
			})

			rr := secOvDo(t, r, adminUser(), http.MethodPost, "/servers",
				secOvPutBody(name, tc.block, nil))
			if rr.Code != http.StatusUnprocessableEntity {
				t.Fatalf("POST %v = %d, want 422; body=%s", tc.block, rr.Code, rr.Body.String())
			}
			if id := secOvProblemID(t, rr); id != "server.security.invalid" {
				t.Errorf("messageId = %q, want server.security.invalid", id)
			}
			if got := shapeProblemArgs(t, rr)["setting"]; got != tc.setting {
				t.Errorf("args.setting = %v, want %q", got, tc.setting)
			}
			if _, err := kubeC.Dynamic.Resource(gvrServers()).
				Namespace(scope.DefaultNamespace).
				Get(t.Context(), name, metav1.GetOptions{}); err == nil {
				t.Error("refused create left a server behind")
			}
		})
	}
}

// TestSecurityOverrideShape_UpdateInvalidRefused puts a bad shape over an
// existing server as an admin. The refusal must leave the stored object as it
// was (no override).
func TestSecurityOverrideShape_UpdateInvalidRefused(t *testing.T) {
	r := secOvRouter(t)
	name := uniqueResourceName("sec-shape-upd")
	secOvFixture(t, name, nil)

	bad := map[string]any{"runAsUser": map[string]any{"value": float64(4294967296)}}
	rr := secOvDo(t, r, adminUser(), http.MethodPut, "/servers/"+name,
		secOvPutBody(name, bad, nil))
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("PUT bad shape = %d, want 422; body=%s", rr.Code, rr.Body.String())
	}
	if id := secOvProblemID(t, rr); id != "server.security.invalid" {
		t.Errorf("messageId = %q, want server.security.invalid", id)
	}
	if got := shapeProblemArgs(t, rr)["setting"]; got != "runAsUser" {
		t.Errorf("args.setting = %v, want runAsUser", got)
	}
	if _, found := secOvStoredSecurity(t, name); found {
		t.Error("refused update changed the stored spec.security")
	}
}

// TestSecurityOverrideShape_ValidStored creates a server with a valid override
// as an admin, checks the 201 response carries it, and reads it back from the
// cluster.
func TestSecurityOverrideShape_ValidStored(t *testing.T) {
	r := secOvRouter(t)
	name := uniqueResourceName("sec-shape-ok")
	t.Cleanup(func() {
		_ = kubeC.Dynamic.Resource(gvrServers()).
			Namespace(scope.DefaultNamespace).
			Delete(context.Background(), name, metav1.DeleteOptions{})
	})

	valid := map[string]any{"runAsUser": map[string]any{"value": float64(2000)}}
	rr := secOvDo(t, r, adminUser(), http.MethodPost, "/servers",
		secOvPutBody(name, valid, nil))
	if rr.Code != http.StatusCreated {
		t.Fatalf("POST valid override = %d, want 201; body=%s", rr.Code, rr.Body.String())
	}

	var got struct {
		Spec struct {
			Security map[string]any `json:"security"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	ru, _ := got.Spec.Security["runAsUser"].(map[string]any)
	if ru["value"] != float64(2000) {
		t.Errorf("response runAsUser = %v, want value 2000", got.Spec.Security["runAsUser"])
	}

	stored, found := secOvStoredSecurity(t, name)
	if !found {
		t.Fatal("valid override was not stored")
	}
	sru, _ := stored["runAsUser"].(map[string]any)
	if sru["value"] != int64(2000) {
		t.Errorf("stored runAsUser = %v, want value 2000", stored["runAsUser"])
	}
}
