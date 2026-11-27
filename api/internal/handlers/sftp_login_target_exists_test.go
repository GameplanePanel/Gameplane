package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// TestSFTPLogin_TargetMustExist covers FR-009a: a namespace-wide servers:read grant passes the access
// check for any name, so a login to a server that does not exist must be refused like a wrong password.
func TestSFTPLogin_TargetMustExist(t *testing.T) {
	env := newSFTPLoginEnv(t)
	h := sftpLoginRouter(env)
	name, id := env.seedUser(t, "x")
	// Admin on every namespace of the default cluster: Allow passes for any server name.
	if _, err := env.store.DB.ExecContext(t.Context(),
		`INSERT INTO user_role_bindings(user_id, role_name, cluster, namespace) VALUES (?, 'admin', ?, '*')`,
		id, scope.DefaultCluster); err != nil {
		t.Fatalf("bind admin role: %v", err)
	}
	pw, err := env.sessions.GenerateAndStoreSFTPPassword(t.Context(), id, 24)
	if err != nil {
		t.Fatalf("password: %v", err)
	}
	env.fake.items = []unstructured.Unstructured{sftpFakeServer(scope.DefaultNamespace, "survival", id)}

	rr := env.login(t, h, map[string]any{"username": name + "+doesnotexist", "password": pw})
	if rr.Code != http.StatusUnauthorized || sftpProblemID(t, rr) != "server.sftp.login_failed" {
		t.Fatalf("missing target login: status %d body %s", rr.Code, rr.Body.String())
	}

	rr = env.login(t, h, map[string]any{"username": name + "+survival", "password": pw})
	if rr.Code != http.StatusOK {
		t.Fatalf("existing target login: status %d body %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		SessionToken string `json:"sessionToken"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if !strings.HasPrefix(resp.SessionToken, auth.SFTPSessionTokenPrefix) {
		t.Fatalf("existing target login returned no session token: %s", rr.Body.String())
	}
}
