package handlers

import (
	"context"
	"errors"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/httperr"
)

// Unit tests for validateSecurityOverride (T012 cases 1 to 3, T013).
// No cluster is involved: the function is called directly with a context
// that carries the test user, and the objects are plain unstructured maps.

// secOverrideObj builds a GameServer-shaped object whose spec.security is
// security. A nil security leaves the field absent.
func secOverrideObj(security map[string]any) *unstructured.Unstructured {
	spec := map[string]any{"templateRef": map[string]any{"name": "minecraft"}}
	if security != nil {
		spec["security"] = security
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": "sec-unit", "namespace": "gameplane"},
		"spec":       spec,
	}}
}

// secOverrideCtx returns a context carrying u (nil means no user).
func secOverrideCtx(u *auth.User) context.Context {
	ctx := context.Background()
	if u == nil {
		return ctx
	}
	return auth.WithUser(ctx, u)
}

// secOverrideWriter holds servers:write only.
func secOverrideWriter() *auth.User {
	return &auth.User{
		ID:       201,
		Username: "sec-writer",
		Role:     "operator",
		Perms: map[string]map[string]map[string]struct{}{
			"*": {"*": {"servers:write": {}}},
		},
	}
}

// secOverrideSecurityHolder holds servers:write and servers:security.
func secOverrideSecurityHolder() *auth.User {
	return &auth.User{
		ID:       202,
		Username: "sec-holder",
		Role:     "operator",
		Perms: map[string]map[string]map[string]struct{}{
			"*": {"*": {"servers:write": {}, "servers:security": {}}},
		},
	}
}

// secOverrideAdmin is the built-in admin wildcard.
func secOverrideAdmin() *auth.User {
	return &auth.User{
		ID:       203,
		Username: "sec-admin",
		Role:     "admin",
		Perms: map[string]map[string]map[string]struct{}{
			"*": {"*": {"*": {}}},
		},
	}
}

// secOverrideStored is a non-root override as stored by the dynamic
// client (int64 numbers).
func secOverrideStored() map[string]any {
	return map[string]any{
		"runAsUser":            map[string]any{"value": int64(2000)},
		"volumeOwnershipCheck": "EveryStart",
	}
}

// secOverrideRequested is the same override as a request body decodes it
// (float64 numbers).
func secOverrideRequested() map[string]any {
	return map[string]any{
		"runAsUser":            map[string]any{"value": float64(2000)},
		"volumeOwnershipCheck": "EveryStart",
	}
}

func TestSecurityOverride_Case1_UnchangedAcceptedWithoutPermission(t *testing.T) {
	ctx := secOverrideCtx(secOverrideWriter())

	t.Run("both absent", func(t *testing.T) {
		desired := secOverrideObj(nil)
		live := secOverrideObj(nil)
		if err := validateSecurityOverride(ctx, "", "gameplane", desired, live); err != nil {
			t.Fatalf("both absent: unexpected error %v", err)
		}
	})

	t.Run("equal with int64 live and float64 request", func(t *testing.T) {
		desired := secOverrideObj(secOverrideRequested())
		live := secOverrideObj(secOverrideStored())
		if err := validateSecurityOverride(ctx, "", "gameplane", desired, live); err != nil {
			t.Fatalf("equal values: unexpected error %v", err)
		}
		// The request value must not have been rewritten.
		got, _, _ := unstructured.NestedFieldNoCopy(desired.Object, "spec", "security", "runAsUser", "value")
		if got != float64(2000) {
			t.Errorf("request value rewritten: got %#v", got)
		}
	})

	t.Run("numeric equality is not a change", func(t *testing.T) {
		if !securityValuesEqual(map[string]any{"value": float64(2000)}, map[string]any{"value": int64(2000)}) {
			t.Error("float64 2000 and int64 2000 compared unequal")
		}
		if securityValuesEqual(map[string]any{"value": float64(2001)}, map[string]any{"value": int64(2000)}) {
			t.Error("float64 2001 and int64 2000 compared equal")
		}
	})
}

func TestSecurityOverride_Case2_AbsentInRequestIsRestored(t *testing.T) {
	ctx := secOverrideCtx(secOverrideWriter())
	desired := secOverrideObj(nil)
	live := secOverrideObj(secOverrideStored())

	if err := validateSecurityOverride(ctx, "", "gameplane", desired, live); err != nil {
		t.Fatalf("restore: unexpected error %v", err)
	}
	got, found, err := unstructured.NestedMap(desired.Object, "spec", "security")
	if err != nil {
		t.Fatalf("read restored spec.security: %v", err)
	}
	if !found {
		t.Fatal("spec.security not restored into the request")
	}
	if got["volumeOwnershipCheck"] != "EveryStart" {
		t.Errorf("restored volumeOwnershipCheck = %#v, want EveryStart", got["volumeOwnershipCheck"])
	}
	if !securityValuesEqual(got, secOverrideStored()) {
		t.Errorf("restored value %#v differs from stored %#v", got, secOverrideStored())
	}
}

func TestSecurityOverride_Case2_HolderMayRemove(t *testing.T) {
	ctx := secOverrideCtx(secOverrideSecurityHolder())
	desired := secOverrideObj(nil)
	live := secOverrideObj(secOverrideStored())

	if err := validateSecurityOverride(ctx, "", "gameplane", desired, live); err != nil {
		t.Fatalf("holder removing override: unexpected error %v", err)
	}
	if _, found, _ := unstructured.NestedFieldNoCopy(desired.Object, "spec", "security"); found {
		t.Error("holder removal was overwritten by the restore step")
	}
}

func TestSecurityOverride_Case3_ForbiddenWithoutPermission(t *testing.T) {
	stored := secOverrideStored()
	changed := map[string]any{"runAsUser": map[string]any{"value": float64(3000)}}

	cases := []struct {
		name    string
		user    *auth.User
		desired *unstructured.Unstructured
		live    *unstructured.Unstructured
	}{
		{"create with override", secOverrideWriter(), secOverrideObj(secOverrideRequested()), nil},
		{"update changed override", secOverrideWriter(), secOverrideObj(changed), secOverrideObj(stored)},
		{"clone with override (live nil)", secOverrideWriter(), secOverrideObj(secOverrideRequested()), nil},
		{"no user at all", nil, secOverrideObj(secOverrideRequested()), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateSecurityOverride(secOverrideCtx(tc.user), "", "gameplane", tc.desired, tc.live)
			if err == nil {
				t.Fatal("expected server.security.forbidden, got nil")
			}
			var msg *httperr.Message
			if !errors.As(err, &msg) {
				t.Fatalf("error %T is not *httperr.Message: %v", err, err)
			}
			if msg.ID != "server.security.forbidden" {
				t.Errorf("message id = %q, want server.security.forbidden", msg.ID)
			}
			if msg.Status != 403 {
				t.Errorf("status = %d, want 403", msg.Status)
			}
		})
	}
}

func TestSecurityOverride_Case3_AdminAndHolderAllowed(t *testing.T) {
	stored := secOverrideStored()
	changed := map[string]any{"runAsUser": map[string]any{"value": float64(3000)}}

	for _, u := range []*auth.User{secOverrideAdmin(), secOverrideSecurityHolder()} {
		ctx := secOverrideCtx(u)
		if err := validateSecurityOverride(ctx, "", "gameplane", secOverrideObj(changed), secOverrideObj(stored)); err != nil {
			t.Errorf("user %s update: unexpected error %v", u.Username, err)
		}
		if err := validateSecurityOverride(ctx, "", "gameplane", secOverrideObj(secOverrideRequested()), nil); err != nil {
			t.Errorf("user %s create: unexpected error %v", u.Username, err)
		}
	}
}
