//go:build e2e

package e2e

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// uploadBudgetBundle builds a tar.gz module bundle for module name, plus the
// given extra members.
func uploadBudgetBundle(t *testing.T, name string, extra map[string][]byte) []byte {
	t.Helper()
	files := map[string][]byte{
		name + "/module.yaml": []byte("apiVersion: gameplane.local/module/v1\nname: " + name +
			"\ndisplayName: E2E upload budget\nversion: 1.0.0\ngame: busybox\n"),
		name + "/template.yaml": []byte("apiVersion: gameplane.local/v1alpha1\nkind: GameTemplate\nspec:\n" +
			"  displayName: E2E upload budget\n  game: busybox\n  version: 1.0.0\n  image: busybox:1.36\n"),
	}
	for k, v := range extra {
		files[k] = v
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for fname, content := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name: fname, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if _, err := tw.Write(content); err != nil {
			t.Fatalf("tar write: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

// postUploadBudgetBundle POSTs a raw archive body with the session's CSRF
// header (APIClient.Do only sends JSON bodies).
func postUploadBudgetBundle(t *testing.T, cli *APIClient, path string, body []byte) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, cli.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/gzip")
	req.Header.Set("X-Gameplane-CSRF", cli.CSRF)
	resp, err := cli.HTTP.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	rb, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(rb)
}

// TestAPI_ModuleUpload_ExtractionStaysWithinBudget checks, through the real
// API, that a valid module bundle upload is stored, and that a bundle whose
// archive expands past the total extraction budget is refused with 400 and
// nothing is stored.
func TestAPI_ModuleUpload_ExtractionStaysWithinBudget(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	suffix := strconv.FormatInt(time.Now().UnixNano()%1_000_000_000, 10)
	srcName := "e2e-upload-budget-" + suffix
	okModule := "e2e-upload-ok-" + suffix
	bigModule := "e2e-upload-big-" + suffix

	srcGVR := schema.GroupVersionResource{Group: "gameplane.local", Version: "v1alpha1", Resource: "modulesources"}
	src := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "ModuleSource",
		"metadata":   map[string]any{"name": srcName},
		"spec":       map[string]any{"type": "upload"},
	}}
	if _, err := envInstance.Dyn.Resource(srcGVR).Create(ctx, src, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create upload ModuleSource: %v", err)
	}
	t.Cleanup(func() {
		for _, m := range []string{okModule, bigModule} {
			_ = envInstance.K8s.CoreV1().ConfigMaps(credentialsNS).Delete(context.Background(), "module-upload-"+m, metav1.DeleteOptions{})
		}
		_ = envInstance.Dyn.Resource(srcGVR).Delete(context.Background(), srcName, metav1.DeleteOptions{})
	})

	envInstance.BootstrapAdmin(t, adminUsername, adminPassword)
	cli := envInstance.APIClient(t, adminUsername, adminPassword)
	defer cli.Close()

	uploadPath := "/modules/sources/" + srcName + "/upload"

	// A valid bundle is stored as a labeled ConfigMap.
	status, body := postUploadBudgetBundle(t, cli, uploadPath, uploadBudgetBundle(t, okModule, nil))
	if status != http.StatusCreated {
		t.Fatalf("valid upload: got %d body=%q, want 201", status, body)
	}
	cm, err := envInstance.K8s.CoreV1().ConfigMaps(credentialsNS).Get(ctx, "module-upload-"+okModule, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get stored bundle: %v", err)
	}
	if cm.Labels["gameplane.local/module-upload"] != "true" ||
		!strings.Contains(string(cm.BinaryData["template.yaml"]), "busybox:1.36") {
		t.Fatalf("stored bundle labels=%v keys=%d", cm.Labels, len(cm.BinaryData))
	}

	// A bundle whose members each fit the per-member cap but together
	// expand past the total budget is refused and not stored.
	pad := bytes.Repeat([]byte{0}, 800<<10)
	extra := map[string][]byte{}
	for i := range 6 {
		extra[fmt.Sprintf("%s/extra/%02d.bin", bigModule, i)] = pad
	}
	big := uploadBudgetBundle(t, bigModule, extra)
	if len(big) > 900<<10 {
		t.Fatalf("over-budget fixture compressed to %d bytes; it must fit the request cap", len(big))
	}
	status, body = postUploadBudgetBundle(t, cli, uploadPath, big)
	if status != http.StatusBadRequest {
		t.Fatalf("over-budget upload: got %d body=%q, want 400", status, body)
	}
	if _, err := envInstance.K8s.CoreV1().ConfigMaps(credentialsNS).Get(ctx, "module-upload-"+bigModule, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("over-budget upload was stored (get err = %v)", err)
	}

	// Removing the stored bundle through the API cleans it up.
	delResp, delBody, err := cli.Delete(uploadPath + "/" + okModule)
	if err != nil {
		t.Fatalf("DELETE upload: %v", err)
	}
	defer delResp.Body.Close()
	if delResp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE upload: got %d body=%q, want 204", delResp.StatusCode, string(delBody))
	}
}
