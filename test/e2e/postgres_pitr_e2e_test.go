//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Runs last in the disposable PostgreSQL cluster. The original DB stays fenced.
func TestPanel_PostgresPointInTimeRecovery(t *testing.T) {
	ctx := t.Context()
	if envInstance.Context != "kind-gameplane-e2e" {
		t.Fatal("PITR requires the disposable PostgreSQL kind fixture")
	}
	fixture, err := envInstance.K8s.AppsV1().StatefulSets("gameplane-system").Get(ctx, "gameplane-postgres", metav1.GetOptions{})
	if err != nil || fixture.Labels["app.kubernetes.io/component"] != "e2e-fixture" {
		t.Fatalf("PostgreSQL fixture is unavailable: %v", err)
	}
	envInstance.BootstrapAdmin(t, adminUsername, adminPassword)
	admin := envInstance.APIClient(t, adminUsername, adminPassword)
	t.Cleanup(admin.Close)
	user, password, id := envInstance.CreateUser(t, admin, "viewer", "pitr-viewer")
	userID, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	game, gameUID := recoveryGame(t, "pitr")
	const ns = "gameplane-games"
	claimID := func() string {
		t.Helper()
		pvc, err := envInstance.K8s.CoreV1().PersistentVolumeClaims(ns).Get(ctx, game+"-data", metav1.GetOptions{})
		if err != nil || pvc.UID == "" {
			t.Fatalf("read game data claim: %v", err)
		}
		return string(pvc.UID)
	}
	keyID := func() string {
		t.Helper()
		key, err := envInstance.K8s.CoreV1().Secrets("gameplane-system").Get(ctx, "gameplane-agent-ca", metav1.GetOptions{})
		if err != nil || key.UID == "" {
			t.Fatalf("read native agent CA identity: %v", err)
		}
		return string(key.UID)
	}
	f := recoveryFixture{t: t}
	f.request(admin, http.MethodPost, "/servers/"+game+":transfer?namespace="+ns, map[string]any{"userId": userID}, http.StatusNoContent)
	beforePod, beforeClaim, beforeKey := recoveryGameIdentity(t, game), claimID(), keyID()
	beforeProgress := recoveryCounter(t, game)
	reader := envInstance.APIClient(t, user, password)
	t.Cleanup(reader.Close)
	f.request(reader, http.MethodGet, "/users/me", nil, http.StatusOK)
	f.request(reader, http.MethodGet, "/users", nil, http.StatusForbidden)

	script, err := findRepoRel("deploy/kind/recover-postgres-wal.sh")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "wal.json")
	out, err := exec.CommandContext(ctx, "bash", script, envInstance.Context, id, path).CombinedOutput()
	if err != nil {
		t.Fatalf("fenced database recovery: %v\n%s", err, out)
	}
	post := envInstance.APIClient(t, user, password)
	t.Cleanup(post.Close)
	stale := &APIClient{BaseURL: post.BaseURL, HTTP: reader.HTTP, CSRF: reader.CSRF}
	f.request(stale, http.MethodGet, "/users/me", nil, http.StatusUnauthorized)
	me := f.request(post, http.MethodGet, "/users/me", nil, http.StatusOK)
	if extractIntField(string(me), "id") != id {
		t.Fatal("restored account ID does not match the recorded owner")
	}
	f.waitServer(post, "/servers?namespace="+ns, gameUID)
	f.request(post, http.MethodGet, "/users", nil, http.StatusForbidden)
	restoredAdmin := envInstance.APIClient(t, adminUsername, adminPassword)
	t.Cleanup(restoredAdmin.Close)
	marker := f.request(restoredAdmin, http.MethodGet, "/servers/"+game+"/files/read?namespace="+ns+"&path=%2Frecovery-marker", nil, http.StatusOK)
	if string(marker) != "retained\n" {
		t.Fatal("agent-backed read returned a different save marker")
	}
	var audit struct {
		OK      bool
		Checked int
	}
	raw := f.request(restoredAdmin, http.MethodGet, "/admin/audit/verify", nil, http.StatusOK)
	if json.Unmarshal(raw, &audit) != nil || !audit.OK || audit.Checked == 0 {
		t.Fatal("restored audit chain failed verification")
	}
	gs, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).Get(ctx, game, metav1.GetOptions{})
	if err != nil || string(gs.GetUID()) != gameUID || gs.GetAnnotations()["gameplane.local/owner-id"] != id {
		t.Fatalf("restored owner does not match the live GameServer: %v", err)
	}
	if recoveryGameIdentity(t, game) != beforePod || claimID() != beforeClaim || keyID() != beforeKey || recoveryCounter(t, game) <= beforeProgress {
		t.Fatal("game process, save, claim, native CA identity or progress changed unexpectedly")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var metrics struct {
		LSN  string `json:"restore_target_lsn"`
		Age  int64  `json:"recovery_point_age_seconds"`
		Loss int64  `json:"loss_unix_seconds"`
	}
	if err := json.Unmarshal(b, &metrics); err != nil || metrics.Loss <= 0 || metrics.LSN == "" {
		t.Fatalf("invalid PITR report: %v", err)
	}
	t.Logf("PostgreSQL PITR passed: target LSN=%s, panel recovery point age=%ds, panel RTO=%ds, live game retained CR=%s PVC=%s Pod=%s and advanced its persistent counter", metrics.LSN, metrics.Age, time.Now().Unix()-metrics.Loss, gameUID, beforeClaim, beforePod.PodUID)
}
