//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// securityServerBody builds a GameServer request body for POST and PUT.
func securityServerBody(ns, gs, tmpl string, security map[string]any) map[string]any {
	spec := map[string]any{"templateRef": map[string]any{"name": tmpl}}
	if security != nil {
		spec["security"] = security
	}
	meta := map[string]any{"name": gs, "namespace": ns}
	// A PUT is an optimistic update, so it carries the stored version. A POST
	// of a new server finds nothing and sends none.
	if live, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
		Get(context.Background(), gs, metav1.GetOptions{}); err == nil {
		meta["resourceVersion"] = live.GetResourceVersion()
	}
	return map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   meta,
		"spec":       spec,
	}
}

// securitySettingsResponse is the GET and PUT body of /security-settings.
type securitySettingsResponse struct {
	Cluster            string `json:"cluster"`
	AllowRootOverrides bool   `json:"allowRootOverrides"`
	Present            bool   `json:"present"`
}

// TestAPI_SecurityOverride_PermissionAndRootRule checks the API side of server
// security overrides with one admin login and one login as an operator-role
// user. It covers the servers:security permission gate, the root rule and
// the per-cluster /security-settings switch that the root rule reads.
func TestAPI_SecurityOverride_PermissionAndRootRule(t *testing.T) {
	t.Parallel()

	// The switch is shared with the operator E2E test; hold the lock for the
	// whole test. Cleanups run in LIFO order, so the unlock is registered first.
	securityRootSettingMu.Lock()
	t.Cleanup(securityRootSettingMu.Unlock)

	ns := "gameplane-games"
	suffix := strconv.FormatInt(time.Now().UnixNano()%1e10, 10)
	tmpl := "e2e-sec-api-tmpl-" + suffix
	gs := "e2e-sec-api-" + suffix
	gsDenied := gs + "-denied"
	roleName := "e2e-sec-role-" + suffix

	cpNS := securityControlPlaneNamespace(t)
	setRootOverrideSwitch(t, cpNS, "false")
	t.Cleanup(func() {
		setRootOverrideSwitch(t, cpNS, "false")
	})

	envInstance.BootstrapAdmin(t, adminUsername, adminPassword)
	admin := envInstance.APIClient(t, adminUsername, adminPassword)
	defer admin.Close()

	// The switch starts off, is present, and is reported for the home cluster.
	resp, body, err := admin.Get("/security-settings?cluster=local")
	if err != nil {
		t.Fatalf("GET /security-settings: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /security-settings status=%d body=%s", resp.StatusCode, body)
	}
	var initial securitySettingsResponse
	if err := json.Unmarshal(body, &initial); err != nil {
		t.Fatalf("decode GET /security-settings: %v: %s", err, body)
	}
	if initial.Cluster != "local" || !initial.Present || initial.AllowRootOverrides {
		t.Fatalf("GET /security-settings initial state = %+v, want local, present, off", initial)
	}

	// An operator-level custom role: the server permissions an operator holds,
	// without servers:security. The grant below adds servers:security to it.
	roleResp, roleBody, err := admin.Post("/roles", map[string]any{
		"name":        roleName,
		"description": "Server writer for the security override test",
		"permissions": []string{"servers:read", "servers:write"},
	})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}
	_ = roleResp.Body.Close()
	if roleResp.StatusCode != http.StatusCreated {
		t.Fatalf("create role status=%d body=%s", roleResp.StatusCode, roleBody)
	}
	t.Cleanup(func() {
		r, _, _ := admin.Delete("/roles/" + roleName)
		if r != nil {
			_ = r.Body.Close()
		}
	})

	userName, userPW, userID := envInstance.CreateUser(t, admin, roleName, "e2e-sec-api")
	t.Cleanup(func() {
		r, _, _ := admin.Delete("/users/" + userID)
		if r != nil {
			_ = r.Body.Close()
		}
	})
	user := envInstance.APIClient(t, userName, userPW)
	defer user.Close()

	applyBusyboxTemplate(t, tmpl)
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Delete(context.Background(), gs, metav1.DeleteOptions{})
		_ = envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Delete(context.Background(), gsDenied, metav1.DeleteOptions{})
	})

	// expect runs one API call, closes its response, checks the status and
	// returns the body.
	expect := func(what string, want int, do func() (*http.Response, []byte, error)) []byte {
		t.Helper()
		r, b, err := do()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		_ = r.Body.Close()
		if r.StatusCode != want {
			t.Fatalf("%s: status=%d want=%d body=%s", what, r.StatusCode, want, b)
		}
		return b
	}

	// Permission gate, without servers:security. Create and update that set a
	// run-as override are refused; a plain create is accepted.
	runAs := map[string]any{"runAsUser": map[string]any{"value": int64(1001)}}
	expect("user create with override", http.StatusForbidden, func() (*http.Response, []byte, error) {
		return user.Post("/servers", securityServerBody(ns, gsDenied, tmpl, runAs))
	})

	r, b, err := user.Post("/servers", securityServerBody(ns, gs, tmpl, nil))
	if err != nil {
		t.Fatalf("user create plain server: %v", err)
	}
	_ = r.Body.Close()
	if r.StatusCode != http.StatusOK && r.StatusCode != http.StatusCreated {
		t.Fatalf("user create plain server: status=%d body=%s", r.StatusCode, b)
	}

	expect("user update with override", http.StatusForbidden, func() (*http.Response, []byte, error) {
		return user.Do(http.MethodPut, "/servers/"+gs+"?namespace="+ns, securityServerBody(ns, gs, tmpl, runAs))
	})

	// Grant servers:security to the role. The same user can now set an override.
	expect("grant servers:security", http.StatusOK, func() (*http.Response, []byte, error) {
		return admin.Patch("/roles/"+roleName, map[string]any{
			"permissions": []string{"servers:read", "servers:write", "servers:security"},
		})
	})

	expect("granted user update with override", http.StatusOK, func() (*http.Response, []byte, error) {
		return user.Do(http.MethodPut, "/servers/"+gs+"?namespace="+ns, securityServerBody(ns, gs, tmpl, runAs))
	})

	// Root rule: a root value is refused while the cluster switch is off.
	root := map[string]any{"fsGroup": map[string]any{"value": int64(0)}}
	expect("root value with switch off", http.StatusForbidden, func() (*http.Response, []byte, error) {
		return user.Do(http.MethodPut, "/servers/"+gs+"?namespace="+ns, securityServerBody(ns, gs, tmpl, root))
	})

	// Admin turns the switch on for this cluster. The ConfigMap follows.
	fb := expect("admin switch on", http.StatusOK, func() (*http.Response, []byte, error) {
		return admin.Do(http.MethodPut, "/security-settings?cluster=local", map[string]any{"allowRootOverrides": true})
	})
	var on securitySettingsResponse
	if err := json.Unmarshal(fb, &on); err != nil || !on.AllowRootOverrides || !on.Present {
		t.Fatalf("PUT /security-settings on: body=%s err=%v", fb, err)
	}
	cm, err := envInstance.K8s.CoreV1().ConfigMaps(cpNS).Get(context.Background(), securitySwitchConfigMap, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get switch ConfigMap: %v", err)
	}
	if got := cm.Data["allowRootOverrides"]; got != "true" {
		t.Fatalf("switch ConfigMap allowRootOverrides=%q after PUT true, want \"true\"", got)
	}

	expect("root value with switch on", http.StatusOK, func() (*http.Response, []byte, error) {
		return user.Do(http.MethodPut, "/servers/"+gs+"?namespace="+ns, securityServerBody(ns, gs, tmpl, root))
	})

	// Admin turns the switch off again. The ConfigMap follows.
	expect("admin switch off", http.StatusOK, func() (*http.Response, []byte, error) {
		return admin.Do(http.MethodPut, "/security-settings?cluster=local", map[string]any{"allowRootOverrides": false})
	})
	cm, err = envInstance.K8s.CoreV1().ConfigMaps(cpNS).Get(context.Background(), securitySwitchConfigMap, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get switch ConfigMap: %v", err)
	}
	if got := cm.Data["allowRootOverrides"]; got != "false" {
		t.Fatalf("switch ConfigMap allowRootOverrides=%q after PUT false, want \"false\"", got)
	}

	// Audit: the flip and the refused root write are both recorded.
	ar, ab, err := admin.Get("/admin/audit?limit=500")
	if err != nil {
		t.Fatalf("/admin/audit: %v", err)
	}
	_ = ar.Body.Close()
	if ar.StatusCode != http.StatusOK {
		t.Fatalf("/admin/audit status=%d body=%s", ar.StatusCode, ab)
	}
	for _, want := range []string{"security/local", "allowRootOverrides:false-", "security_root_disabled"} {
		if !strings.Contains(string(ab), want) {
			t.Errorf("audit log missing %q", want)
		}
	}
}
