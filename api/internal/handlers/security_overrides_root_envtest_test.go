//go:build envtest

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// T048 (envtest): the root rule (case 5) through the create, update and clone
// handlers, against the envtest apiserver. The router is the dedicated one from
// secAuditSetup (rbac middleware, the real switch ConfigMap). Holders of
// servers:security are used, so the permission check passes and only the root
// rule decides.

// secRootSwitch sets the switch ConfigMap value through the apiserver.
func secRootSwitch(t *testing.T, env *secAuditEnv, value string) {
	t.Helper()
	cm, err := kubeC.Typed.CoreV1().ConfigMaps(env.ns).Get(context.Background(), securitySettingsConfigMap, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get switch ConfigMap: %v", err)
	}
	cm = cm.DeepCopy()
	cm.Data[securitySettingsKey] = value
	if _, err := kubeC.Typed.CoreV1().ConfigMaps(env.ns).Update(context.Background(), cm, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("update switch ConfigMap to %q: %v", value, err)
	}
}

// secRootProblem decodes the messageId and the setting argument of a refusal.
func secRootProblem(t *testing.T, rr interface{ Bytes() []byte }) (string, string) {
	t.Helper()
	var doc struct {
		MessageID string         `json:"messageId"`
		Args      map[string]any `json:"args"`
	}
	if err := json.Unmarshal(rr.Bytes(), &doc); err != nil {
		t.Fatalf("decode problem body: %v", err)
	}
	setting, _ := doc.Args["setting"].(string)
	return doc.MessageID, setting
}

func secRootBlock(members map[string]any) map[string]any { return members }

func secRootValue(v int64) map[string]any { return map[string]any{"value": v} }

func TestSecurityRootEnv_CreateRefusedWhileOffAcceptedWhenOn(t *testing.T) {
	env := secAuditSetup(t, kubeC)
	env.putConfigMap(t, "false")

	refused := uniqueResourceName("sec-root-create-off")
	secAuditDeleteServer(t, refused)
	rr := secOvDo(t, env.r, secOvHolder(), http.MethodPost, "/servers",
		secOvPutBody(refused, secRootBlock(map[string]any{"runAsUser": secRootValue(0)}), nil))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("root create while off = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
	if id, setting := secRootProblem(t, rr.Body); id != "server.security.root_disabled" || setting != "runAsUser" {
		t.Errorf("refusal = %s setting %q, want server.security.root_disabled runAsUser", id, setting)
	}
	if _, err := kubeC.Dynamic.Resource(gvrServers()).Namespace(scope.DefaultNamespace).
		Get(t.Context(), refused, metav1.GetOptions{}); err == nil {
		t.Error("refused create left a server behind")
	}

	secRootSwitch(t, env, "true")
	accepted := uniqueResourceName("sec-root-create-on")
	secAuditDeleteServer(t, accepted)
	rr = secOvDo(t, env.r, secOvHolder(), http.MethodPost, "/servers",
		secOvPutBody(accepted, secRootBlock(map[string]any{"runAsUser": secRootValue(0)}), nil))
	if rr.Code != http.StatusCreated {
		t.Fatalf("root create while on = %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
}

func TestSecurityRootEnv_FSGroupRootAndImageDefault(t *testing.T) {
	env := secAuditSetup(t, kubeC)
	env.putConfigMap(t, "false")

	rootFS := uniqueResourceName("sec-root-fs0")
	secAuditDeleteServer(t, rootFS)
	rr := secOvDo(t, env.r, secOvHolder(), http.MethodPost, "/servers",
		secOvPutBody(rootFS, secRootBlock(map[string]any{"fsGroup": secRootValue(0)}), nil))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("fsGroup 0 while off = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
	if _, setting := secRootProblem(t, rr.Body); setting != "fsGroup" {
		t.Errorf("refused setting = %q, want fsGroup", setting)
	}

	imageFS := uniqueResourceName("sec-root-fsimg")
	secAuditDeleteServer(t, imageFS)
	rr = secOvDo(t, env.r, secOvHolder(), http.MethodPost, "/servers",
		secOvPutBody(imageFS, secRootBlock(map[string]any{"fsGroup": map[string]any{"useImageDefault": true}}), nil))
	if rr.Code != http.StatusCreated {
		t.Fatalf("fsGroup image default while off = %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
}

func TestSecurityRootEnv_UpdateRefusedWhileOff(t *testing.T) {
	env := secAuditSetup(t, kubeC)
	env.putConfigMap(t, "false")
	name := uniqueResourceName("sec-root-upd")
	secOvFixture(t, name, secOvStored())

	rr := secOvDo(t, env.r, secOvHolder(), http.MethodPut, "/servers/"+name,
		secOvPutBody(name, secRootBlock(map[string]any{"runAsGroup": map[string]any{"useImageDefault": true}}), nil))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("root group update while off = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
	if sec, _ := secOvStoredSecurity(t, name); !securityValuesEqual(sec, secOvStored()) {
		t.Errorf("refused update changed the stored block: %#v", sec)
	}

	secRootSwitch(t, env, "true")
	rr = secOvDo(t, env.r, secOvHolder(), http.MethodPut, "/servers/"+name,
		secOvPutBody(name, secRootBlock(map[string]any{"runAsGroup": map[string]any{"useImageDefault": true}}), nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("root group update while on = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
}

// A stored root override survives unrelated saves while the setting is off,
// and removing or replacing it is always allowed.
func TestSecurityRootEnv_StoredRootUnrelatedSaveRemovalAndReplacement(t *testing.T) {
	env := secAuditSetup(t, kubeC)
	env.putConfigMap(t, "false")

	t.Run("unrelated save passes", func(t *testing.T) {
		name := uniqueResourceName("sec-root-keep")
		secOvFixture(t, name, secRootBlock(map[string]any{"runAsUser": secRootValue(0)}))
		rr := secOvDo(t, env.r, secOvHolder(), http.MethodPut, "/servers/"+name,
			secOvPutBody(name, secRootBlock(map[string]any{"runAsUser": secRootValue(0)}),
				map[string]any{"sec-test": "unrelated"}))
		if rr.Code != http.StatusOK {
			t.Fatalf("unrelated save with stored root while off = %d, want 200; body=%s", rr.Code, rr.Body.String())
		}
	})

	t.Run("replacement with non-root passes", func(t *testing.T) {
		name := uniqueResourceName("sec-root-repl")
		secOvFixture(t, name, secRootBlock(map[string]any{"runAsUser": secRootValue(0)}))
		rr := secOvDo(t, env.r, secOvHolder(), http.MethodPut, "/servers/"+name,
			secOvPutBody(name, secRootBlock(map[string]any{"runAsUser": secRootValue(2000)}), nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("replacement while off = %d, want 200; body=%s", rr.Code, rr.Body.String())
		}
	})

	t.Run("removal passes", func(t *testing.T) {
		name := uniqueResourceName("sec-root-rm")
		secOvFixture(t, name, secRootBlock(map[string]any{"runAsUser": secRootValue(0)}))
		rr := secOvDo(t, env.r, secOvHolder(), http.MethodPut, "/servers/"+name,
			secOvPutBody(name, nil, nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("removal while off = %d, want 200; body=%s", rr.Code, rr.Body.String())
		}
		if _, found := secOvStoredSecurity(t, name); found {
			t.Error("removal did not clear spec.security")
		}
	})
}

// Cloning a server that holds a root override is a new root value, so it is
// refused while off and accepted once on.
func TestSecurityRootEnv_CloneRefusedWhileOff(t *testing.T) {
	env := secAuditSetup(t, kubeC)
	env.putConfigMap(t, "false")
	src := uniqueResourceName("sec-root-csrc")
	secOvFixture(t, src, secRootBlock(map[string]any{"runAsUser": secRootValue(0)}))

	clone := uniqueResourceName("sec-root-cdst")
	secAuditDeleteServer(t, clone)
	rr := secOvDo(t, env.r, secOvHolder(), http.MethodPost, "/servers/"+src+":clone", map[string]any{"newName": clone})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("clone with root while off = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
	if id, _ := secRootProblem(t, rr.Body); id != "server.security.root_disabled" {
		t.Errorf("messageId = %q, want server.security.root_disabled", id)
	}

	secRootSwitch(t, env, "true")
	rr = secOvDo(t, env.r, secOvHolder(), http.MethodPost, "/servers/"+src+":clone", map[string]any{"newName": clone})
	if rr.Code/100 != 2 {
		t.Fatalf("clone with root while on = %d, want 2xx; body=%s", rr.Code, rr.Body.String())
	}
}
