package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"
)

// groupSubsets returns every subset of universe, including the empty one.
func groupSubsets(universe []string) [][]string {
	var out [][]string
	for mask := 0; mask < 1<<len(universe); mask++ {
		var groups []string
		for i, g := range universe {
			if mask&(1<<i) != 0 {
				groups = append(groups, g)
			}
		}
		out = append(out, groups)
	}
	return out
}

// FR-019 (T037): a Helm policy built from only the --oidc-role-mapping-*
// flags, and a dashboard provider stored with only roleMappings, decide the
// same role as the pre-spec-034 precedence table (legacyComputeRole, in
// oidc_rolerules_test.go) for every group combination and fallback.
func TestOldFormPoliciesDecideLikeBefore(t *testing.T) {
	forms := []*RoleMappings{
		{Admin: []string{"a"}, Operator: []string{"o", "o2"}, Viewer: []string{"v"}},
		{Operator: []string{"o"}},
		{Admin: []string{"a"}, Viewer: []string{"v"}},
		{},
	}
	universe := []string{"a", "o", "o2", "v", "x"}
	defaults := []string{"", "viewer", "operator", "admin", "deny"}

	for fi, m := range forms {
		for _, def := range defaults {
			// The Helm policy main.go builds from the old per-role flags,
			// through the sign-in path with no or an empty helmOverride.
			helmBase := &ProviderPolicy{RoleMappings: m, DefaultRole: def}
			helmPolicies := map[string]*ProviderPolicy{
				"helm, no override":    effectiveHelmOverridePolicy(helmBase, nil),
				"helm, empty override": effectiveHelmOverridePolicy(helmBase, &HelmOverride{}),
			}
			// A dashboard provider row with only the old keys, as the
			// registry reads and builds it.
			raw, err := json.Marshal(map[string]any{
				"name": "corp", "kind": "oidc", "enabled": true,
				"roleMappings": m, "defaultRole": def,
			})
			if err != nil {
				t.Fatalf("marshal provider: %v", err)
			}
			var p Provider
			if err := json.Unmarshal(raw, &p); err != nil {
				t.Fatalf("unmarshal provider: %v", err)
			}
			helmPolicies["dashboard provider"] = p.policy()

			for name, pol := range helmPolicies {
				for _, groups := range groupSubsets(universe) {
					wantRole, wantDeny := legacyComputeRole(groups, m, def)
					got := decideRole(groups, pol)
					if got.Role != wantRole || got.Deny != wantDeny {
						t.Errorf("form %d, %s, default %q, groups %v: decideRole = (%q, %v), before = (%q, %v)",
							fi, name, def, groups, got.Role, got.Deny, wantRole, wantDeny)
					}
				}
			}
		}
	}
}

func TestEffectiveHelmOverridePolicy_NothingToApplyReturnsBase(t *testing.T) {
	base := &ProviderPolicy{Rules: []RoleRule{{Group: "a", Role: "admin"}}, DefaultRole: "deny"}
	if got := effectiveHelmOverridePolicy(base, nil); got != base {
		t.Fatal("nil override must return base unchanged (same pointer)")
	}
	if got := effectiveHelmOverridePolicy(base, &HelmOverride{}); got != base {
		t.Fatal("empty override must return base unchanged (same pointer)")
	}
	if got := effectiveHelmOverridePolicy(nil, &HelmOverride{DefaultRole: ptrTo("viewer")}); got != nil {
		t.Fatalf("nil base must stay nil, got %+v", got)
	}
}

func ptrTo[T any](v T) *T { return &v }

// OD-3: override roleRules replace the whole Helm list, an empty list turns
// mapping off, and override defaultRole replaces the fallback.
func TestEffectiveHelmOverridePolicy_RulesAndDefaultRole(t *testing.T) {
	base := &ProviderPolicy{
		GroupsClaim:  "memberOf",
		RoleMappings: &RoleMappings{Admin: []string{"old-admins"}},
		Rules:        []RoleRule{{Group: "a", Role: "admin"}},
		DefaultRole:  "viewer",
	}
	tests := []struct {
		name    string
		ov      *HelmOverride
		groups  []string
		want    Decision
		mapping bool
	}{
		{
			name:    "override rules replace the Helm list",
			ov:      &HelmOverride{RoleRules: &[]RoleRule{{Group: "b", Role: "login-only"}}},
			groups:  []string{"a", "b"},
			want:    Decision{RuleIndex: 0, Group: "b", Role: "login-only"},
			mapping: true,
		},
		{
			name:    "a Helm group no longer matches",
			ov:      &HelmOverride{RoleRules: &[]RoleRule{{Group: "b", Role: "login-only"}}},
			groups:  []string{"a"},
			want:    Decision{RuleIndex: -1, Role: "viewer"},
			mapping: true,
		},
		{
			name:    "empty override rules turn mapping off",
			ov:      &HelmOverride{RoleRules: &[]RoleRule{}, DefaultRole: ptrTo("deny")},
			groups:  []string{"a"},
			want:    Decision{RuleIndex: -1, Role: "viewer"},
			mapping: false,
		},
		{
			name:    "override defaultRole replaces the fallback",
			ov:      &HelmOverride{DefaultRole: ptrTo("deny")},
			groups:  []string{"x"},
			want:    Decision{RuleIndex: -1, Deny: true},
			mapping: true,
		},
		{
			name:    "override defaultRole alone keeps the Helm rules",
			ov:      &HelmOverride{DefaultRole: ptrTo("login-only")},
			groups:  []string{"a"},
			want:    Decision{RuleIndex: 0, Group: "a", Role: "admin"},
			mapping: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pol := effectiveHelmOverridePolicy(base, tt.ov)
			if pol.MappingOn() != tt.mapping {
				t.Fatalf("MappingOn = %v, want %v", pol.MappingOn(), tt.mapping)
			}
			if got := decideRole(tt.groups, pol); got != tt.want {
				t.Fatalf("decideRole = %#v, want %#v", got, tt.want)
			}
			if pol.GroupsClaim != "memberOf" {
				t.Fatalf("GroupsClaim = %q, want the Helm value", pol.GroupsClaim)
			}
		})
	}
	if base.DefaultRole != "viewer" || len(base.Rules) != 1 || base.RoleMappings == nil {
		t.Fatalf("base policy was modified: %+v", base)
	}
}

// The old per-role override still merges per role exactly as
// effectiveHelmPolicy does on an old-form Helm seed.
func TestEffectiveHelmOverridePolicy_OldOverrideMatchesPerRoleMerge(t *testing.T) {
	base := &ProviderPolicy{
		RoleMappings: &RoleMappings{Admin: []string{"seed-a"}, Operator: []string{"seed-o"}, Viewer: []string{"seed-v"}},
		DefaultRole:  "deny",
	}
	overrides := []*RoleMappings{
		{Admin: []string{}},
		{Viewer: []string{"ov-v"}},
		{Admin: []string{"ov-a"}, Operator: []string{}},
	}
	universe := []string{"seed-a", "seed-o", "seed-v", "ov-a", "ov-v"}
	for i, ov := range overrides {
		want := effectiveHelmPolicy(base, ov)
		got := effectiveHelmOverridePolicy(base, &HelmOverride{RoleMappings: ov})
		for _, groups := range groupSubsets(universe) {
			if w, g := decideRole(groups, want), decideRole(groups, got); w != g {
				t.Errorf("override %d, groups %v: got %#v, per-role merge %#v", i, groups, g, w)
			}
		}
	}
}

// On a Helm seed from --oidc-role-rules, an old per-role override replaces
// the rules for the roles it sets and keeps the others.
func TestEffectiveHelmOverridePolicy_OldOverrideOnRuleSeed(t *testing.T) {
	base := &ProviderPolicy{Rules: []RoleRule{
		{Group: "a1", Role: "admin"},
		{Group: "o1", Role: "operator"},
		{Group: "c1", Role: "login-only"},
	}}
	got := effectiveHelmOverridePolicy(base, &HelmOverride{RoleMappings: &RoleMappings{Admin: []string{"x"}}})
	want := []RoleRule{
		{Group: "x", Role: "admin"},
		{Group: "o1", Role: "operator"},
		{Group: "c1", Role: "login-only"},
	}
	if !reflect.DeepEqual(got.EffectiveRules(), want) {
		t.Fatalf("EffectiveRules = %#v, want %#v", got.EffectiveRules(), want)
	}
}

func TestRegistry_HelmPolicyOverrideReadsAllForms(t *testing.T) {
	s := newAuthDB(t)
	reg := NewRegistry(s, staticSecrets(nil), nil, "", nil)
	if got := reg.HelmPolicyOverride(context.Background()); got != nil {
		t.Fatalf("no auth row: got %+v, want nil", got)
	}

	seedConfigRow(t, s, "auth", `{"providers":[],"helmOverride":{"roleRules":[{"group":"ops","role":"operator"}],"defaultRole":"deny"}}`)
	got := reg.HelmPolicyOverride(context.Background())
	if got == nil || got.RoleRules == nil || got.DefaultRole == nil {
		t.Fatalf("got %+v, want roleRules and defaultRole", got)
	}
	if want := []RoleRule{{Group: "ops", Role: "operator"}}; !reflect.DeepEqual(*got.RoleRules, want) {
		t.Fatalf("roleRules = %#v, want %#v", *got.RoleRules, want)
	}
	if *got.DefaultRole != "deny" {
		t.Fatalf("defaultRole = %q, want deny", *got.DefaultRole)
	}
	if reg.HelmRoleOverrides(context.Background()) != nil {
		t.Fatal("HelmRoleOverrides must be nil without roleMappings")
	}

	seedConfigRow(t, s, "auth", `{"providers":[],"helmOverride":{"roleMappings":{"admin":["x"]}}}`)
	if m := reg.HelmRoleOverrides(context.Background()); m == nil || !reflect.DeepEqual(m.Admin, []string{"x"}) {
		t.Fatalf("HelmRoleOverrides = %+v, want admin [x]", m)
	}
}

// A dashboard override's rules apply to the Helm provider at the next
// sign-in, and a role they name must exist.
func TestHandleCallback_HelmOverrideRulesApplyLive(t *testing.T) {
	idp := newFakeIDP(t, "client-1")
	idp.groups = []string{"gameplane login only"}
	base := &ProviderPolicy{Rules: []RoleRule{{Group: "gameplane login only", Role: "viewer"}}}
	o, err := NewOIDCWithPolicy(context.Background(), idp.issuer(), "client-1", "secret", "https://app/cb", base)
	if err != nil {
		t.Fatalf("NewOIDCWithPolicy: %v", err)
	}
	o.SetProviderName(HelmProviderName)
	var override *HelmOverride
	o.AttachHelmOverrideFunc(func(context.Context) *HelmOverride { return override })
	store := newAuthDB(t)
	o.AttachStore(store)
	sessions := NewSessionStore(store)
	if _, err := store.DB.ExecContext(context.Background(),
		`INSERT INTO roles(name, builtin, description) VALUES ('login-only', 0, '')`); err != nil {
		t.Fatalf("seed role: %v", err)
	}
	roleOf := func() string {
		t.Helper()
		var role string
		if err := store.DB.QueryRowContext(context.Background(),
			`SELECT role FROM users WHERE email = ?`, idp.email).Scan(&role); err != nil {
			t.Fatalf("read user: %v", err)
		}
		return role
	}
	login := func(n int) int {
		t.Helper()
		idp.nonce = fmt.Sprintf("nonce-ovr-rules-%d", n)
		return callbackViaIDP(t, o, sessions, idp.nonce).Code
	}

	if code := login(1); code != http.StatusFound {
		t.Fatalf("login 1: code=%d", code)
	}
	if got := roleOf(); got != "viewer" {
		t.Fatalf("login 1: role=%q, want viewer from the Helm rules", got)
	}

	override = &HelmOverride{RoleRules: &[]RoleRule{{Group: "gameplane login only", Role: "login-only"}}}
	if code := login(2); code != http.StatusFound {
		t.Fatalf("login 2: code=%d", code)
	}
	if got := roleOf(); got != "login-only" {
		t.Fatalf("login 2: role=%q, want login-only from the override", got)
	}

	override = &HelmOverride{RoleRules: &[]RoleRule{{Group: "gameplane login only", Role: "ghost"}}}
	if code := login(3); code != http.StatusForbidden {
		t.Fatalf("login 3: code=%d, want 403 for a missing override role", code)
	}
	if got := roleOf(); got != "login-only" {
		t.Fatalf("login 3: role=%q, want login-only unchanged", got)
	}
}
