package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// T047 (unit): the per-cluster allow-root-overrides switch, GET and PUT
// /security-settings, against a fake clientset. No rbac middleware here; the
// permission matrix is in security_settings_envtest_test.go.

const secSetNamespace = "gameplane-credentials"

// secSetRegistry builds a registry whose home cluster is backed by a fake
// clientset seeded with objs, with the home control-plane namespace set.
func secSetRegistry(objs ...runtime.Object) (*kube.Registry, *k8sfake.Clientset) {
	cs := k8sfake.NewSimpleClientset(objs...)
	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.Set(scope.DefaultCluster, &kube.Client{Typed: cs})
	reg.SetControlPlaneNamespace(secSetNamespace)
	return reg, cs
}

// secSetConfigMap is the switch ConfigMap with the given value.
func secSetConfigMap(value string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: securitySettingsConfigMap, Namespace: secSetNamespace},
		Data:       map[string]string{securitySettingsKey: value},
	}
}

// secSetRouter mounts the handler on a bare router without auditor.
func secSetRouter(reg *kube.Registry) http.Handler {
	r := chi.NewRouter()
	MountSecuritySettings(r, reg, nil)
	return r
}

// secSetDo sends method with body to /security-settings and returns the response.
func secSetDo(h http.Handler, method, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), method, "/security-settings", strings.NewReader(body))
	req.Header.Set("Accept", "application/problem+json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// secSetMessageID decodes the messageId of an RFC 9457 response.
func secSetMessageID(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var doc struct {
		MessageID string `json:"messageId"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode problem body %q: %v", rr.Body.String(), err)
	}
	return doc.MessageID
}

// secSetStoredValue reads the switch value stored in the fake clientset.
func secSetStoredValue(t *testing.T, cs *k8sfake.Clientset) string {
	t.Helper()
	cm, err := cs.CoreV1().ConfigMaps(secSetNamespace).Get(context.Background(), securitySettingsConfigMap, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get switch ConfigMap: %v", err)
	}
	return cm.Data[securitySettingsKey]
}

// secSetWrites reports whether cs recorded any write verb on ConfigMaps.
func secSetWrites(cs *k8sfake.Clientset) bool {
	for _, a := range cs.Actions() {
		switch a.GetVerb() {
		case "create", "update", "patch", "delete":
			if a.GetResource().Resource == "configmaps" {
				return true
			}
		}
	}
	return false
}

func TestSecuritySettingsDecode(t *testing.T) {
	ok := []struct {
		name string
		body string
		want bool
	}{
		{"true", `{"allowRootOverrides":true}`, true},
		{"false", `{"allowRootOverrides":false}`, false},
		{"unknown members dropped", `{"allowRootOverrides":true,"other":1}`, true},
	}
	for _, tc := range ok {
		t.Run(tc.name, func(t *testing.T) {
			got, err := decodeAllowRootOverrides([]byte(tc.body))
			if err != nil {
				t.Fatalf("decode %s: %v", tc.body, err)
			}
			if got != tc.want {
				t.Errorf("decode %s = %v, want %v", tc.body, got, tc.want)
			}
		})
	}

	bad := []string{
		`{"allowRootOverrides":"true"}`,
		`{"allowRootOverrides":1}`,
		`{"allowRootOverrides":null}`,
		`{}`,
		`[]`,
		`not json`,
	}
	for _, body := range bad {
		if _, err := decodeAllowRootOverrides([]byte(body)); err == nil {
			t.Errorf("decode %s: want error, got none", body)
		}
	}
}

func TestSecuritySettingsPUT_EmptyBody400(t *testing.T) {
	reg, _ := secSetRegistry(secSetConfigMap("false"))
	rr := secSetDo(secSetRouter(reg), http.MethodPut, "")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("empty PUT = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	if id := secSetMessageID(t, rr); id != "server.empty_body" {
		t.Errorf("messageId = %q, want server.empty_body", id)
	}
}

func TestSecuritySettingsPUT_InvalidBody422(t *testing.T) {
	for _, body := range []string{`{"allowRootOverrides":"yes"}`, `{}`, `not json`} {
		reg, cs := secSetRegistry(secSetConfigMap("false"))
		rr := secSetDo(secSetRouter(reg), http.MethodPut, body)
		if rr.Code != http.StatusUnprocessableEntity {
			t.Errorf("PUT %s = %d, want 422", body, rr.Code)
		}
		if secSetWrites(cs) {
			t.Errorf("PUT %s wrote the ConfigMap", body)
		}
	}
}

func TestSecuritySettingsGET_Missing(t *testing.T) {
	reg, _ := secSetRegistry()
	rr := secSetDo(secSetRouter(reg), http.MethodGet, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET missing = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var body securitySettingsBody
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.AllowRootOverrides || body.Present {
		t.Errorf("GET missing = %+v, want allowRootOverrides false and present false", body)
	}
}

func TestSecuritySettingsGET_Values(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"true", true},
		{"false", false},
		{"TRUE", false}, // only the exact value is on
		{"yes", false},
	}
	for _, tc := range cases {
		reg, _ := secSetRegistry(secSetConfigMap(tc.value))
		rr := secSetDo(secSetRouter(reg), http.MethodGet, "")
		if rr.Code != http.StatusOK {
			t.Fatalf("GET %q = %d, want 200", tc.value, rr.Code)
		}
		var body securitySettingsBody
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body.AllowRootOverrides != tc.want || !body.Present || body.Cluster != scope.DefaultCluster {
			t.Errorf("GET %q = %+v, want allowRootOverrides %v, present true, cluster %s",
				tc.value, body, tc.want, scope.DefaultCluster)
		}
	}
}

func TestSecuritySettingsGET_ReadErrorIs502(t *testing.T) {
	reg, cs := secSetRegistry(secSetConfigMap("true"))
	cs.PrependReactor("get", "configmaps", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewInternalError(errors.New("injected read failure"))
	})
	rr := secSetDo(secSetRouter(reg), http.MethodGet, "")
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("GET with read error = %d, want 502", rr.Code)
	}
}

func TestSecuritySettingsGET_UnsetNamespaceIs502(t *testing.T) {
	cs := k8sfake.NewSimpleClientset(secSetConfigMap("true"))
	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.Set(scope.DefaultCluster, &kube.Client{Typed: cs})
	rr := secSetDo(secSetRouter(reg), http.MethodGet, "")
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("GET without home namespace = %d, want 502 (fail closed)", rr.Code)
	}
}

func TestSecuritySettingsPUT_MissingConfigMap409AndNeverCreates(t *testing.T) {
	reg, cs := secSetRegistry()
	rr := secSetDo(secSetRouter(reg), http.MethodPut, `{"allowRootOverrides":true}`)
	if rr.Code != http.StatusConflict {
		t.Fatalf("PUT missing = %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
	if id := secSetMessageID(t, rr); id != "server.cluster.security_settings_missing" {
		t.Errorf("messageId = %q, want server.cluster.security_settings_missing", id)
	}
	if secSetWrites(cs) {
		t.Error("PUT with a missing ConfigMap wrote to the cluster; it must never create one")
	}
	if _, err := cs.CoreV1().ConfigMaps(secSetNamespace).Get(context.Background(), securitySettingsConfigMap, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("ConfigMap after refused PUT: err = %v, want NotFound", err)
	}
}

func TestSecuritySettingsPUT_UnchangedWritesNothing(t *testing.T) {
	reg, cs := secSetRegistry(secSetConfigMap("false"))
	rr := secSetDo(secSetRouter(reg), http.MethodPut, `{"allowRootOverrides":false}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT unchanged = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if secSetWrites(cs) {
		t.Error("unchanged PUT wrote the ConfigMap")
	}
	var body securitySettingsBody
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.AllowRootOverrides || !body.Present {
		t.Errorf("PUT body = %+v, want allowRootOverrides false, present true", body)
	}
}

func TestSecuritySettingsPUT_FlipBothWays(t *testing.T) {
	reg, cs := secSetRegistry(secSetConfigMap("false"))
	h := secSetRouter(reg)

	rr := secSetDo(h, http.MethodPut, `{"allowRootOverrides":true}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT true = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if got := secSetStoredValue(t, cs); got != "true" {
		t.Errorf("stored after PUT true = %q, want \"true\"", got)
	}
	var on securitySettingsBody
	if err := json.Unmarshal(rr.Body.Bytes(), &on); err != nil || !on.AllowRootOverrides || !on.Present {
		t.Errorf("PUT true body = %s, want allowRootOverrides true, present true", rr.Body.String())
	}

	rr = secSetDo(h, http.MethodPut, `{"allowRootOverrides":false}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT false = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if got := secSetStoredValue(t, cs); got != "false" {
		t.Errorf("stored after PUT false = %q, want \"false\"", got)
	}
}

func TestSecuritySettingsPUT_UpdateFailureIs502(t *testing.T) {
	reg, cs := secSetRegistry(secSetConfigMap("false"))
	cs.PrependReactor("update", "configmaps", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewInternalError(errors.New("injected update failure"))
	})
	rr := secSetDo(secSetRouter(reg), http.MethodPut, `{"allowRootOverrides":true}`)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("PUT with update failure = %d, want 502; body=%s", rr.Code, rr.Body.String())
	}
	if got := secSetStoredValue(t, cs); got != "false" {
		t.Errorf("stored after failed update = %q, want \"false\"", got)
	}
}
