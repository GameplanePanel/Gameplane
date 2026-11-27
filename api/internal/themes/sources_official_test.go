package themes

import (
	"context"
	"encoding/json"
	"testing"
)

// TestUpdateOfficialSourceDisableWithDefaultPolicy checks FR-002: with the chart
// default (no verify policy), an admin can still disable the built-in official
// source, and a PUT that names a url, themes or verify policy does not change
// the chart-owned values.
func TestUpdateOfficialSourceDisableWithDefaultPolicy(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := h.svc.SeedOfficial(ctx, OfficialConfig{Enabled: true, Themes: []string{"slate"}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	before := officialRowOf(t, h)
	if before.Verify != nil {
		t.Fatalf("precondition: verify = %s, want none", *before.Verify)
	}

	disabled := false
	interval := 7200
	in := SourceSettings{
		Type:             "oci",
		Spec:             json.RawMessage(`{"oci":{"url":"evil.example/themes","themes":["other"]}}`),
		Verify:           json.RawMessage(`{"key":"-----BEGIN PUBLIC KEY-----\nx\n-----END PUBLIC KEY-----\n"}`),
		RefreshIntervalS: &interval,
		Enabled:          &disabled,
	}
	got, err := h.svc.UpdateSource(ctx, OfficialSourceName, in)
	if err != nil {
		t.Fatalf("disable official source: %v", err)
	}
	if got.Enabled {
		t.Fatal("official source still enabled after PUT enabled=false")
	}
	if got.RefreshIntervalS != interval {
		t.Fatalf("refresh interval = %d, want %d", got.RefreshIntervalS, interval)
	}
	sp := specOf(t, got)
	if sp.URL != OfficialSourceURL {
		t.Fatalf("url = %q, want the chart-owned %q", sp.URL, OfficialSourceURL)
	}
	if len(sp.Themes) != 1 || sp.Themes[0] != "slate" {
		t.Fatalf("themes = %v, want the chart-owned [slate]", sp.Themes)
	}
	if got.Verify != nil {
		t.Fatalf("verify = %s, want the chart-owned none", *got.Verify)
	}
}
