package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/rbac"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// The SFTP server listing carries the file-access check's write decision per server: a namespace-wide
// servers:read user may write only the server they own.
func TestSFTPServersUnit_ListCarriesWrite(t *testing.T) {
	fake := &sftpFakeServers{items: []unstructured.Unstructured{
		sftpFakeServer(scope.DefaultNamespace, "mine", 7),
		sftpFakeServer(scope.DefaultNamespace, "theirs", 8),
	}}
	files := rbac.NewFileAccess(fake, fake)
	user := &auth.User{ID: 7, Username: "seven", Role: "viewer", Perms: map[string]map[string]map[string]struct{}{
		scope.DefaultCluster: {scope.DefaultNamespace: {"servers:read": {}}},
	}}
	caller := auth.SFTPCaller{Cluster: scope.DefaultCluster, SessionID: 3}
	h := sftpServersRouter(files, func(ctx context.Context) context.Context {
		return auth.WithUser(auth.WithSFTPCaller(ctx, caller), user)
	})

	rr := sftpUnitGet(t, h, "/sftp/v1/servers")
	var out struct {
		Items []sftpServerItem `json:"items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil || rr.Code != http.StatusOK {
		t.Fatalf("status %d err %v body %s", rr.Code, err, rr.Body.String())
	}
	got := map[string]bool{}
	for _, it := range out.Items {
		got[it.Server] = it.Write
	}
	if len(got) != 2 {
		t.Fatalf("items = %+v, want both servers", out.Items)
	}
	if !got["mine"] {
		t.Errorf("owned server: write = false, want true")
	}
	if got["theirs"] {
		t.Errorf("read-only server: write = true, want false")
	}
}
