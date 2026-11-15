package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/messages"
)

// canSetSecurityOverrides reports whether the caller may change spec.security
// of a GameServer in cluster cl, namespace ns. It follows the captures:manage
// precedent: admins and holders of servers:security. A nil user holds nothing.
func canSetSecurityOverrides(ctx context.Context, cl, ns string) bool {
	u := auth.UserFromContext(ctx)
	if u == nil {
		return false
	}
	return u.Role == "admin" || u.Can("*", false, cl, ns) || u.Can("servers:security", true, cl, ns)
}

// securityOverrideValue returns spec.security of obj. A JSON null counts as absent.
func securityOverrideValue(obj *unstructured.Unstructured) (any, bool) {
	if obj == nil {
		return nil, false
	}
	raw, found, _ := unstructured.NestedFieldNoCopy(obj.Object, "spec", "security")
	if !found || raw == nil {
		return nil, false
	}
	return raw, true
}

// securityValuesEqual compares two spec.security values by their JSON value.
// Request bodies decode integers as float64 while the dynamic client returns
// int64, so a plain reflect.DeepEqual would report an unchanged value as changed.
// If either side cannot be normalized, the values count as different.
func securityValuesEqual(a, b any) bool {
	na, errA := normalizeJSONValue(a)
	nb, errB := normalizeJSONValue(b)
	if errA != nil || errB != nil {
		return false
	}
	return reflect.DeepEqual(na, nb)
}

func normalizeJSONValue(v any) (any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encode spec.security: %w", err)
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode spec.security: %w", err)
	}
	return out, nil
}

// validateSecurityOverride is the field-level step for spec.security. It runs
// on create, update and clone (live == nil), from validateAndProtectGameServer.
//
// Cases, in order (contracts/api-server-security.md):
//  1. Unchanged, or absent on both sides: accepted without the permission.
//  2. Absent in the request, present in the stored object, no permission:
//     the stored value is copied back into the request.
//  3. Any other difference without servers:security: 403 server.security.forbidden.
//
// Shape validation (case 4) and the root rule (case 5) run after this step
// and are not implemented here yet.
func validateSecurityOverride(ctx context.Context, cl, ns string, desired, live *unstructured.Unstructured) error {
	desiredVal, hasDesired := securityOverrideValue(desired)
	liveVal, hasLive := securityOverrideValue(live)

	if !hasDesired && !hasLive {
		return nil
	}
	if hasDesired && hasLive && securityValuesEqual(desiredVal, liveVal) {
		return nil
	}

	canSet := canSetSecurityOverrides(ctx, cl, ns)
	if !hasDesired {
		if canSet {
			// Removing the override is a change the caller may make.
			return nil
		}
		// Case 2: a client that drops the field must not wipe the stored value.
		if err := unstructured.SetNestedField(desired.Object, liveVal, "spec", "security"); err != nil {
			return fmt.Errorf("restore spec.security: %w", err)
		}
		return nil
	}

	if !canSet {
		return messages.New("server.security.forbidden", nil)
	}
	return nil
}
