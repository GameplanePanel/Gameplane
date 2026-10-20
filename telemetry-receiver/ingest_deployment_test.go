package main

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/telemetryschema"
)

// TestIngestSchema2WritesDeploymentAndOneRowPerSetValue checks that a schema 2
// report writes a deployment row and one daily_dim row per value of each set.
func TestIngestSchema2WritesDeploymentAndOneRowPerSetValue(t *testing.T) {
	s, _ := newExtServer(t, config{}, extStart)
	body, sig := newExtInstall(1).report(t, extStart, func(r *telemetryschema.Report) {
		r.Ext.Env = telemetryschema.Env{
			K8s: []string{"1.30", "1.31"}, Distro: []string{"eks", "k3s"}, Arch: []string{"amd64", "arm64"},
			Nodes: "4-10", Deployment: "standalone",
		}
	})
	if w := extSend(t, s, body, sig); w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (%s)", w.Code, w.Body)
	}
	const day = "2026-10-07"
	for _, c := range []struct{ dim, value string }{
		{"deployment", "standalone"},
		{"k8s", "1.30"},
		{"k8s", "1.31"},
		{"distro", "eks"},
		{"distro", "k3s"},
		{"nodes", "4-10"},
	} {
		n := countRows(t.Context(), t, s.store,
			`SELECT installs FROM daily_dim WHERE day = ? AND dim = ? AND value = ?`, day, c.dim, c.value)
		if n != 1 {
			t.Errorf("daily_dim (%s, %s) installs = %d, want 1", c.dim, c.value, n)
		}
	}
	if n := countRows(t.Context(), t, s.store,
		`SELECT count(*) FROM daily_dim WHERE day = ? AND dim = 'k8s'`, day); n != 2 {
		t.Errorf("k8s rows = %d, want 2 (one per value)", n)
	}
}

// TestIngestSchema1CountsDeploymentAsOther checks that a schema 1 report,
// which carries no deployment, is counted under deployment "other".
func TestIngestSchema1CountsDeploymentAsOther(t *testing.T) {
	s, _ := newExtServer(t, config{}, extStart)
	body, sig := newExtInstall(1).report(t, extStart, func(r *telemetryschema.Report) {
		r.Ext.Schema = 1
		r.Ext.Env = telemetryschema.Env{K8s: []string{"1.29"}, Distro: []string{"kind"}, Arch: []string{"amd64"}, Nodes: "1"}
	})
	if w := extSend(t, s, body, sig); w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (%s)", w.Code, w.Body)
	}
	const day = "2026-10-07"
	for _, c := range []struct{ dim, value string }{
		{"deployment", "other"},
		{"k8s", "1.29"},
		{"distro", "kind"},
	} {
		n := countRows(t.Context(), t, s.store,
			`SELECT installs FROM daily_dim WHERE day = ? AND dim = ? AND value = ?`, day, c.dim, c.value)
		if n != 1 {
			t.Errorf("daily_dim (%s, %s) installs = %d, want 1", c.dim, c.value, n)
		}
	}
}

// TestBuildViewsDeploymentShares checks that the deployment share is split
// between the two installs that reported it.
func TestBuildViewsDeploymentShares(t *testing.T) {
	s, clock := newExtServer(t, config{activityExpiryDays: 31}, extStart)
	a, aSig := newExtInstall(1).report(t, extStart, func(r *telemetryschema.Report) {
		r.Ext.Env.Deployment = "standalone"
	})
	b, bSig := newExtInstall(2).report(t, extStart, func(r *telemetryschema.Report) {
		r.Ext.Env.Deployment = "cluster"
	})
	for _, c := range []struct{ body, sig string }{{a, aSig}, {b, bSig}} {
		if w := extSend(t, s, c.body, c.sig); w.Code != http.StatusNoContent {
			t.Fatalf("status = %d (%s)", w.Code, w.Body)
		}
	}
	*clock = extStart.Add(24 * time.Hour)
	v, err := BuildViews(t.Context(), s.store, 7, *clock)
	if err != nil {
		t.Fatal(err)
	}
	if v.Extended == nil {
		t.Fatal("Extended is nil")
	}
	want := []ShareRow{{"cluster", 0.5}, {"standalone", 0.5}}
	if got := fmt.Sprint(v.Extended.Env.Deployment); got != fmt.Sprint(want) {
		t.Fatalf("deployment = %v, want %v", v.Extended.Env.Deployment, want)
	}
}
