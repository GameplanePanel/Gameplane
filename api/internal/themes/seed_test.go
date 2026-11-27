package themes

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/GameplanePanel/gameplane/api/internal/db"
)

// seedTestKey stands in for the official signing public key. Seeding only stores
// the policy; it does not parse the key.
const seedTestKey = "-----BEGIN PUBLIC KEY-----\ntest-key-one\n-----END PUBLIC KEY-----\n"

// seedTestKeyRotated is a second key, used to check that a restart reconciles it.
const seedTestKeyRotated = "-----BEGIN PUBLIC KEY-----\ntest-key-two\n-----END PUBLIC KEY-----\n"

// officialRowOf reads the official row, failing the test when it is missing.
func officialRowOf(t *testing.T, h *harness) db.ThemeSource {
	t.Helper()
	src, err := h.store.GetThemeSource(context.Background(), OfficialSourceName)
	if err != nil {
		t.Fatalf("get official source: %v", err)
	}
	return src
}

// specOf decodes the OCI spec of a row.
func specOf(t *testing.T, src db.ThemeSource) ociSpec {
	t.Helper()
	var sp sourceSpec
	if err := json.Unmarshal([]byte(src.Spec), &sp); err != nil {
		t.Fatalf("decode spec: %v", err)
	}
	if sp.OCI == nil {
		t.Fatalf("spec has no oci part: %s", src.Spec)
	}
	return *sp.OCI
}

// verifyKeyOf returns the PEM key of a keyed verify policy, or "" when none is set.
func verifyKeyOf(t *testing.T, src db.ThemeSource) string {
	t.Helper()
	if src.Verify == nil {
		return ""
	}
	var vs verifySpec
	if err := json.Unmarshal([]byte(*src.Verify), &vs); err != nil {
		t.Fatalf("decode verify: %v", err)
	}
	if vs.Key == nil {
		t.Fatalf("verify has no key: %s", *src.Verify)
	}
	return *vs.Key
}

func TestSeedOfficialSkippedWhenDisabled(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := h.svc.SeedOfficial(ctx, OfficialConfig{Enabled: false, Themes: []string{"a"}}); err != nil {
		t.Fatalf("seed disabled: %v", err)
	}
	_, err := h.store.GetThemeSource(ctx, OfficialSourceName)
	if !errors.Is(err, db.ErrThemeNotFound) {
		t.Fatalf("disabled seed created a row, got err %v", err)
	}
}

func TestSeedOfficialCreatedWhenEnabled(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	cfg := OfficialConfig{
		Enabled:         true,
		Themes:          []string{"dark-amber", "slate"},
		VerifyEnabled:   true,
		CosignPublicKey: seedTestKey,
	}
	if err := h.svc.SeedOfficial(ctx, cfg); err != nil {
		t.Fatalf("seed: %v", err)
	}
	src := officialRowOf(t, h)
	if !src.Enabled || src.Type != "oci" {
		t.Fatalf("row enabled=%v type=%q, want enabled oci", src.Enabled, src.Type)
	}
	sp := specOf(t, src)
	if sp.URL != OfficialSourceURL {
		t.Fatalf("url = %q, want default %q", sp.URL, OfficialSourceURL)
	}
	if strings.Join(sp.Themes, ",") != "dark-amber,slate" {
		t.Fatalf("themes = %v", sp.Themes)
	}
	if got := verifyKeyOf(t, src); got != seedTestKey {
		t.Fatalf("verify key = %q, want the configured key", got)
	}
}

func TestSeedOfficialUnsignedWhenVerifyDisabled(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := h.svc.SeedOfficial(ctx, OfficialConfig{Enabled: true, URL: "registry.example/themes"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	src := officialRowOf(t, h)
	if src.Verify != nil {
		t.Fatalf("verify = %s, want no policy when verify is disabled", *src.Verify)
	}
	if got := specOf(t, src).URL; got != "registry.example/themes" {
		t.Fatalf("url = %q, want the configured url", got)
	}
}

func TestSeedOfficialRequiresKeyWhenVerifyEnabled(t *testing.T) {
	h := newHarness(t)
	err := h.svc.SeedOfficial(context.Background(), OfficialConfig{Enabled: true, VerifyEnabled: true})
	if err == nil {
		t.Fatal("verify enabled without a key was accepted")
	}
	if _, err := h.store.GetThemeSource(context.Background(), OfficialSourceName); !errors.Is(err, db.ErrThemeNotFound) {
		t.Fatalf("row created despite the config error: %v", err)
	}
}

func TestSeedOfficialReconcilesListAndKeyOnRestart(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	first := OfficialConfig{Enabled: true, Themes: []string{"slate"}, VerifyEnabled: true, CosignPublicKey: seedTestKey}
	if err := h.svc.SeedOfficial(ctx, first); err != nil {
		t.Fatalf("first start: %v", err)
	}

	// An upgrade ships a longer list and a rotated key; the next start applies both.
	second := OfficialConfig{Enabled: true, Themes: []string{"slate", "dark-amber"}, VerifyEnabled: true, CosignPublicKey: seedTestKeyRotated}
	if err := h.svc.SeedOfficial(ctx, second); err != nil {
		t.Fatalf("restart: %v", err)
	}
	src := officialRowOf(t, h)
	if strings.Join(specOf(t, src).Themes, ",") != "slate,dark-amber" {
		t.Fatalf("themes after restart = %v, want the new list", specOf(t, src).Themes)
	}
	if got := verifyKeyOf(t, src); got != seedTestKeyRotated {
		t.Fatalf("verify key after restart not reconciled")
	}

	// Removing verify from the chart removes the policy from the row.
	if err := h.svc.SeedOfficial(ctx, OfficialConfig{Enabled: true, Themes: []string{"slate"}}); err != nil {
		t.Fatalf("restart without verify: %v", err)
	}
	if src := officialRowOf(t, h); src.Verify != nil {
		t.Fatalf("verify still set after the chart turned it off: %s", *src.Verify)
	}
}

func TestSeedOfficialKeepsAdminEnabledFlagAndInterval(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	cfg := OfficialConfig{Enabled: true, Themes: []string{"slate"}}
	if err := h.svc.SeedOfficial(ctx, cfg); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// The admin turns the source off and sets a custom refresh interval.
	src := officialRowOf(t, h)
	src.Enabled = false
	src.RefreshIntervalS = 7200
	if err := h.store.UpdateThemeSource(ctx, src); err != nil {
		t.Fatalf("admin update: %v", err)
	}

	cfg.Themes = []string{"slate", "dark-amber"}
	if err := h.svc.SeedOfficial(ctx, cfg); err != nil {
		t.Fatalf("restart: %v", err)
	}
	got := officialRowOf(t, h)
	if got.Enabled {
		t.Fatal("restart re-enabled the source the admin disabled")
	}
	if got.RefreshIntervalS != 7200 {
		t.Fatalf("refresh interval = %d, want the admin's 7200", got.RefreshIntervalS)
	}
	if strings.Join(specOf(t, got).Themes, ",") != "slate,dark-amber" {
		t.Fatalf("list not reconciled while disabled: %v", specOf(t, got).Themes)
	}
}

func TestSeedOfficialDisabledConfigLeavesRowAlone(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := h.svc.SeedOfficial(ctx, OfficialConfig{Enabled: true, Themes: []string{"slate"}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	before := officialRowOf(t, h)
	if err := h.svc.SeedOfficial(ctx, OfficialConfig{Enabled: false, Themes: []string{"other"}}); err != nil {
		t.Fatalf("seed disabled: %v", err)
	}
	after := officialRowOf(t, h)
	if after.Spec != before.Spec {
		t.Fatalf("disabled config rewrote the row: %s", after.Spec)
	}
}

func TestSeedOfficialKeepsAdminSpecFields(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	cfg := OfficialConfig{Enabled: true, Themes: []string{"slate"}}
	if err := h.svc.SeedOfficial(ctx, cfg); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// The admin narrows the source with allow globs.
	src := officialRowOf(t, h)
	var sp sourceSpec
	if err := json.Unmarshal([]byte(src.Spec), &sp); err != nil {
		t.Fatalf("decode spec: %v", err)
	}
	sp.Allow = []string{"sl*"}
	sp.OCI.Insecure = true
	raw, err := json.Marshal(sp)
	if err != nil {
		t.Fatalf("encode spec: %v", err)
	}
	src.Spec = string(raw)
	if err := h.store.UpdateThemeSource(ctx, src); err != nil {
		t.Fatalf("admin update: %v", err)
	}

	cfg.Themes = []string{"slate", "dark-amber"}
	if err := h.svc.SeedOfficial(ctx, cfg); err != nil {
		t.Fatalf("restart: %v", err)
	}
	row := officialRowOf(t, h)
	var full sourceSpec
	if err := json.Unmarshal([]byte(row.Spec), &full); err != nil {
		t.Fatalf("decode spec: %v", err)
	}
	if strings.Join(full.Allow, ",") != "sl*" {
		t.Fatalf("allow globs = %v, want the admin's [sl*]", full.Allow)
	}
	got := specOf(t, row)
	if !got.Insecure {
		t.Fatal("restart cleared the admin's insecure flag")
	}
	if strings.Join(got.Themes, ",") != "slate,dark-amber" {
		t.Fatalf("list not reconciled: %v", got.Themes)
	}
}
