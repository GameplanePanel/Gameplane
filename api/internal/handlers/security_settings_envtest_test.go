//go:build envtest

package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/rbac"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// T047 (envtest): GET and PUT /security-settings against the envtest
// apiserver. The router is dedicated and carries rbac.Middleware and the audit
// middleware (secAuditSetup in security_overrides_audit_envtest_test.go), never
// the shared mountedR. The ConfigMap writes are real.

// secSetViewer holds config:read and deliberately not config:manage.
func secSetViewer() *auth.User {
	return &auth.User{
		ID:       401,
		Username: "sec-setting-viewer",
		Role:     rbac.RoleViewer,
		Perms: map[string]map[string]map[string]struct{}{
			"*": {"*": {"config:read": {}}},
		},
	}
}

// secSetEnvDo sends method with a raw body to /security-settings?cluster=<home>
// as u (nil for no user).
func secSetEnvDo(t *testing.T, h http.Handler, u *auth.User, method, body string) *httptest.ResponseRecorder {
	t.Helper()
	ctx := t.Context()
	if u != nil {
		ctx = auth.WithUser(ctx, u)
	}
	req := httptest.NewRequestWithContext(ctx, method,
		"/security-settings?cluster="+scope.DefaultCluster, strings.NewReader(body))
	req.Header.Set("Accept", "application/problem+json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// secSetFlipRows returns the rows of the switch's own target.
func secSetFlipRows(t *testing.T, env *secAuditEnv) []secAuditRow {
	t.Helper()
	return secAuditRows(t, env.store, "target = ?", "security/"+scope.DefaultCluster)
}

func TestSecuritySettingsEnv_RoundTripAndFlipRow(t *testing.T) {
	env := secAuditSetup(t, kubeC)
	env.putConfigMap(t, "false")

	rr := secSetEnvDo(t, env.r, secSetViewer(), http.MethodGet, "")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"present":true`) ||
		!strings.Contains(rr.Body.String(), `"allowRootOverrides":false`) {
		t.Fatalf("GET = %d %s, want allowRootOverrides false and present true", rr.Code, rr.Body.String())
	}

	rr = secSetEnvDo(t, env.r, adminUser(), http.MethodPut, `{"allowRootOverrides":true}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("admin PUT true = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if got := env.configMapValue(t); got != "true" {
		t.Errorf("ConfigMap after flip = %q, want \"true\"", got)
	}
	rows := secSetFlipRows(t, env)
	if len(rows) != 1 || rows[0].Method != http.MethodPut || rows[0].Path != "/security-settings" ||
		rows[0].Reason != "allowRootOverrides:false->true" || rows[0].Status != http.StatusOK {
		t.Fatalf("flip rows = %+v, want one allowRootOverrides:false->true row", rows)
	}

	rr = secSetEnvDo(t, env.r, secSetViewer(), http.MethodGet, "")
	if !strings.Contains(rr.Body.String(), `"allowRootOverrides":true`) {
		t.Errorf("GET after flip = %s, want allowRootOverrides true", rr.Body.String())
	}

	// An unchanged value writes nothing and adds no row.
	rr = secSetEnvDo(t, env.r, adminUser(), http.MethodPut, `{"allowRootOverrides":true}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("unchanged PUT = %d, want 200", rr.Code)
	}
	if n := len(secSetFlipRows(t, env)); n != 1 {
		t.Errorf("flip rows after unchanged PUT = %d, want 1", n)
	}
}

func TestSecuritySettingsEnv_GETMissingReportsNotPresent(t *testing.T) {
	env := secAuditSetup(t, kubeC)
	rr := secSetEnvDo(t, env.r, secSetViewer(), http.MethodGet, "")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"present":false`) {
		t.Fatalf("GET missing = %d %s, want present false", rr.Code, rr.Body.String())
	}
}

func TestSecuritySettingsEnv_ViewerPUTForbidden(t *testing.T) {
	env := secAuditSetup(t, kubeC)
	env.putConfigMap(t, "false")

	rr := secSetEnvDo(t, env.r, secSetViewer(), http.MethodPut, `{"allowRootOverrides":true}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("viewer PUT = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
	if got := env.configMapValue(t); got != "false" {
		t.Errorf("ConfigMap after refused PUT = %q, want \"false\"", got)
	}
	if n := len(secSetFlipRows(t, env)); n != 0 {
		t.Errorf("flip rows after refused PUT = %d, want 0", n)
	}
}

func TestSecuritySettingsEnv_MissingConfigMap409NoRowNoCreate(t *testing.T) {
	env := secAuditSetup(t, kubeC)

	rr := secSetEnvDo(t, env.r, adminUser(), http.MethodPut, `{"allowRootOverrides":true}`)
	if rr.Code != http.StatusConflict {
		t.Fatalf("PUT with missing ConfigMap = %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
	if n := len(secSetFlipRows(t, env)); n != 0 {
		t.Errorf("flip rows with missing ConfigMap = %d, want 0", n)
	}
	if _, err := kubeC.Typed.CoreV1().ConfigMaps(env.ns).Get(context.Background(),
		securitySettingsConfigMap, metav1.GetOptions{}); err == nil {
		t.Error("PUT created the switch ConfigMap; nothing may create it")
	}
}

func TestSecuritySettingsEnv_AuditFailureLeavesValue(t *testing.T) {
	env := secAuditSetup(t, kubeC)
	env.putConfigMap(t, "false")
	secAuditFailOn(t, env.store, "allowRootOverrides:%")

	rr := secSetEnvDo(t, env.r, adminUser(), http.MethodPut, `{"allowRootOverrides":true}`)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("PUT with failing audit = %d, want 500; body=%s", rr.Code, rr.Body.String())
	}
	if got := env.configMapValue(t); got != "false" {
		t.Errorf("ConfigMap after failed audit = %q, want \"false\"", got)
	}
}

func TestSecuritySettingsEnv_UpdateFailureWritesSaveFailed(t *testing.T) {
	failing := &kube.Client{Dynamic: kubeC.Dynamic, Typed: secAuditFailUpdateTyped{kubeC.Typed}}
	env := secAuditSetup(t, failing)
	env.putConfigMap(t, "false")

	rr := secSetEnvDo(t, env.r, adminUser(), http.MethodPut, `{"allowRootOverrides":true}`)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("PUT with failing update = %d, want 502; body=%s", rr.Code, rr.Body.String())
	}
	if got := env.configMapValue(t); got != "false" {
		t.Errorf("ConfigMap after failed update = %q, want \"false\"", got)
	}
	rows := secSetFlipRows(t, env)
	if len(rows) != 2 || rows[0].Reason != "allowRootOverrides:false->true" || rows[0].Status != http.StatusOK ||
		rows[1].Reason != "security_save_failed" || rows[1].Status != http.StatusBadGateway {
		t.Fatalf("flip rows = %+v, want the flip row then security_save_failed (502)", rows)
	}
}
