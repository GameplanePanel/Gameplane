package auth

import (
	"reflect"
	"testing"
)

func TestRulesFromMappings(t *testing.T) {
	tests := []struct {
		name string
		in   *RoleMappings
		want []RoleRule
	}{
		{name: "nil", in: nil, want: nil},
		{
			name: "all roles in admin, operator, viewer order",
			in:   &RoleMappings{Admin: []string{"a1", "a2"}, Operator: []string{"o1"}, Viewer: []string{"v1"}},
			want: []RoleRule{
				{Group: "a1", Role: "admin"},
				{Group: "a2", Role: "admin"},
				{Group: "o1", Role: "operator"},
				{Group: "v1", Role: "viewer"},
			},
		},
		{name: "empty struct", in: &RoleMappings{}, want: []RoleRule{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rulesFromMappings(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("rulesFromMappings() = %#v, want %#v", got, tt.want)
			}
			if tt.in != nil && got == nil {
				t.Fatalf("rulesFromMappings() returned nil for non-nil input")
			}
		})
	}
}

func TestDecideRole(t *testing.T) {
	ruleList := []RoleRule{{Group: "sys-admin", Role: "admin"}, {Group: "login", Role: "login-only"}}
	reversed := []RoleRule{{Group: "login", Role: "login-only"}, {Group: "sys-admin", Role: "admin"}}

	tests := []struct {
		name   string
		groups []string
		pol    *ProviderPolicy
		want   Decision
	}{
		{
			name:   "nil policy is viewer",
			groups: []string{"anything"},
			pol:    nil,
			want:   Decision{RuleIndex: -1, Role: "viewer"},
		},
		{
			name:   "mapping off ignores deny fallback",
			groups: []string{"anything"},
			pol:    &ProviderPolicy{DefaultRole: "deny"},
			want:   Decision{RuleIndex: -1, Role: "viewer"},
		},
		{
			name:   "rule order wins over group order",
			groups: []string{"login", "sys-admin"},
			pol:    &ProviderPolicy{Rules: ruleList},
			want:   Decision{RuleIndex: 0, Group: "sys-admin", Role: "admin"},
		},
		{
			name:   "reversed rules: first rule wins even when less privileged",
			groups: []string{"login", "sys-admin"},
			pol:    &ProviderPolicy{Rules: reversed},
			want:   Decision{RuleIndex: 0, Group: "login", Role: "login-only"},
		},
		{
			name:   "custom role target",
			groups: []string{"g1"},
			pol:    &ProviderPolicy{Rules: []RoleRule{{Group: "g1", Role: "login-only"}}},
			want:   Decision{RuleIndex: 0, Group: "g1", Role: "login-only"},
		},
		{
			name:   "no match, default empty is viewer",
			groups: []string{"other"},
			pol:    &ProviderPolicy{Rules: ruleList, DefaultRole: ""},
			want:   Decision{RuleIndex: -1, Role: "viewer"},
		},
		{
			name:   "no match, custom default role",
			groups: []string{"other"},
			pol:    &ProviderPolicy{Rules: ruleList, DefaultRole: "login-only"},
			want:   Decision{RuleIndex: -1, Role: "login-only"},
		},
		{
			name:   "no match, deny refuses",
			groups: []string{"other"},
			pol:    &ProviderPolicy{Rules: ruleList, DefaultRole: "deny"},
			want:   Decision{RuleIndex: -1, Deny: true},
		},
		{
			name:   "empty non-nil rules keep mapping on, so deny applies",
			groups: []string{"anything"},
			pol:    &ProviderPolicy{Rules: []RoleRule{}, DefaultRole: "deny"},
			want:   Decision{RuleIndex: -1, Deny: true},
		},
		{
			name:   "rules take precedence over role mappings",
			groups: []string{"a"},
			pol: &ProviderPolicy{
				Rules:        []RoleRule{{Group: "g1", Role: "login-only"}},
				RoleMappings: &RoleMappings{Admin: []string{"a"}},
			},
			want: Decision{RuleIndex: -1, Role: "viewer"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decideRole(tt.groups, tt.pol)
			if got != tt.want {
				t.Fatalf("decideRole() = %#v, want %#v", got, tt.want)
			}
			wantGroup := tt.want.Group
			if tt.want.RuleIndex < 0 {
				wantGroup = "none"
			}
			if g := got.MatchedGroup(); g != wantGroup {
				t.Fatalf("MatchedGroup() = %q, want %q", g, wantGroup)
			}
		})
	}
}

// legacyComputeRole reproduces the pre-spec-034 computeRole logic exactly,
// so the rule engine can be checked against the old admin > operator >
// viewer behavior for every group combination.
func legacyComputeRole(groups []string, m *RoleMappings, def string) (string, bool) {
	if m == nil {
		return "viewer", false
	}
	member := map[string]bool{}
	for _, g := range groups {
		member[g] = true
	}
	matches := func(mapped []string) bool {
		for _, g := range mapped {
			if member[g] {
				return true
			}
		}
		return false
	}
	switch {
	case matches(m.Admin):
		return "admin", false
	case matches(m.Operator):
		return "operator", false
	case matches(m.Viewer):
		return "viewer", false
	}
	switch def {
	case "deny":
		return "", true
	case "admin", "operator":
		return def, false
	default:
		return "viewer", false
	}
}

func TestDecideRole_OldFormMatchesOldPrecedence(t *testing.T) {
	m := &RoleMappings{Admin: []string{"a"}, Operator: []string{"o"}, Viewer: []string{"v"}}
	universe := []string{"a", "o", "v"}
	defaults := []string{"", "viewer", "operator", "admin", "deny"}

	for mask := 0; mask < 1<<len(universe); mask++ {
		var groups []string
		for i, g := range universe {
			if mask&(1<<i) != 0 {
				groups = append(groups, g)
			}
		}
		for _, def := range defaults {
			wantRole, wantDeny := legacyComputeRole(groups, m, def)
			got := decideRole(groups, &ProviderPolicy{RoleMappings: m, DefaultRole: def})
			if got.Role != wantRole || got.Deny != wantDeny {
				t.Errorf("groups=%v default=%q: decideRole() = (%q, %v), legacy = (%q, %v)",
					groups, def, got.Role, got.Deny, wantRole, wantDeny)
			}
		}
	}
}

func TestProviderPolicyMappingOn(t *testing.T) {
	tests := []struct {
		name string
		pol  *ProviderPolicy
		want bool
	}{
		{name: "nil", pol: nil, want: false},
		{name: "empty policy", pol: &ProviderPolicy{}, want: false},
		{name: "role mappings set", pol: &ProviderPolicy{RoleMappings: &RoleMappings{}}, want: true},
		{name: "empty non-nil rules", pol: &ProviderPolicy{Rules: []RoleRule{}}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.pol.MappingOn(); got != tt.want {
				t.Fatalf("MappingOn() = %v, want %v", got, tt.want)
			}
		})
	}
}
