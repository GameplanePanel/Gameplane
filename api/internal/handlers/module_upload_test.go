package handlers

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
)

const uploadModuleYAML = `apiVersion: gameplane.local/module/v1
name: factorio
displayName: Factorio
version: 2.0.0
game: factorio
summary: The factory must grow.
`

const uploadTemplateYAML = `apiVersion: gameplane.local/v1alpha1
kind: GameTemplate
spec:
  displayName: Factorio
  game: factorio
  version: 2.0.0
  image: factoriotools/factorio:stable
`

func newUploadSource(name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "ModuleSource",
		"metadata":   map[string]any{"name": name},
		"spec":       map[string]any{"type": "upload"},
	}}
}

func bundleTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatalf("tar: %v", err)
		}
		_, _ = tw.Write([]byte(content))
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func bundleZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip: %v", err)
		}
		_, _ = w.Write([]byte(content))
	}
	_ = zw.Close()
	return buf.Bytes()
}

func doBytes(t *testing.T, h http.Handler, method, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, path, bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func validBundleFiles() map[string]string {
	return map[string]string{
		"factorio/module.yaml":   uploadModuleYAML,
		"factorio/template.yaml": uploadTemplateYAML,
		"factorio/README.md":     "# Factorio\n",
	}
}

func TestUploadBundle(t *testing.T) {
	t.Run("tar.gz happy path writes labeled configmap", func(t *testing.T) {
		k := fakeKubeClient(newUploadSource("uploads"))
		r := mountModulesRouter(k)

		rr := doBytes(t, r, "POST", "/modules/sources/uploads/upload", bundleTarGz(t, validBundleFiles()))
		if rr.Code != http.StatusCreated {
			t.Fatalf("got %d %s", rr.Code, rr.Body)
		}
		var resp uploadResponse
		_ = json.NewDecoder(rr.Body).Decode(&resp)
		if resp.Module.Name != "factorio" || resp.Module.Version != "2.0.0" || resp.ConfigMap == "" {
			t.Fatalf("resp = %+v", resp)
		}

		cm, err := k.Typed.CoreV1().ConfigMaps("gameplane-system").Get(context.Background(), resp.ConfigMap, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("get cm: %v", err)
		}
		if cm.Labels[labelModuleUpload] != "true" || cm.Labels[labelUploadModuleName] != "factorio" {
			t.Errorf("labels = %v", cm.Labels)
		}
		if !strings.Contains(string(cm.BinaryData["template.yaml"]), "factoriotools") {
			t.Errorf("binaryData keys = %v", len(cm.BinaryData))
		}
	})

	t.Run("zip with flat layout", func(t *testing.T) {
		k := fakeKubeClient(newUploadSource("uploads"))
		r := mountModulesRouter(k)
		flat := map[string]string{"module.yaml": uploadModuleYAML, "template.yaml": uploadTemplateYAML}
		rr := doBytes(t, r, "POST", "/modules/sources/uploads/upload", bundleZip(t, flat))
		if rr.Code != http.StatusCreated {
			t.Fatalf("got %d %s", rr.Code, rr.Body)
		}
	})

	t.Run("re-upload updates the configmap", func(t *testing.T) {
		k := fakeKubeClient(newUploadSource("uploads"))
		r := mountModulesRouter(k)
		if rr := doBytes(t, r, "POST", "/modules/sources/uploads/upload", bundleTarGz(t, validBundleFiles())); rr.Code != http.StatusCreated {
			t.Fatalf("first upload: %d", rr.Code)
		}
		files := validBundleFiles()
		files["factorio/template.yaml"] = strings.ReplaceAll(uploadTemplateYAML, "stable", "1.1.110")
		if rr := doBytes(t, r, "POST", "/modules/sources/uploads/upload", bundleTarGz(t, files)); rr.Code != http.StatusCreated {
			t.Fatalf("re-upload: %d", rr.Code)
		}
		cm, _ := k.Typed.CoreV1().ConfigMaps("gameplane-system").Get(context.Background(), "module-upload-factorio", metav1.GetOptions{})
		if !strings.Contains(string(cm.BinaryData["template.yaml"]), "1.1.110") {
			t.Error("re-upload did not update content")
		}
	})

	t.Run("dry run validates without writing", func(t *testing.T) {
		k := fakeKubeClient(newUploadSource("uploads"))
		r := mountModulesRouter(k)
		rr := doBytes(t, r, "POST", "/modules/sources/uploads/upload?dryRun=true", bundleTarGz(t, validBundleFiles()))
		if rr.Code != http.StatusOK {
			t.Fatalf("got %d %s", rr.Code, rr.Body)
		}
		var resp uploadResponse
		_ = json.NewDecoder(rr.Body).Decode(&resp)
		if !resp.DryRun || resp.Module.Name != "factorio" {
			t.Fatalf("resp = %+v", resp)
		}
		cms, _ := k.Typed.CoreV1().ConfigMaps("gameplane-system").List(context.Background(), metav1.ListOptions{})
		if len(cms.Items) != 0 {
			t.Errorf("dry run wrote %d configmaps", len(cms.Items))
		}
	})

	t.Run("rejections", func(t *testing.T) {
		k := fakeKubeClient(
			newUploadSource("uploads"),
			newSource("registry", nil), // not upload-typed
		)
		r := mountModulesRouter(k)

		cases := []struct {
			name string
			path string
			body []byte
			want int
		}{
			{"wrong source type", "/modules/sources/registry/upload", bundleTarGz(t, validBundleFiles()), http.StatusConflict},
			{"missing source", "/modules/sources/ghost/upload", bundleTarGz(t, validBundleFiles()), http.StatusNotFound},
			{"not an archive", "/modules/sources/uploads/upload", []byte("plain text"), http.StatusBadRequest},
			{"no module.yaml", "/modules/sources/uploads/upload",
				bundleTarGz(t, map[string]string{"factorio/template.yaml": uploadTemplateYAML}), http.StatusBadRequest},
			{"two modules", "/modules/sources/uploads/upload", bundleTarGz(t, map[string]string{
				"a/module.yaml": uploadModuleYAML, "a/template.yaml": uploadTemplateYAML,
				"b/module.yaml": strings.ReplaceAll(uploadModuleYAML, "factorio", "other"), "b/template.yaml": uploadTemplateYAML,
			}), http.StatusBadRequest},
			{"missing required metadata", "/modules/sources/uploads/upload", bundleTarGz(t, map[string]string{
				"m/module.yaml":   "name: x\nversion: 1.0.0\ngame: x\n", // no displayName
				"m/template.yaml": uploadTemplateYAML,
			}), http.StatusBadRequest},
			{"template without image", "/modules/sources/uploads/upload", bundleTarGz(t, map[string]string{
				"m/module.yaml":   uploadModuleYAML,
				"m/template.yaml": "spec:\n  game: factorio\n",
			}), http.StatusBadRequest},
			{"path traversal", "/modules/sources/uploads/upload", bundleTarGz(t, map[string]string{
				"../evil/module.yaml": uploadModuleYAML,
			}), http.StatusBadRequest},
		}
		for _, tc := range cases {
			if rr := doBytes(t, r, "POST", tc.path, tc.body); rr.Code != tc.want {
				t.Errorf("%s: got %d %s, want %d", tc.name, rr.Code, rr.Body, tc.want)
			}
		}
	})
}

func TestDeleteUpload(t *testing.T) {
	seed := func(t *testing.T, k *kube.Client) {
		t.Helper()
		r := mountModulesRouter(k)
		if rr := doBytes(t, r, "POST", "/modules/sources/uploads/upload", bundleTarGz(t, validBundleFiles())); rr.Code != http.StatusCreated {
			t.Fatalf("seed upload: %d", rr.Code)
		}
	}

	t.Run("removes the configmap", func(t *testing.T) {
		k := fakeKubeClient(newUploadSource("uploads"))
		seed(t, k)
		r := mountModulesRouter(k)
		if rr := doBytes(t, r, "DELETE", "/modules/sources/uploads/upload/factorio", nil); rr.Code != http.StatusNoContent {
			t.Fatalf("got %d %s", rr.Code, rr.Body)
		}
		cms, _ := k.Typed.CoreV1().ConfigMaps("gameplane-system").List(context.Background(), metav1.ListOptions{})
		if len(cms.Items) != 0 {
			t.Errorf("configmap not removed")
		}
	})

	t.Run("blocked while installed", func(t *testing.T) {
		k := fakeKubeClient(
			newUploadSource("uploads"),
			newModule("factorio", map[string]any{"source": map[string]any{"name": "uploads"}, "name": "factorio"}),
		)
		seed(t, k)
		r := mountModulesRouter(k)
		if rr := doBytes(t, r, "DELETE", "/modules/sources/uploads/upload/factorio", nil); rr.Code != http.StatusConflict {
			t.Fatalf("got %d %s", rr.Code, rr.Body)
		}
	})

	t.Run("missing bundle is 404", func(t *testing.T) {
		k := fakeKubeClient(newUploadSource("uploads"))
		r := mountModulesRouter(k)
		if rr := doBytes(t, r, "DELETE", "/modules/sources/uploads/upload/ghost", nil); rr.Code != http.StatusNotFound {
			t.Fatalf("got %d %s", rr.Code, rr.Body)
		}
	})
}

// overBudgetPadding returns archive members that are each under the
// per-member cap but together exceed maxUploadExtractedBytes.
func overBudgetPadding(prefix string) map[string]string {
	pad := strings.Repeat("\x00", maxUploadBundleBytes-1024)
	files := map[string]string{}
	for i := range maxUploadExtractedBytes/len(pad) + 2 {
		files[fmt.Sprintf("%s%02d.bin", prefix, i)] = pad
	}
	return files
}

// TestExtractUploadArchive_EnforcesTotalExtractedBudget checks that
// extraction stops once the members together pass the total budget, even
// though each member stays under the per-member cap and the compressed
// body fits the request cap.
func TestExtractUploadArchive_EnforcesTotalExtractedBudget(t *testing.T) {
	files := overBudgetPadding("pad/")
	for format, build := range map[string]func(*testing.T, map[string]string) []byte{
		"tar.gz": bundleTarGz,
		"zip":    bundleZip,
	} {
		body := build(t, files)
		if len(body) > maxUploadBundleBytes {
			t.Fatalf("%s fixture compressed to %d bytes; it must fit the request cap", format, len(body))
		}
		if _, err := extractUploadArchive(body); err == nil || !strings.Contains(err.Error(), "total limit") {
			t.Fatalf("%s: err = %v, want the total extracted-size limit error", format, err)
		}
	}
}

// TestExtractUploadArchive_RepeatedMemberNamesCountTowardBudget checks that
// every member read counts toward the total budget, including members that
// reuse a name already extracted.
func TestExtractUploadArchive_RepeatedMemberNamesCountTowardBudget(t *testing.T) {
	member := bytes.Repeat([]byte{0}, maxUploadBundleBytes-1024)
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for range maxUploadExtractedBytes/len(member) + 2 {
		if err := tw.WriteHeader(&tar.Header{
			Name: "pad.bin", Mode: 0o644, Size: int64(len(member)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if _, err := tw.Write(member); err != nil {
			t.Fatalf("tar write: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	if buf.Len() > maxUploadBundleBytes {
		t.Fatalf("fixture compressed to %d bytes; it must fit the request cap", buf.Len())
	}
	if _, err := extractUploadArchive(buf.Bytes()); err == nil || !strings.Contains(err.Error(), "total limit") {
		t.Fatalf("err = %v, want the total extracted-size limit error", err)
	}
}

// TestExtractUploadArchive_SkippedTarEntriesCountTowardBudget checks that
// tar entries skipped by extraction (directories, symlinks, etc.) still count
// toward the decompression budget, so a small compressed bundle with many
// headers can exceed the budget.
func TestExtractUploadArchive_SkippedTarEntriesCountTowardBudget(t *testing.T) {
	// Each tar header is 512 bytes. We need enough to exceed maxUploadDecompressedBytes.
	headerCount := maxUploadDecompressedBytes/512 + 64
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for i := range headerCount {
		dirName := fmt.Sprintf("d/%06d/", i)
		if err := tw.WriteHeader(&tar.Header{
			Name: dirName, Mode: 0o755, Typeflag: tar.TypeDir,
		}); err != nil {
			t.Fatalf("tar header: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	if buf.Len() > maxUploadBundleBytes {
		t.Fatalf("fixture compressed to %d bytes; it must fit the request cap", buf.Len())
	}
	if _, err := extractUploadArchive(buf.Bytes()); err == nil || !strings.Contains(err.Error(), "total limit") {
		t.Fatalf("err = %v, want the total decompressed-size limit error", err)
	}
}

// TestUploadBundle_RejectsArchiveOverTotalExtractedBudget checks that an
// otherwise valid bundle whose archive expands past the total budget is
// refused with 400 and nothing is stored.
func TestUploadBundle_RejectsArchiveOverTotalExtractedBudget(t *testing.T) {
	k := fakeKubeClient(newUploadSource("uploads"))
	r := mountModulesRouter(k)

	files := validBundleFiles()
	for name, content := range overBudgetPadding("factorio/extra/") {
		files[name] = content
	}
	body := bundleTarGz(t, files)
	if len(body) > maxUploadBundleBytes {
		t.Fatalf("fixture compressed to %d bytes; it must fit the request cap", len(body))
	}

	rr := doBytes(t, r, "POST", "/modules/sources/uploads/upload", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("got %d %s, want 400", rr.Code, rr.Body)
	}
	cms, err := k.Typed.CoreV1().ConfigMaps("gameplane-system").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list configmaps: %v", err)
	}
	if len(cms.Items) != 0 {
		t.Fatalf("rejected upload stored %d configmaps", len(cms.Items))
	}
}

// TestBudgetReader_ExactLimitEndsInEOF checks that a stream of exactly the
// limit reads through to its own io.EOF, even in small chunks.
func TestBudgetReader_ExactLimitEndsInEOF(t *testing.T) {
	br := newBudgetReader(bytes.NewReader(make([]byte, 10)), 10)
	buf := make([]byte, 4)
	total := 0
	for {
		n, err := br.Read(buf)
		total += n
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("read after %d bytes: %v", total, err)
		}
	}
	if total != 10 {
		t.Fatalf("read %d bytes, want 10", total)
	}
}

// TestBudgetReader_OverLimitFails checks that one byte past the limit is
// refused with the budget error, and stays refused.
func TestBudgetReader_OverLimitFails(t *testing.T) {
	br := newBudgetReader(bytes.NewReader(make([]byte, 11)), 10)
	_, err := io.ReadAll(br)
	if !errors.Is(err, errUploadTooLarge) || !strings.Contains(err.Error(), "total limit") {
		t.Fatalf("err = %v, want the total limit budget error", err)
	}
	if _, err := br.Read(make([]byte, 1)); !errors.Is(err, errUploadTooLarge) {
		t.Fatalf("read after the limit: err = %v, want the budget error", err)
	}
}
