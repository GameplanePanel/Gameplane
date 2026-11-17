package handlers

import (
	"encoding/json"
	"net/http"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
)

func TestSharePowerPolicy(t *testing.T) {
	for _, tc := range []struct {
		name      string
		suspended bool
		canStart  bool
		wantStart int
	}{
		{name: "manual-stop", suspended: true, canStart: true, wantStart: http.StatusConflict},
		{name: "idle-asleep", canStart: true, wantStart: http.StatusAccepted},
		{name: "view-only", canStart: false, wantStart: http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := newTestStore(t)
			ownerID := insertShareTestUser(t, store, "owner-power-"+tc.name)
			server := newShareTestServer("srv-power", ownerID)
			server.Object["spec"] = map[string]any{"suspend": tc.suspended}
			server.Object["status"] = map[string]any{"phase": "Stopped"}
			annotations := server.GetAnnotations()
			// Include a stale idle marker for the manual stop, before reconciliation.
			annotations["gameplane.local/idle-asleep-since"] = "2026-11-17T00:00:00Z"
			server.SetAnnotations(annotations)
			client := fakeKubeClient(server)
			reg := kube.NewRegistry("local")
			reg.Set("local", client)
			token, _, err := store.CreateShareLink(t.Context(), "local", "gameplane-games", "srv-power", ownerID, tc.canStart, nil)
			if err != nil {
				t.Fatal(err)
			}
			handler := mountSharesRouter(reg, store)
			remote := "power-" + tc.name + ":1"
			status, body := shareReq(t, handler, http.MethodGet, "/shares/"+token, nil, nil, remote)
			if status != http.StatusOK {
				t.Fatalf("resolve status=%d body=%s", status, body)
			}
			var result map[string]any
			if err := json.Unmarshal(body, &result); err != nil {
				t.Fatal(err)
			}
			if result["manuallyStopped"] != tc.suspended || result["canStart"] != (tc.canStart && !tc.suspended) {
				t.Fatalf("unexpected public power state: %s", body)
			}
			status, body = shareReq(t, handler, http.MethodPost, "/shares/"+token+"/start", nil, nil, remote)
			if status != tc.wantStart {
				t.Fatalf("start status=%d want=%d body=%s", status, tc.wantStart, body)
			}
			live, err := client.Dynamic.Resource(kube.GVRs["servers"]).Namespace("gameplane-games").Get(t.Context(), "srv-power", metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			suspended, _, err := unstructured.NestedBool(live.Object, "spec", "suspend")
			if err != nil || suspended != tc.suspended {
				t.Fatalf("share start changed manual power policy: suspended=%v err=%v", suspended, err)
			}
			wake := live.GetAnnotations()[idleWakeRequestedAnnotation] != ""
			if wake != (tc.wantStart == http.StatusAccepted) {
				t.Fatalf("wake annotation present=%v, response=%d", wake, status)
			}
		})
	}
}
