package handlers

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
)

func TestRedact_LastAppliedConfigurationNeverLeavesServerReads(t *testing.T) {
	const annotation = "kubectl.kubernetes.io/last-applied-configuration"
	for _, state := range []string{"password", "missing", "null", "empty", "malformed"} {
		t.Run(state, func(t *testing.T) {
			gs := serverWithConfig("alpha", plainConfig())
			spec := gs.Object["spec"].(map[string]any)
			switch state {
			case "missing":
				delete(spec, "config")
			case "null":
				spec["config"] = nil
			case "empty":
				spec["config"] = map[string]any{}
			case "malformed":
				spec["config"] = "invalid"
			}
			annotations := map[string]string{
				annotation:         `{"spec":{"config":{"REMOVED_PASSWORD":"historical-secret"}}}`,
				"example.com/note": "keep-me",
			}
			gs.SetAnnotations(annotations)
			k := fakeKubeClient(redactTemplate(), gs)
			r := mountResourcesRouter(k)
			for _, path := range []string{"/servers/alpha", "/servers/"} {
				rr := do(t, r, http.MethodGet, path, nil)
				if rr.Code != http.StatusOK {
					t.Fatalf("%s: %d %s", path, rr.Code, rr.Body)
				}
				body := rr.Body.String()
				assertNoSecretLeak(t, body)
				if strings.Contains(body, annotation) || strings.Contains(body, "historical-secret") {
					t.Fatalf("%s exposed last-applied configuration: %s", path, body)
				}
				if !strings.Contains(body, "keep-me") {
					t.Fatalf("%s lost ordinary metadata: %s", path, body)
				}
			}
			stored, err := k.Dynamic.Resource(kube.GVRs["servers"]).Namespace(redactNS).Get(t.Context(), "alpha", metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(stored.GetAnnotations(), annotations) {
				t.Fatalf("redaction changed persisted annotations: %v", stored.GetAnnotations())
			}
		})
	}
}
