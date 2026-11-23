package handlers

import (
	"errors"
	"testing"

	"github.com/GameplanePanel/gameplane/api/internal/httperr"
)

// T028 unit tests for the shape step (case 4) of validateSecurityOverride
// (contracts/api-server-security.md). Like security_overrides_test.go, no
// cluster is involved: the function is called directly with an admin context
// and plain unstructured maps. Requests decode numbers as float64.

// shapeInvalidCase is one request block that must be refused with 422.
type shapeInvalidCase struct {
	name    string
	block   map[string]any
	setting string
}

func shapeInvalidCases() []shapeInvalidCase {
	return []shapeInvalidCase{
		{
			name:    "value above range",
			block:   map[string]any{"fsGroup": map[string]any{"value": float64(4294967296)}},
			setting: "fsGroup",
		},
		{
			name:    "negative value",
			block:   map[string]any{"runAsUser": map[string]any{"value": float64(-1)}},
			setting: "runAsUser",
		},
		{
			name:    "non-integer value",
			block:   map[string]any{"runAsGroup": map[string]any{"value": float64(1.5)}},
			setting: "runAsGroup",
		},
		{
			name:    "string value",
			block:   map[string]any{"runAsUser": map[string]any{"value": "2000"}},
			setting: "runAsUser",
		},
		{
			name: "both value and useImageDefault",
			block: map[string]any{"runAsUser": map[string]any{
				"value":           float64(2000),
				"useImageDefault": true,
			}},
			setting: "runAsUser",
		},
		{
			name:    "neither value nor useImageDefault",
			block:   map[string]any{"runAsGroup": map[string]any{}},
			setting: "runAsGroup",
		},
		{
			name:    "useImageDefault false",
			block:   map[string]any{"fsGroup": map[string]any{"useImageDefault": false}},
			setting: "fsGroup",
		},
		{
			name: "unknown member inside an id",
			block: map[string]any{"runAsUser": map[string]any{
				"value": float64(2000),
				"extra": true,
			}},
			setting: "runAsUser",
		},
		{
			name:    "unknown top-level member",
			block:   map[string]any{"bogus": map[string]any{"value": float64(1)}},
			setting: "bogus",
		},
		{
			name:    "bad volumeOwnershipCheck enum",
			block:   map[string]any{"volumeOwnershipCheck": "Sometimes"},
			setting: "volumeOwnershipCheck",
		},
	}
}

// TestSecurityOverrideShape_InvalidRefusedWith422 checks that each bad shape
// is refused with server.security.invalid, status 422 and the offending
// setting in args, for an admin (so case 3 cannot hide the shape error).
func TestSecurityOverrideShape_InvalidRefusedWith422(t *testing.T) {
	for _, tc := range shapeInvalidCases() {
		t.Run(tc.name, func(t *testing.T) {
			desired := secOverrideObj(tc.block)
			err := validateSecurityOverride(secOverrideCtx(secOverrideAdmin()), "cl", "gameplane", desired, nil)
			if err == nil {
				t.Fatalf("shape %v accepted, want refusal", tc.block)
			}
			var m *httperr.Message
			if !errors.As(err, &m) {
				t.Fatalf("error %T is not an httperr.Message: %v", err, err)
			}
			if m.ID != "server.security.invalid" {
				t.Errorf("ID = %q, want server.security.invalid", m.ID)
			}
			if m.Status != 422 {
				t.Errorf("Status = %d, want 422", m.Status)
			}
			if got := m.Args["setting"]; got != tc.setting {
				t.Errorf("args.setting = %v, want %q", got, tc.setting)
			}
			if detail, ok := m.Args["detail"].(string); !ok || detail == "" {
				t.Errorf("args.detail = %v, want a non-empty string", m.Args["detail"])
			}
		})
	}
}

// TestSecurityOverrideShape_ValidAccepted checks shapes that must pass,
// including the int64 numbers the dynamic client returns and the range bounds.
func TestSecurityOverrideShape_ValidAccepted(t *testing.T) {
	valid := map[string]map[string]any{
		"value 2000": {"runAsUser": map[string]any{"value": float64(2000)}},
		"value 0 is root but a valid shape": {
			"runAsUser": map[string]any{"value": float64(0)},
		},
		"upper bound": {"fsGroup": map[string]any{"value": float64(4294967295)}},
		"int64 value": {"runAsGroup": map[string]any{"value": int64(1000)}},
		"use image default": {
			"runAsGroup": map[string]any{"useImageDefault": true},
		},
		"ownership check": {
			"runAsUser":            map[string]any{"value": float64(2000)},
			"volumeOwnershipCheck": "OnlyWhenWrong",
		},
		"null id counts as absent": {
			"fsGroup": nil,
		},
	}
	for name, block := range valid {
		t.Run(name, func(t *testing.T) {
			desired := secOverrideObj(block)
			if err := validateSecurityOverride(secOverrideCtx(secOverrideAdmin()), "cl", "gameplane", desired, nil); err != nil {
				t.Fatalf("shape %v refused: %v", block, err)
			}
		})
	}
}

// TestSecurityOverrideShape_ForbiddenBeforeShape checks the order: a caller
// without servers:security gets 403 for a bad shape, not 422.
func TestSecurityOverrideShape_ForbiddenBeforeShape(t *testing.T) {
	desired := secOverrideObj(map[string]any{"runAsUser": map[string]any{"value": float64(-1)}})
	err := validateSecurityOverride(secOverrideCtx(secOverrideWriter()), "cl", "gameplane", desired, nil)
	var m *httperr.Message
	if !errors.As(err, &m) || m.ID != "server.security.forbidden" {
		t.Fatalf("error = %v, want server.security.forbidden", err)
	}
}

// TestSecurityOverrideShape_UnchangedNotChecked checks that an unchanged stored
// block is accepted without a shape check (case 1 runs first).
func TestSecurityOverrideShape_UnchangedNotChecked(t *testing.T) {
	block := map[string]any{"runAsUser": map[string]any{"value": float64(-1)}}
	stored := map[string]any{"runAsUser": map[string]any{"value": int64(-1)}}
	desired := secOverrideObj(block)
	live := secOverrideObj(stored)
	if err := validateSecurityOverride(secOverrideCtx(secOverrideAdmin()), "cl", "gameplane", desired, live); err != nil {
		t.Fatalf("unchanged block refused: %v", err)
	}
}
