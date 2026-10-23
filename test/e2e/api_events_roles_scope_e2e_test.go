//go:build e2e

package e2e

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// TestAPI_EventStreamAndRoleEdits_FollowCallerPermissions checks two
// permission controls with one custom-role user: the /events stream sends
// only the resource kinds the caller may read, and a role edit may not
// remove users:manage from the caller's own primary role.
//
// Login budget: 1 e2e-admin login plus 1 login as the test's own user.
func TestAPI_EventStreamAndRoleEdits_FollowCallerPermissions(t *testing.T) {
	t.Parallel()

	const ns = "gameplane-games"
	suffix := time.Now().UnixNano()
	roleName := fmt.Sprintf("e2e-scope-role-%d", suffix)
	tmpl := fmt.Sprintf("e2e-scope-tmpl-%d", suffix)
	gsName := fmt.Sprintf("e2e-scope-gs-%d", suffix)

	envInstance.BootstrapAdmin(t, adminUsername, adminPassword)
	admin := envInstance.APIClient(t, adminUsername, adminPassword)
	defer admin.Close()

	// Registered before the user so it runs after the user is deleted
	// (a role that is still bound can't be deleted).
	t.Cleanup(func() {
		r, _, _ := admin.Delete("/roles/" + roleName)
		if r != nil {
			_ = r.Body.Close()
		}
	})
	resp, body, err := admin.Post("/roles", map[string]any{
		"name":        roleName,
		"description": "e2e: server reads plus user and role management",
		"permissions": []string{"servers:read", "users:manage", "roles:manage"},
	})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create role: status=%d body=%s", resp.StatusCode, string(body))
	}

	userName, userPW, userID := envInstance.CreateUser(t, admin, roleName, "e2e-scope-user")
	t.Cleanup(func() {
		r, _, _ := admin.Delete("/users/" + userID)
		if r != nil {
			_ = r.Body.Close()
		}
	})

	applyBusyboxTemplate(t, tmpl)
	createGameServerViaAPI(t, admin, ns, gsName, tmpl)
	// /events starts at each collection's current resourceVersion. Drive real
	// updates throughout both streams instead of depending on initial replay
	// or other parallel tests to happen to mutate a readable resource kind.
	changes := func(ctx context.Context) error {
		patch, err := json.Marshal(map[string]any{"metadata": map[string]any{"annotations": map[string]string{
			"gameplane.local/e2e-event-sequence": time.Now().UTC().Format(time.RFC3339Nano),
		}}})
		if err != nil {
			return fmt.Errorf("encode event trigger: %w", err)
		}
		if _, err := envInstance.Dyn.Resource(gameTemplateGVR).Patch(ctx, tmpl, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
			return fmt.Errorf("trigger template event: %w", err)
		}
		if _, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).Patch(ctx, gsName, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
			return fmt.Errorf("trigger server event: %w", err)
		}
		return nil
	}

	user := envInstance.APIClient(t, userName, userPW)
	defer user.Close()

	t.Run("admin event stream includes templates", func(t *testing.T) {
		kinds, found := streamEventKinds(t, admin, ns, func(kind, name string) bool { return kind == "templates" && name == tmpl }, 0, changes)
		if !found {
			t.Fatalf("admin stream sent no templates event; kinds=%v", kinds)
		}
	})

	t.Run("event stream sends only kinds the caller may read", func(t *testing.T) {
		kinds, found := streamEventKinds(t, user, ns, func(kind, name string) bool { return kind == "servers" && name == gsName }, 3*time.Second, changes)
		if !found {
			t.Fatalf("user stream never sent server %s; kinds=%v", gsName, kinds)
		}
		for _, k := range []string{"templates", "backups", "schedules", "restores"} {
			if kinds[k] {
				t.Errorf("user stream sent %q events without that kind's read permission; kinds=%v", k, kinds)
			}
		}
	})

	t.Run("role edit may not remove the caller's own user management", func(t *testing.T) {
		resp, body, err := user.Patch("/roles/"+roleName, map[string]any{
			"permissions": []string{"servers:read", "roles:manage"},
		})
		if err != nil {
			t.Fatalf("PATCH own role: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("PATCH own role dropping users:manage: status=%d want=400 body=%s", resp.StatusCode, string(body))
		}

		lr, lb, err := admin.Get("/roles")
		if err != nil {
			t.Fatalf("list roles: %v", err)
		}
		defer func() { _ = lr.Body.Close() }()
		var roles []struct {
			Name        string   `json:"name"`
			Permissions []string `json:"permissions"`
		}
		if err := json.Unmarshal(lb, &roles); err != nil {
			t.Fatalf("decode roles: %v body=%s", err, string(lb))
		}
		for _, r := range roles {
			if r.Name == roleName {
				if !slices.Contains(r.Permissions, "users:manage") {
					t.Errorf("role %s lost users:manage although the edit was refused: %v", roleName, r.Permissions)
				}
				return
			}
		}
		t.Errorf("role %s not in listing", roleName)
	})
}

// streamEventKinds reads GET /events as cli and returns the set of event
// kinds it received. When match reports true for a frame, it keeps
// reading for grace (0 = stop at once); otherwise it stops after 30s.
// changes keeps both permitted and forbidden kinds active while connecting and
// reading; its worker is cancelled and joined before the helper returns.
// found reports whether match was ever true.
func streamEventKinds(t *testing.T, cli *APIClient, ns string, match func(kind, name string) bool, grace time.Duration, changes func(context.Context) error) (map[string]bool, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	changesDone := make(chan struct{})
	var changesErr error
	go func() {
		defer close(changesDone)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			if err := changes(ctx); err != nil {
				if ctx.Err() == nil {
					changesErr = err
				}
				cancel()
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	defer func() {
		cancel()
		<-changesDone
		if changesErr != nil {
			t.Errorf("event trigger failed: %v", changesErr)
		}
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cli.BaseURL+"/events?namespace="+ns, nil)
	if err != nil {
		t.Fatalf("new events request: %v", err)
	}
	resp, err := cli.HTTP.Do(req)
	if err != nil {
		t.Fatalf("open events stream: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("events stream: status=%d", resp.StatusCode)
	}

	kinds := map[string]bool{}
	found := false
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 8<<20)
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data: ")
		if !ok {
			continue
		}
		var frame struct {
			Kind   string `json:"kind"`
			Object struct {
				Metadata struct {
					Name string `json:"name"`
				} `json:"metadata"`
			} `json:"object"`
		}
		if json.Unmarshal([]byte(data), &frame) != nil {
			continue
		}
		kinds[frame.Kind] = true
		if !found && match(frame.Kind, frame.Object.Metadata.Name) {
			found = true
			if grace <= 0 {
				break
			}
			time.AfterFunc(grace, cancel)
		}
	}
	return kinds, found
}
