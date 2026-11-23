//go:build envtest

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"

	"github.com/GameplanePanel/gameplane/api/internal/audit"
	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/rbac"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// T049 (envtest): audit rows for security overrides and the allow-root switch,
// plus the failure fallbacks of contracts/audit-events.md "Write order".
//
// The shared harness below (secAuditSetup and the failing client wrappers) is
// also used by security_settings_envtest_test.go and
// security_overrides_root_envtest_test.go. Failures are injected with SQLite
// triggers on audit_events, which fail only the rows whose reason matches, so
// the generic middleware row still lands and can be told apart.

// secAuditEnv is one router with the audit middleware, rbac and every handler
// that writes security rows, backed by its own audit store and a home
// registry on client. The control-plane namespace holds the switch ConfigMap.
type secAuditEnv struct {
	r      *chi.Mux
	store  *db.Store
	client *kube.Client
	ns     string
}

// secAuditStore opens a private in-memory audit store for one test.
func secAuditStore(t *testing.T) *db.Store {
	t.Helper()
	dsn := "file:" + uniqueResourceName("secaudit") + "?mode=memory&cache=shared&_pragma=journal_mode(WAL)"
	store, err := db.Open(t.Context(), "sqlite", dsn)
	if err != nil {
		t.Fatalf("open audit store: %v", err)
	}
	if err := store.Migrate(t.Context()); err != nil {
		_ = store.Close()
		t.Fatalf("migrate audit store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// secAuditSetup builds the router. It creates a control-plane namespace
// through client but no ConfigMap; tests add one with putConfigMap.
func secAuditSetup(t *testing.T, client *kube.Client) *secAuditEnv {
	t.Helper()
	store := secAuditStore(t)
	auditor := audit.New(store)

	ns := uniqueResourceName("sec-cfg")
	if _, err := client.Typed.CoreV1().Namespaces().Create(t.Context(),
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create control-plane namespace %s: %v", ns, err)
	}

	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.Set(scope.DefaultCluster, client)
	reg.SetControlPlaneNamespace(ns)

	r := chi.NewRouter()
	r.Use(audit.Middleware(auditor))
	r.Use(rbac.Middleware(reg))
	MountResources(r, reg, nil, auditor)
	MountLifecycle(r, reg, auditor)
	MountSecuritySettings(r, reg, auditor)
	MountAudit(r, auditor)
	return &secAuditEnv{r: r, store: store, client: client, ns: ns}
}

// putConfigMap creates the switch ConfigMap with value in the env namespace.
func (e *secAuditEnv) putConfigMap(t *testing.T, value string) {
	t.Helper()
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: securitySettingsConfigMap, Namespace: e.ns},
		Data:       map[string]string{securitySettingsKey: value},
	}
	if _, err := e.client.Typed.CoreV1().ConfigMaps(e.ns).Create(t.Context(), cm, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create switch ConfigMap: %v", err)
	}
}

// configMapValue reads the switch value straight from the apiserver.
func (e *secAuditEnv) configMapValue(t *testing.T) string {
	t.Helper()
	cm, err := kubeC.Typed.CoreV1().ConfigMaps(e.ns).Get(t.Context(), securitySettingsConfigMap, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get switch ConfigMap: %v", err)
	}
	return cm.Data[securitySettingsKey]
}

// secAuditRow is one audit_events row, the columns the tests compare.
type secAuditRow struct {
	Method string
	Path   string
	Target string
	Reason string
	Status int
}

// secAuditRows returns the rows matching where (SQL over the audit columns), in
// insertion order.
func secAuditRows(t *testing.T, store *db.Store, where string, args ...any) []secAuditRow {
	t.Helper()
	q := "SELECT method, path, COALESCE(target, ''), COALESCE(reason, ''), status FROM audit_events WHERE " +
		where + " ORDER BY id"
	rows, err := store.DB.QueryContext(t.Context(), q, args...)
	if err != nil {
		t.Fatalf("query audit rows: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var out []secAuditRow
	for rows.Next() {
		var row secAuditRow
		if err := rows.Scan(&row.Method, &row.Path, &row.Target, &row.Reason, &row.Status); err != nil {
			t.Fatalf("scan audit row: %v", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate audit rows: %v", err)
	}
	return out
}

// secAuditFailOn makes every audit insert whose reason matches pattern (a SQL
// LIKE pattern without quotes) fail, so that the synchronous row cannot be
// written. Other rows, including the middleware row, are unaffected.
func secAuditFailOn(t *testing.T, store *db.Store, pattern string) {
	t.Helper()
	stmt := fmt.Sprintf(
		"CREATE TRIGGER sec_audit_fail BEFORE INSERT ON audit_events WHEN NEW.reason LIKE '%s' "+
			"BEGIN SELECT RAISE(ABORT, 'injected audit failure'); END", pattern)
	if _, err := store.DB.ExecContext(t.Context(), stmt); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}
}

// secAuditChange is the parsed reason of a change row.
type secAuditChange struct {
	Before map[string]any `json:"before"`
	After  map[string]any `json:"after"`
	Risky  []string       `json:"risky"`
}

// secAuditParseChange parses a security:{...} reason and fails on anything else.
func secAuditParseChange(t *testing.T, reason string) secAuditChange {
	t.Helper()
	raw, ok := strings.CutPrefix(reason, "security:")
	if !ok {
		t.Fatalf("reason %q does not start with security:", reason)
	}
	var c secAuditChange
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatalf("parse change reason %q: %v", reason, err)
	}
	return c
}

// secAuditChangeRows returns the rows for target whose reason is a change row.
func secAuditChangeRows(t *testing.T, store *db.Store, target string) []secAuditRow {
	t.Helper()
	return secAuditRows(t, store, "target = ? AND reason LIKE 'security:%'", target)
}

// secAuditSaveError is the Kubernetes error an injected failing write returns.
func secAuditSaveError(name string) error {
	return apierrors.NewConflict(gvrServers().GroupResource(), name, errors.New("injected save failure"))
}

// secAuditFailDynamic fails every create and update of a namespaced resource
// after the handler has written its change row. Reads pass through.
type secAuditFailDynamic struct{ dynamic.Interface }

func (d secAuditFailDynamic) Resource(g schema.GroupVersionResource) dynamic.NamespaceableResourceInterface {
	return secAuditFailNamespaceable{d.Interface.Resource(g)}
}

type secAuditFailNamespaceable struct {
	dynamic.NamespaceableResourceInterface
}

func (n secAuditFailNamespaceable) Namespace(ns string) dynamic.ResourceInterface {
	return secAuditFailResource{n.NamespaceableResourceInterface.Namespace(ns)}
}

type secAuditFailResource struct{ dynamic.ResourceInterface }

func (secAuditFailResource) Create(_ context.Context, obj *unstructured.Unstructured, _ metav1.CreateOptions, _ ...string) (*unstructured.Unstructured, error) {
	return nil, secAuditSaveError(obj.GetName())
}

func (secAuditFailResource) Update(_ context.Context, obj *unstructured.Unstructured, _ metav1.UpdateOptions, _ ...string) (*unstructured.Unstructured, error) {
	return nil, secAuditSaveError(obj.GetName())
}

// secAuditFailUpdateTyped fails every ConfigMap update. Reads and creates pass through.
type secAuditFailUpdateTyped struct{ kubernetes.Interface }

func (c secAuditFailUpdateTyped) CoreV1() typedcorev1.CoreV1Interface {
	return secAuditFailUpdateCoreV1{c.Interface.CoreV1()}
}

type secAuditFailUpdateCoreV1 struct{ typedcorev1.CoreV1Interface }

func (c secAuditFailUpdateCoreV1) ConfigMaps(ns string) typedcorev1.ConfigMapInterface {
	return secAuditFailUpdateConfigMaps{c.CoreV1Interface.ConfigMaps(ns)}
}

type secAuditFailUpdateConfigMaps struct{ typedcorev1.ConfigMapInterface }

func (secAuditFailUpdateConfigMaps) Update(_ context.Context, cm *corev1.ConfigMap, _ metav1.UpdateOptions) (*corev1.ConfigMap, error) {
	return nil, apierrors.NewInternalError(errors.New("injected update failure for " + cm.GetName()))
}

// secAuditDeleteServer removes a server fixture at cleanup, ignoring NotFound.
func secAuditDeleteServer(t *testing.T, name string) {
	t.Cleanup(func() {
		_ = kubeC.Dynamic.Resource(gvrServers()).
			Namespace(scope.DefaultNamespace).
			Delete(context.Background(), name, metav1.DeleteOptions{})
	})
}

func TestSecurityAuditEnv_CreateWritesOneChangeRow(t *testing.T) {
	env := secAuditSetup(t, kubeC)
	name := uniqueResourceName("sec-aud-create")
	secAuditDeleteServer(t, name)

	rr := secOvDo(t, env.r, adminUser(), http.MethodPost, "/servers", secOvPutBody(name, secOvStored(), nil))
	if rr.Code != http.StatusCreated {
		t.Fatalf("admin create = %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	rows := secAuditChangeRows(t, env.store, name)
	if len(rows) != 1 {
		t.Fatalf("change rows for %s = %d, want exactly 1: %+v", name, len(rows), rows)
	}
	row := rows[0]
	if row.Method != http.MethodPost || row.Path != "/servers" || row.Status != http.StatusCreated {
		t.Errorf("row = %+v, want POST /servers status 201", row)
	}
	change := secAuditParseChange(t, row.Reason)
	if len(change.Before) != 0 {
		t.Errorf("create before = %#v, want empty", change.Before)
	}
	if got := change.After["runAsUser"]; got != float64(2000) {
		t.Errorf("create after.runAsUser = %#v, want 2000", got)
	}
}

func TestSecurityAuditEnv_UpdateChangeRowAndNoRowWhenUnchanged(t *testing.T) {
	env := secAuditSetup(t, kubeC)
	name := uniqueResourceName("sec-aud-upd")
	secOvFixture(t, name, secOvStored())

	rr := secOvDo(t, env.r, adminUser(), http.MethodPut, "/servers/"+name,
		secOvPutBody(name, map[string]any{"runAsUser": map[string]any{"value": int64(3000)}}, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("admin update = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	rows := secAuditChangeRows(t, env.store, name)
	if len(rows) != 1 {
		t.Fatalf("change rows after update = %d, want 1: %+v", len(rows), rows)
	}
	change := secAuditParseChange(t, rows[0].Reason)
	if change.Before["runAsUser"] != float64(2000) || change.After["runAsUser"] != float64(3000) {
		t.Errorf("update reason = %s, want before 2000 and after 3000", rows[0].Reason)
	}
	if rows[0].Status != http.StatusOK {
		t.Errorf("update row status = %d, want 200", rows[0].Status)
	}

	// An unrelated edit that sends the same block back writes no security row.
	rr = secOvDo(t, env.r, adminUser(), http.MethodPut, "/servers/"+name,
		secOvPutBody(name, map[string]any{"runAsUser": map[string]any{"value": int64(3000)}},
			map[string]any{"sec-test": "same-block"}))
	if rr.Code != http.StatusOK {
		t.Fatalf("unrelated edit = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if n := len(secAuditChangeRows(t, env.store, name)); n != 1 {
		t.Errorf("security rows after unrelated edit = %d, want still 1", n)
	}
}

func TestSecurityAuditEnv_CloneChangeRow(t *testing.T) {
	env := secAuditSetup(t, kubeC)
	src := uniqueResourceName("sec-aud-csrc")
	clone := uniqueResourceName("sec-aud-cdst")
	secOvFixture(t, src, secOvStored())
	secAuditDeleteServer(t, clone)

	rr := secOvDo(t, env.r, adminUser(), http.MethodPost, "/servers/"+src+":clone", map[string]any{"newName": clone})
	if rr.Code/100 != 2 {
		t.Fatalf("admin clone = %d, want 2xx; body=%s", rr.Code, rr.Body.String())
	}
	rows := secAuditChangeRows(t, env.store, clone)
	if len(rows) != 1 {
		t.Fatalf("clone change rows = %d, want exactly 1: %+v", len(rows), rows)
	}
	if rows[0].Status != rr.Code {
		t.Errorf("clone row status = %d, want the response status %d", rows[0].Status, rr.Code)
	}
	change := secAuditParseChange(t, rows[0].Reason)
	if len(change.Before) != 0 || change.After["runAsUser"] != float64(2000) {
		t.Errorf("clone reason = %s, want empty before and after runAsUser 2000", rows[0].Reason)
	}
}

func TestSecurityAuditEnv_PermissionRefusalRow(t *testing.T) {
	env := secAuditSetup(t, kubeC)
	name := uniqueResourceName("sec-aud-forbid")
	secOvFixture(t, name, secOvStored())

	rr := secOvDo(t, env.r, secOvWriter(), http.MethodPut, "/servers/"+name,
		secOvPutBody(name, secOvChanged(), nil))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("writer changed block = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
	rows := secAuditRows(t, env.store, "target = ?", name)
	if len(rows) != 1 || rows[0].Reason != "security_forbidden" || rows[0].Status != http.StatusForbidden {
		t.Fatalf("rows = %+v, want one security_forbidden row with status 403", rows)
	}
}

func TestSecurityAuditEnv_RootRefusalRows(t *testing.T) {
	env := secAuditSetup(t, kubeC)
	env.putConfigMap(t, "false")

	t.Run("update", func(t *testing.T) {
		name := uniqueResourceName("sec-aud-root-upd")
		secOvFixture(t, name, secOvStored())
		rr := secOvDo(t, env.r, secOvHolder(), http.MethodPut, "/servers/"+name,
			secOvPutBody(name, map[string]any{"runAsUser": map[string]any{"value": int64(0)}}, nil))
		if rr.Code != http.StatusForbidden {
			t.Fatalf("root update while off = %d, want 403; body=%s", rr.Code, rr.Body.String())
		}
		rows := secAuditRows(t, env.store, "target = ?", name)
		if len(rows) != 1 || rows[0].Reason != "security_root_disabled:runAsUser" || rows[0].Status != http.StatusForbidden {
			t.Fatalf("rows = %+v, want one security_root_disabled:runAsUser row with status 403", rows)
		}
	})

	t.Run("create", func(t *testing.T) {
		name := uniqueResourceName("sec-aud-root-create")
		secAuditDeleteServer(t, name)
		rr := secOvDo(t, env.r, secOvHolder(), http.MethodPost, "/servers",
			secOvPutBody(name, map[string]any{"fsGroup": map[string]any{"value": int64(0)}}, nil))
		if rr.Code != http.StatusForbidden {
			t.Fatalf("root create while off = %d, want 403; body=%s", rr.Code, rr.Body.String())
		}
		rows := secAuditRows(t, env.store, "target = ?", name)
		if len(rows) != 1 || rows[0].Reason != "security_root_disabled:fsGroup" {
			t.Fatalf("rows = %+v, want one security_root_disabled:fsGroup row", rows)
		}
	})
}

func TestSecurityAuditEnv_InvalidRefusalRow(t *testing.T) {
	env := secAuditSetup(t, kubeC)
	name := uniqueResourceName("sec-aud-invalid")
	secAuditDeleteServer(t, name)

	rr := secOvDo(t, env.r, secOvHolder(), http.MethodPost, "/servers",
		secOvPutBody(name, map[string]any{"fsGroup": map[string]any{"value": int64(-1)}}, nil))
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid fsGroup = %d, want 422; body=%s", rr.Code, rr.Body.String())
	}
	rows := secAuditRows(t, env.store, "target = ?", name)
	if len(rows) != 1 || rows[0].Reason != "security_invalid:fsGroup" || rows[0].Status != http.StatusUnprocessableEntity {
		t.Fatalf("rows = %+v, want one security_invalid:fsGroup row with status 422", rows)
	}
}

// A failed synchronous change row refuses the save: 500, nothing stored, and
// the middleware row records the failed request as the fallback.
func TestSecurityAuditEnv_ChangeRowFailureStopsSave(t *testing.T) {
	env := secAuditSetup(t, kubeC)
	secAuditFailOn(t, env.store, "security:%")
	name := uniqueResourceName("sec-aud-wfail")
	secAuditDeleteServer(t, name)

	rr := secOvDo(t, env.r, secOvHolder(), http.MethodPost, "/servers", secOvPutBody(name, secOvStored(), nil))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("create with failing audit = %d, want 500; body=%s", rr.Code, rr.Body.String())
	}
	if _, err := kubeC.Dynamic.Resource(gvrServers()).Namespace(scope.DefaultNamespace).
		Get(t.Context(), name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("server after failed audit write: err = %v, want NotFound (nothing saved)", err)
	}
	fallback := secAuditRows(t, env.store, "path = ? AND method = ?", "/servers", http.MethodPost)
	if len(fallback) != 1 || fallback[0].Reason != "" || fallback[0].Status != http.StatusInternalServerError {
		t.Errorf("fallback rows = %+v, want one generic row with status 500", fallback)
	}
}

// A change row followed by a failed Kubernetes write adds security_save_failed.
func TestSecurityAuditEnv_SaveFailedRows(t *testing.T) {
	failing := &kube.Client{Dynamic: secAuditFailDynamic{kubeC.Dynamic}, Typed: kubeC.Typed}

	t.Run("create", func(t *testing.T) {
		env := secAuditSetup(t, failing)
		name := uniqueResourceName("sec-aud-sfc")
		rr := secOvDo(t, env.r, adminUser(), http.MethodPost, "/servers", secOvPutBody(name, secOvStored(), nil))
		if rr.Code != http.StatusConflict {
			t.Fatalf("create with failing save = %d, want 409; body=%s", rr.Code, rr.Body.String())
		}
		rows := secAuditRows(t, env.store, "target = ?", name)
		if len(rows) != 2 || !strings.HasPrefix(rows[0].Reason, "security:") || rows[0].Status != http.StatusCreated ||
			rows[1].Reason != "security_save_failed" || rows[1].Status != http.StatusConflict {
			t.Fatalf("rows = %+v, want change row (201) then security_save_failed (409)", rows)
		}
	})

	t.Run("update", func(t *testing.T) {
		env := secAuditSetup(t, failing)
		name := uniqueResourceName("sec-aud-sfu")
		secOvFixture(t, name, secOvStored())
		rr := secOvDo(t, env.r, adminUser(), http.MethodPut, "/servers/"+name,
			secOvPutBody(name, secOvChanged(), nil))
		if rr.Code != http.StatusConflict {
			t.Fatalf("update with failing save = %d, want 409; body=%s", rr.Code, rr.Body.String())
		}
		rows := secAuditRows(t, env.store, "target = ?", name)
		if len(rows) != 2 || !strings.HasPrefix(rows[0].Reason, "security:") || rows[0].Status != http.StatusOK ||
			rows[1].Reason != "security_save_failed" || rows[1].Status != http.StatusConflict {
			t.Fatalf("rows = %+v, want change row (200) then security_save_failed (409)", rows)
		}
	})
}

// A refusal whose row cannot be written keeps the response and leaves the
// generic middleware row as the record of the request.
func TestSecurityAuditEnv_RefusalRowFailureFallsBackToMiddleware(t *testing.T) {
	env := secAuditSetup(t, kubeC)
	secAuditFailOn(t, env.store, "security_forbidden")
	name := uniqueResourceName("sec-aud-rfall")
	secOvFixture(t, name, secOvStored())

	rr := secOvDo(t, env.r, secOvWriter(), http.MethodPut, "/servers/"+name,
		secOvPutBody(name, secOvChanged(), nil))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("refusal with failing audit = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
	fallback := secAuditRows(t, env.store, "path = ? AND method = ?", "/servers/"+name, http.MethodPut)
	if len(fallback) != 1 || fallback[0].Reason != "" || fallback[0].Status != http.StatusForbidden {
		t.Fatalf("fallback rows = %+v, want one generic row with status 403", fallback)
	}
}

// The flip row is written once per changed value, and the hash chain still verifies.
func TestSecurityAuditEnv_FlipRowAndChainVerifies(t *testing.T) {
	env := secAuditSetup(t, kubeC)
	env.putConfigMap(t, "false")

	rr := secOvDo(t, env.r, adminUser(), http.MethodPut, "/security-settings?cluster="+scope.DefaultCluster,
		map[string]any{"allowRootOverrides": true})
	if rr.Code != http.StatusOK {
		t.Fatalf("flip on = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	flips := secAuditRows(t, env.store, "target = ?", "security/"+scope.DefaultCluster)
	if len(flips) != 1 || flips[0].Method != http.MethodPut || flips[0].Path != "/security-settings" ||
		flips[0].Reason != "allowRootOverrides:false->true" || flips[0].Status != http.StatusOK {
		t.Fatalf("flip rows = %+v, want one allowRootOverrides:false->true row", flips)
	}

	name := uniqueResourceName("sec-aud-chain")
	secAuditDeleteServer(t, name)
	if rr := secOvDo(t, env.r, adminUser(), http.MethodPost, "/servers",
		secOvPutBody(name, secOvStored(), nil)); rr.Code != http.StatusCreated {
		t.Fatalf("create for chain = %d, want 201; body=%s", rr.Code, rr.Body.String())
	}

	rr = secOvDo(t, env.r, adminUser(), http.MethodGet, "/admin/audit/verify", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("verify = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var res audit.VerifyResult
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode verify: %v", err)
	}
	if !res.OK {
		t.Errorf("verify OK = false: %+v", res)
	}
}
