//go:build e2e

package e2e

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// TestMultiCluster_SFTPRemoteEndpoint covers spec 040's remote-cluster
// endpoint on the central panel: a registered remote cluster gets its own
// SFTP endpoint address, host key fingerprint and service tokens. It checks
// the admin contract only: PUT sets the endpoint, tokens are shown once and
// capped at two active, the listing never echoes a token, a revoked token
// disappears, and the panel's own cluster refuses a remote endpoint.
//
// Not covered here: the SFTP pod running on cluster B and logging in to this
// API across clusters. The dual-cluster job has no route from cluster B's pods
// to this cluster's API, so that path stays a manual check (quickstart §10).
//
// Budget: one e2e-admin login. The bucket is at its per-IP login ceiling, so
// the test runs non-parallel and waits for the limiter to refill afterwards.
func TestMultiCluster_SFTPRemoteEndpoint(t *testing.T) {
	_, clusterBName := ensureClusterB(t)

	envInstance.BootstrapAdmin(t, adminUsername, adminPassword)
	admin := envInstance.APIClient(t, adminUsername, adminPassword)
	defer admin.Close()
	lastLogin := time.Now()
	t.Cleanup(func() {
		if d := 12*time.Second - time.Since(lastLogin); d > 0 {
			time.Sleep(d)
		}
	})

	clusterID := fmt.Sprintf("e2e-sftp-mc-%d", time.Now().UnixNano())
	mcpMCRegisterRemoteCluster(t, admin, clusterID, clusterBName)

	// A real SHA256 fingerprint in OpenSSH form; the panel only checks the shape.
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("ssh public key: %v", err)
	}
	fingerprint := ssh.FingerprintSHA256(sshPub)
	const address = "sftp.e2e-remote.example.test:2222"

	resp, body, err := admin.Do(http.MethodPut, "/clusters/"+clusterID+"/sftp", map[string]string{
		"address":            address,
		"hostKeyFingerprint": fingerprint,
	})
	if err != nil {
		t.Fatalf("PUT remote sftp endpoint: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT remote sftp endpoint: status=%d body=%s", resp.StatusCode, body)
	}

	mint := func() (int64, string) {
		t.Helper()
		resp, body, err := admin.Post("/clusters/"+clusterID+"/sftp-token", nil)
		if err != nil {
			t.Fatalf("POST sftp-token: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("POST sftp-token: status=%d body=%s", resp.StatusCode, body)
		}
		var out struct {
			ID    int64  `json:"id"`
			Token string `json:"token"`
		}
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("decode sftp-token: %v", err)
		}
		if !strings.HasPrefix(out.Token, "gpsftps_") || out.ID == 0 {
			t.Fatalf("sftp-token: want gpsftps_ token and id, got id=%d token=%q", out.ID, out.Token)
		}
		return out.ID, out.Token
	}

	firstID, firstToken := mint()
	_, _ = mint()

	resp, body, err = admin.Post("/clusters/"+clusterID+"/sftp-token", nil)
	if err != nil {
		t.Fatalf("third POST sftp-token: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusConflict || !strings.Contains(string(body), "token_limit_reached") {
		t.Fatalf("third sftp token: want 409 token_limit_reached, got status=%d body=%s", resp.StatusCode, body)
	}

	resp, body, err = admin.Get("/clusters/" + clusterID + "/sftp")
	if err != nil {
		t.Fatalf("GET cluster sftp: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET cluster sftp: status=%d body=%s", resp.StatusCode, body)
	}
	var view struct {
		Local              bool   `json:"local"`
		Address            string `json:"address"`
		HostKeyFingerprint string `json:"hostKeyFingerprint"`
		Tokens             []struct {
			ID int64 `json:"id"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(body, &view); err != nil {
		t.Fatalf("decode cluster sftp view: %v", err)
	}
	if view.Local || view.Address != address || view.HostKeyFingerprint != fingerprint {
		t.Fatalf("cluster sftp view: local=%v address=%q fingerprint=%q, want remote %q %q",
			view.Local, view.Address, view.HostKeyFingerprint, address, fingerprint)
	}
	if len(view.Tokens) != 2 {
		t.Fatalf("cluster sftp view: want 2 active tokens, got %d", len(view.Tokens))
	}
	if strings.Contains(string(body), firstToken) {
		t.Fatalf("cluster sftp view leaks a token secret")
	}

	resp, body, err = admin.Delete(fmt.Sprintf("/clusters/%s/sftp-token/%d", clusterID, firstID))
	if err != nil {
		t.Fatalf("revoke sftp token: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke sftp token: status=%d body=%s", resp.StatusCode, body)
	}
	resp, body, err = admin.Get("/clusters/" + clusterID + "/sftp")
	if err != nil {
		t.Fatalf("GET cluster sftp after revoke: %v", err)
	}
	_ = resp.Body.Close()
	if err := json.Unmarshal(body, &view); err != nil || len(view.Tokens) != 1 {
		t.Fatalf("after revoke: want 1 active token, got %d (%v) body=%s", len(view.Tokens), err, body)
	}

	// The panel's own cluster takes its endpoint from Helm, never from PUT.
	resp, body, err = admin.Do(http.MethodPut, "/clusters/local/sftp", map[string]string{
		"address":            address,
		"hostKeyFingerprint": fingerprint,
	})
	if err != nil {
		t.Fatalf("PUT local sftp endpoint: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode < 400 || resp.StatusCode >= 500 || !strings.Contains(string(body), "endpoint_local") {
		t.Fatalf("PUT local sftp endpoint: want a 4xx endpoint_local refusal, got status=%d body=%s", resp.StatusCode, body)
	}
}
