//go:build envtest

package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/api/internal/audit"
	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/db"
)

// Spec 034 auth config: role rules naming any existing role, the store-aware
// role check on save, the one-form-per-provider rule (OD-1), the any-role
// fallback (OD-7), the Helm override's rules and fallback (OD-3), and old-form
// rows that keep loading (FR-019).

// newRoleRulesConfigServer mounts the config routes on a fresh store with an
// optional Helm policy and a real auditor (helmOverride saves audit). The
// auth row a test writes is removed when it ends.
func newRoleRulesConfigServer(t *testing.T, helm *auth.ProviderPolicy) (*httptest.Server, *db.Store) {
	t.Helper()
	store := newTestStore(t)
	r := chi.NewRouter()
	MountConfig(r, store, audit.New(store), helm != nil, "", helm)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	t.Cleanup(func() {
		_, _ = store.DB.ExecContext(t.Context(), `DELETE FROM config WHERE key = 'auth'`)
	})
	return srv, store
}

// rrLocal is an enabled local provider, so every body below passes the
// "at least one provider enabled" guard.
const rrLocal = `{"name":"local","kind":"local","enabled":true}`

// rrOIDC returns an enabled OIDC provider entry with the given extra fields.
func rrOIDC(name, extra string) string {
	return `{"name":"` + name + `","kind":"oidc","enabled":true,"issuer":"https://idp.example","clientID":"gameplane"` + extra + `}`
}

// putAuth saves body as the auth section, asking for problem+json errors so
// a refusal carries its messageId.
func putAuth(t *testing.T, srv *httptest.Server, body string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, srv.URL+"/admin/config/auth", strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/problem+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("put auth: %v", err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, out
}

// storedAuth returns the stored auth row, or "" when there is none.
func storedAuth(t *testing.T, store *db.Store) string {
	t.Helper()
	raw, ok, err := store.ConfigValue(t.Context(), "auth")
	if err != nil {
		t.Fatalf("read auth row: %v", err)
	}
	if !ok {
		return ""
	}
	return raw
}

// getConfig returns GET /admin/config as section → raw JSON.
func getConfig(t *testing.T, srv *httptest.Server) map[string]json.RawMessage {
	t.Helper()
	status, body := doReq(t, http.MethodGet, srv.URL+"/admin/config", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /admin/config: %d %s", status, body)
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode config: %v; body=%s", err, body)
	}
	return out
}

// wantRefused requires a 400 problem document with messageID and, when
// role is non-empty, args.role naming it.
func wantRefused(t *testing.T, status int, body []byte, messageID, role string) {
	t.Helper()
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", status, body)
	}
	var problem struct {
		MessageID string         `json:"messageId"`
		Args      map[string]any `json:"args"`
	}
	if err := json.Unmarshal(body, &problem); err != nil {
		t.Fatalf("decode problem: %v; body=%s", err, body)
	}
	if problem.MessageID != messageID {
		t.Fatalf("messageId = %q, want %q; body=%s", problem.MessageID, messageID, body)
	}
	if role != "" && problem.Args["role"] != role {
		t.Fatalf("args.role = %v, want %q; body=%s", problem.Args["role"], role, body)
	}
}

func TestConfigRoleRules_SaveAcceptsCustomRoleAndTrims(t *testing.T) {
	srv, store := newRoleRulesConfigServer(t, nil)
	seedRole(t, store, "rr-login-only", "servers:read")

	status, body := putAuth(t, srv, `{"providers":[`+rrLocal+`,`+rrOIDC("authentik",
		`,"roleRules":[{"group":"sys-admin","role":"admin"},{"group":" gameplane login only ","role":" rr-login-only "}],"defaultRole":"deny"`)+`]}`)
	if status != http.StatusOK {
		t.Fatalf("save: %d %s", status, body)
	}
	raw := storedAuth(t, store)
	if !strings.Contains(raw, `"roleRules":[{"group":"sys-admin","role":"admin"},{"group":"gameplane login only","role":"rr-login-only"}]`) {
		t.Fatalf("stored rules not in order or not trimmed: %s", raw)
	}
	if strings.Contains(raw, "roleMappings") {
		t.Fatalf("stored row grew roleMappings: %s", raw)
	}
}

func TestConfigRoleRules_SaveRefusesUnknownRuleRole(t *testing.T) {
	srv, store := newRoleRulesConfigServer(t, nil)

	status, body := putAuth(t, srv, `{"providers":[`+rrLocal+`,`+rrOIDC("authentik",
		`,"roleRules":[{"group":"ops","role":"rr-ghost"}]`)+`]}`)
	wantRefused(t, status, body, "server.config.auth_role_unknown", "rr-ghost")
	if raw := storedAuth(t, store); raw != "" {
		t.Fatalf("refused save was stored: %s", raw)
	}
}

func TestConfigRoleRules_SaveRefusesUnknownDefaultRole(t *testing.T) {
	srv, _ := newRoleRulesConfigServer(t, nil)

	status, body := putAuth(t, srv, `{"providers":[`+rrLocal+`,`+rrOIDC("authentik",
		`,"roleRules":[],"defaultRole":"rr-ghost-default"`)+`]}`)
	wantRefused(t, status, body, "server.config.auth_role_unknown", "rr-ghost-default")
}

// OD-1: a provider or the Helm override sets the old keys or the rules, never both.
func TestConfigRoleRules_SaveRefusesBothForms(t *testing.T) {
	srv, store := newRoleRulesConfigServer(t, nil)

	status, body := putAuth(t, srv, `{"providers":[`+rrLocal+`,`+rrOIDC("authentik",
		`,"roleMappings":{"admin":["a"]},"roleRules":[{"group":"ops","role":"operator"}]`)+`]}`)
	wantRefused(t, status, body, "server.config.auth_both_mapping_forms", "")

	status, body = putAuth(t, srv, `{"providers":[`+rrLocal+`],"helmOverride":{`+
		`"roleMappings":{"admin":["a"]},"roleRules":[{"group":"ops","role":"operator"}]}}`)
	wantRefused(t, status, body, "server.config.auth_both_mapping_forms", "")
	if raw := storedAuth(t, store); raw != "" {
		t.Fatalf("refused save was stored: %s", raw)
	}
}

// OD-7: the fallback may be any existing role, a built-in one, or deny.
func TestConfigRoleRules_DefaultRoleAcceptsAnyExistingRoleOrDeny(t *testing.T) {
	srv, store := newRoleRulesConfigServer(t, nil)
	seedRole(t, store, "rr-fallback", "servers:read")

	for _, def := range []string{"rr-fallback", "operator", "deny", ""} {
		status, body := putAuth(t, srv, `{"providers":[`+rrLocal+`,`+rrOIDC("authentik",
			`,"roleRules":[{"group":"ops","role":"operator"}],"defaultRole":"`+def+`"`)+`]}`)
		if status != http.StatusOK {
			t.Fatalf("defaultRole %q: %d %s", def, status, body)
		}
	}
}

func TestConfigRoleRules_SaveRefusesBlankRuleFields(t *testing.T) {
	srv, _ := newRoleRulesConfigServer(t, nil)

	for _, rule := range []string{`{"group":"  ","role":"viewer"}`, `{"group":"ops","role":" "}`} {
		status, body := putAuth(t, srv, `{"providers":[`+rrLocal+`,`+rrOIDC("authentik", `,"roleRules":[`+rule+`]`)+`]}`)
		if status == http.StatusOK {
			t.Fatalf("rule %s: saved, want refused; body=%s", rule, body)
		}
		if !strings.Contains(string(body), "must not be blank") {
			t.Fatalf("rule %s: body %s does not explain the blank field", rule, body)
		}
	}
}

// OD-3: the Helm override's rules and fallback are stored and returned as saved.
func TestConfigRoleRules_HelmOverrideRulesRoundTrip(t *testing.T) {
	helm := &auth.ProviderPolicy{Rules: []auth.RoleRule{{Group: "seed", Role: "admin"}}}
	srv, store := newRoleRulesConfigServer(t, helm)
	seedRole(t, store, "rr-ovr-role", "servers:read")

	status, body := putAuth(t, srv, `{"providers":[`+rrLocal+`],"helmOverride":{`+
		`"roleRules":[{"group":"ops","role":"operator"},{"group":"login","role":"rr-ovr-role"}],"defaultRole":"viewer"}}`)
	if status != http.StatusOK {
		t.Fatalf("save: %d %s", status, body)
	}
	var authSection struct {
		HelmOverride struct {
			RoleRules   []auth.RoleRule `json:"roleRules"`
			DefaultRole *string         `json:"defaultRole"`
		} `json:"helmOverride"`
	}
	if err := json.Unmarshal(getConfig(t, srv)["auth"], &authSection); err != nil {
		t.Fatalf("decode auth: %v", err)
	}
	wantRules := []auth.RoleRule{{Group: "ops", Role: "operator"}, {Group: "login", Role: "rr-ovr-role"}}
	if !reflect.DeepEqual(authSection.HelmOverride.RoleRules, wantRules) {
		t.Fatalf("helmOverride.roleRules = %#v, want %#v", authSection.HelmOverride.RoleRules, wantRules)
	}
	if d := authSection.HelmOverride.DefaultRole; d == nil || *d != "viewer" {
		t.Fatalf("helmOverride.defaultRole = %v, want viewer", d)
	}

	status, body = putAuth(t, srv, `{"providers":[`+rrLocal+`],"helmOverride":{"roleRules":[{"group":"ops","role":"rr-ghost-ovr"}]}}`)
	wantRefused(t, status, body, "server.config.auth_role_unknown", "rr-ghost-ovr")
	status, body = putAuth(t, srv, `{"providers":[`+rrLocal+`],"helmOverride":{"defaultRole":"rr-ghost-ovr-def"}}`)
	wantRefused(t, status, body, "server.config.auth_role_unknown", "rr-ghost-ovr-def")
}

// Resetting one old per-role override keeps the override's rules and fallback.
func TestConfigRoleRules_ResetRoleMappingKeepsOverrideFallback(t *testing.T) {
	srv, store := newRoleRulesConfigServer(t, &auth.ProviderPolicy{})
	putAuthRow(t, store, `{"providers":[`+rrLocal+`],"helmOverride":{"roleMappings":{"admin":["a"]},"defaultRole":"deny"}}`)

	status, body := doReq(t, http.MethodDelete, srv.URL+"/admin/config/auth/role-mappings/admin", nil)
	if status != http.StatusOK {
		t.Fatalf("reset: %d %s", status, body)
	}
	raw := storedAuth(t, store)
	if !strings.Contains(raw, `"helmOverride":{"defaultRole":"deny"}`) {
		t.Fatalf("reset dropped the override fallback: %s", raw)
	}
}

// installTimeSettings.oidcHelmProvider.roleRules is the effective Helm seed
// as one ordered list, from the new flag or the old per-role flags.
func TestConfigRoleRules_InstallTimeSettingsCarryRoleRules(t *testing.T) {
	tests := []struct {
		name string
		helm *auth.ProviderPolicy
		want []auth.RoleRule
	}{
		{
			name: "from --oidc-role-rules",
			helm: &auth.ProviderPolicy{Rules: []auth.RoleRule{{Group: "login only", Role: "login-only"}, {Group: "adm", Role: "admin"}}},
			want: []auth.RoleRule{{Group: "login only", Role: "login-only"}, {Group: "adm", Role: "admin"}},
		},
		{
			name: "from the old per-role flags",
			helm: &auth.ProviderPolicy{RoleMappings: &auth.RoleMappings{Viewer: []string{"v"}, Admin: []string{"a"}}},
			want: []auth.RoleRule{{Group: "a", Role: "admin"}, {Group: "v", Role: "viewer"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := newRoleRulesConfigServer(t, tt.helm)
			var its struct {
				OIDCHelmProvider struct {
					RoleRules []auth.RoleRule `json:"roleRules"`
				} `json:"oidcHelmProvider"`
			}
			if err := json.Unmarshal(getConfig(t, srv)["installTimeSettings"], &its); err != nil {
				t.Fatalf("decode installTimeSettings: %v", err)
			}
			if !reflect.DeepEqual(its.OIDCHelmProvider.RoleRules, tt.want) {
				t.Fatalf("roleRules = %#v, want %#v", its.OIDCHelmProvider.RoleRules, tt.want)
			}
		})
	}
}

// FR-019 (T038): a provider stored with only the old roleMappings still
// loads, and GET returns it unchanged until it is saved again.
func TestConfigRoleRules_OldFormProviderLoadsUnchanged(t *testing.T) {
	srv, store := newRoleRulesConfigServer(t, nil)
	const old = `{"providers":[{"name":"local","kind":"local","enabled":true},` +
		`{"name":"corp","kind":"oidc","enabled":true,"issuer":"https://idp.example","clientID":"gameplane",` +
		`"roleMappings":{"admin":["corp-admins"],"viewer":["corp-view"]},"defaultRole":"deny"}],` +
		`"helmOverride":{"roleMappings":{"operator":["ops"]}}}`
	putAuthRow(t, store, old)

	var want, got any
	if err := json.Unmarshal([]byte(old), &want); err != nil {
		t.Fatalf("decode seed: %v", err)
	}
	if err := json.Unmarshal(getConfig(t, srv)["auth"], &got); err != nil {
		t.Fatalf("decode auth: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GET auth = %v, want the stored row unchanged %v", got, want)
	}

	reg := auth.NewRegistry(store, nil, nil, "", nil)
	var corp *auth.Provider
	for _, p := range reg.Enabled(t.Context()) {
		if p.Name == "corp" {
			corp = &p
		}
	}
	if corp == nil {
		t.Fatal("old-form provider corp is not loaded")
	}
	if corp.RoleMappings == nil || !reflect.DeepEqual(corp.RoleMappings.Admin, []string{"corp-admins"}) || corp.RoleRules != nil {
		t.Fatalf("corp loaded as %+v, want the old roleMappings and no roleRules", corp)
	}
	if m := reg.HelmRoleOverrides(t.Context()); m == nil || !reflect.DeepEqual(m.Operator, []string{"ops"}) {
		t.Fatalf("old helmOverride.roleMappings = %+v, want operator [ops]", m)
	}
}
