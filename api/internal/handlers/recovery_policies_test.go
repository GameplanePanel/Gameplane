package handlers

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestRecoveryPoliciesRoundTripThroughGenericAPI(t *testing.T) {
	for _, resource := range []struct{ path, kind string }{{"backups", "Backup"}, {"schedules", "BackupSchedule"}} {
		t.Run(resource.path, func(t *testing.T) {
			router := mountResourcesRouter(fakeKubeClient())
			body := map[string]any{"apiVersion": "gameplane.local/v1alpha1", "kind": resource.kind, "metadata": map[string]any{"name": "recovery"}, "spec": map[string]any{"serverRef": map[string]any{"name": "game"}, "repoRef": map[string]any{"name": "repo", "key": "repo"}, "schedule": "* * * * *", "strategy": "restic-snapshot", "powerPolicy": "stop-copy-restart", "volumePolicy": "all-data"}}
			rr := do(t, router, http.MethodPost, "/"+resource.path+"/", body)
			if rr.Code != http.StatusCreated {
				t.Fatalf("create: %d %s", rr.Code, rr.Body)
			}
			rr = do(t, router, http.MethodGet, "/"+resource.path+"/recovery", nil)
			var got struct {
				Spec struct{ PowerPolicy, VolumePolicy string }
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Spec.PowerPolicy != "stop-copy-restart" || got.Spec.VolumePolicy != "all-data" {
				t.Fatalf("policies lost: %+v", got.Spec)
			}
		})
	}
}
