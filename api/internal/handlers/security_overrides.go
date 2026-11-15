package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"

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
//  4. A changed or new block with an invalid shape: 422 server.security.invalid
//     (validateSecurityShape).
//
// The root rule (case 5) is not implemented here yet.
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
	// Case 4: shape. Runs only for a changed or new block, after the permission
	// check (case 3), so a caller without servers:security gets 403 first.
	if err := validateSecurityShape(desiredVal); err != nil {
		return err
	}
	return nil
}

// securityIDMax is the largest UID or GID a spec.security id may carry.
const securityIDMax = 4294967295

// securityIDNames are the three whole-number settings of spec.security.
var securityIDNames = []string{"runAsUser", "runAsGroup", "fsGroup"}

// securityVolumeOwnershipChecks are the accepted volumeOwnershipCheck values.
var securityVolumeOwnershipChecks = map[string]bool{"EveryStart": true, "OnlyWhenWrong": true}

// validateSecurityShape is case 4 of contracts/api-server-security.md. raw is
// the request's spec.security. It checks, in a fixed order, the object form,
// unknown members, each id (exactly one of value or useImageDefault, a whole
// number 0 to 4294967295 for value, true for useImageDefault) and the enum of
// volumeOwnershipCheck. The first failure decides. The returned error is a
// server.security.invalid message (422) whose args name the setting.
func validateSecurityShape(raw any) error {
	block, ok := raw.(map[string]any)
	if !ok {
		return invalidSecurity("security", "must be an object")
	}
	for _, key := range sortedKeys(block) {
		switch key {
		case "runAsUser", "runAsGroup", "fsGroup", "volumeOwnershipCheck":
		default:
			return invalidSecurity(key, "unknown member")
		}
	}
	for _, name := range securityIDNames {
		v, present := block[name]
		if !present || v == nil {
			continue
		}
		if err := validateSecurityIDShape(name, v); err != nil {
			return err
		}
	}
	if v, present := block["volumeOwnershipCheck"]; present && v != nil {
		s, isString := v.(string)
		if !isString || !securityVolumeOwnershipChecks[s] {
			return invalidSecurity("volumeOwnershipCheck", "must be EveryStart or OnlyWhenWrong")
		}
	}
	return nil
}

// validateSecurityIDShape checks one of runAsUser, runAsGroup or fsGroup.
// A null member counts as absent, as it does for the CRD.
func validateSecurityIDShape(name string, v any) error {
	obj, ok := v.(map[string]any)
	if !ok {
		return invalidSecurity(name, "must be an object with value or useImageDefault")
	}
	for _, key := range sortedKeys(obj) {
		if key != "value" && key != "useImageDefault" {
			return invalidSecurity(name, "unknown member "+key)
		}
	}
	value, hasValue := obj["value"]
	hasValue = hasValue && value != nil
	useDefault, hasUseDefault := obj["useImageDefault"]
	hasUseDefault = hasUseDefault && useDefault != nil

	if hasValue == hasUseDefault {
		return invalidSecurity(name, "set exactly one of value or useImageDefault")
	}
	if hasUseDefault {
		if b, isBool := useDefault.(bool); !isBool || !b {
			return invalidSecurity(name, "useImageDefault must be true when set")
		}
		return nil
	}
	n, isNumber := securityNumber(value)
	if !isNumber || n != math.Trunc(n) || n < 0 || n > securityIDMax {
		return invalidSecurity(name, "must be a whole number between 0 and 4294967295")
	}
	return nil
}

// securityNumber reads a JSON number. Request bodies decode numbers as float64,
// the dynamic client returns int64; both are accepted.
func securityNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

// invalidSecurity builds the 422 server.security.invalid refusal.
func invalidSecurity(setting, detail string) error {
	return messages.New("server.security.invalid", map[string]any{"setting": setting, "detail": detail})
}

// sortedKeys returns the keys of m in order, so the first refusal is stable.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
