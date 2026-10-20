package handlers

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
)

func setupTestBuilderRouter(t *testing.T, objects ...runtime.Object) (http.Handler, *kubefake.Clientset) {
	t.Helper()
	scheme := runtime.NewScheme()
	dyn := dynamicfake.NewSimpleDynamicClient(scheme, objects...)
	k8s := kubefake.NewSimpleClientset()
	k := &kube.Client{
		Dynamic: dyn,
		Typed:   k8s,
	}
	r := chi.NewRouter()
	MountModules(r, k, "gameplane-system")
	return r, k8s
}

func TestBuilderScaffold(t *testing.T) {
	r, _ := setupTestBuilderRouter(t)

	reqBody := BuilderScaffoldRequest{
		Name:        "cs2-match",
		DisplayName: "Counter-Strike 2 Match",
		Archetype:   "steamcmd",
		Image:       "ghcr.io/gameplanepanel/cs2:latest@sha256:4b9a8e23f0c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7",
		Ports: []BuilderPortDef{
			{Name: "game", ContainerPort: 27015, Protocol: "UDP", Advertise: true},
		},
		Categories:       []string{"Shooter", "Co-op"},
		Summary:          "Dedicated server for competitive CS2 matches",
		StorageSize:      "25Gi",
		StorageMountPath: "/cs2-data",
	}
	jsonBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/modules/builder/scaffold", bytes.NewReader(jsonBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var resp BuilderScaffoldResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if !strings.Contains(resp.ModuleYaml, "name: cs2-match") {
		t.Errorf("module.yaml missing name: %s", resp.ModuleYaml)
	}
	if !strings.Contains(resp.TemplateYaml, "kind: GameTemplate") {
		t.Errorf("template.yaml missing GameTemplate: %s", resp.TemplateYaml)
	}
	if !strings.Contains(resp.TemplateYaml, "size: 25Gi") {
		t.Errorf("template.yaml missing custom storage size: %s", resp.TemplateYaml)
	}
	if !strings.Contains(resp.TemplateYaml, "mountPath: /cs2-data") {
		t.Errorf("template.yaml missing custom mountPath: %s", resp.TemplateYaml)
	}
	if !strings.Contains(resp.ReadmeMd, "Counter-Strike 2 Match") {
		t.Errorf("README.md missing display name: %s", resp.ReadmeMd)
	}
}

func TestBuilderScaffold_InvalidName(t *testing.T) {
	r, _ := setupTestBuilderRouter(t)

	reqBody := BuilderScaffoldRequest{
		Name: "Invalid_Name_Uppercase",
	}
	jsonBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/modules/builder/scaffold", bytes.NewReader(jsonBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request, got %d: %s", w.Code, w.Body.String())
	}
}

func TestBuilderValidate(t *testing.T) {
	r, _ := setupTestBuilderRouter(t)

	validModule := `apiVersion: gameplane.local/module/v1
name: my-game
displayName: My Game
version: 1.0.0
game: my-game
summary: A dedicated game server
categories:
  - Shooter
`
	validTemplate := `apiVersion: gameplane.local/v1alpha1
kind: GameTemplate
metadata:
  name: my-game
  labels:
    gameplane.local/module: my-game
spec:
  displayName: My Game
  game: my-game
  version: 1.0.0
  image: ghcr.io/valgul/my-game:v1.0@sha256:1111111111111111111111111111111111111111111111111111111111111111
  ports:
    - name: game
      containerPort: 7777
      protocol: UDP
`

	reqBody := BuilderValidateRequest{
		ModuleYaml:   validModule,
		TemplateYaml: validTemplate,
	}
	jsonBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/modules/builder/validate", bytes.NewReader(jsonBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var resp BuilderValidateResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if !resp.Clean || resp.ErrorCount > 0 {
		t.Fatalf("expected clean validation, got Clean=%v, Errors=%d, Findings: %+v", resp.Clean, resp.ErrorCount, resp.Findings)
	}

	// Test unpinned image
	unpinnedTemplate := `apiVersion: gameplane.local/v1alpha1
kind: GameTemplate
metadata:
  name: my-game
  labels:
    gameplane.local/module: my-game
spec:
  displayName: My Game
  game: my-game
  version: 1.0.0
  image: ghcr.io/valgul/my-game:latest
  ports:
    - name: game
      containerPort: 7777
      protocol: UDP
`
	reqBody.TemplateYaml = unpinnedTemplate
	jsonBytes, _ = json.Marshal(reqBody)
	req = httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/modules/builder/validate", bytes.NewReader(jsonBytes))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Clean || resp.ErrorCount == 0 {
		t.Fatalf("expected validation errors for unpinned image, got Clean=%v, Errors=%d", resp.Clean, resp.ErrorCount)
	}
}

func TestBuilderPreview(t *testing.T) {
	r, _ := setupTestBuilderRouter(t)

	templateYaml := `apiVersion: gameplane.local/v1alpha1
kind: GameTemplate
metadata:
  name: mc-test
spec:
  displayName: Minecraft Server
  game: minecraft
  version: 1.20.4
  image: itzg/minecraft-server:java21@sha256:1111111111111111111111111111111111111111111111111111111111111111
  ports:
    - name: game
      containerPort: 25565
      protocol: TCP
      advertise: true
    - name: rcon
      containerPort: 25575
      protocol: TCP
      advertise: false
  configSchema:
    - name: MEMORY
      type: string
      autoFromMemoryLimit:
        percent: 75
    - name: MOTD
      type: string
      default: "Welcome to Gameplane"
  env:
    - name: EULA
      value: "TRUE"
`

	reqBody := BuilderPreviewRequest{
		TemplateYaml: templateYaml,
		Memory:       "4Gi",
		Config: map[string]string{
			"MOTD": "Custom Server",
		},
	}
	jsonBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/modules/builder/preview", bytes.NewReader(jsonBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var resp BuilderPreviewResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Ports) != 2 {
		t.Fatalf("expected 2 ports, got %d", len(resp.Ports))
	}
	if !resp.Ports[0].Advertise {
		t.Errorf("expected port 0 Advertise=true")
	}
	if resp.Ports[1].Advertise {
		t.Errorf("expected port 1 Advertise=false")
	}

	if resp.ComputedConfig["MEMORY"] != "3072M" {
		t.Errorf("expected computed MEMORY 3072M, got %q", resp.ComputedConfig["MEMORY"])
	}
	if resp.ComputedConfig["MOTD"] != "Custom Server" {
		t.Errorf("expected MOTD 'Custom Server', got %q", resp.ComputedConfig["MOTD"])
	}

	foundEula := false
	for _, env := range resp.EffectiveEnv {
		if env.Name == "EULA" && env.Value == "TRUE" {
			foundEula = true
		}
	}
	if !foundEula {
		t.Errorf("expected EULA=TRUE in effectiveEnv, got %+v", resp.EffectiveEnv)
	}
}

func TestBuilderExport_DownloadArchive(t *testing.T) {
	r, _ := setupTestBuilderRouter(t)

	validModule := "apiVersion: gameplane.local/module/v1\nname: cs2-match\ndisplayName: CS2 Match\nversion: 1.0.0\ngame: cs2\nsummary: A CS2 server\ncategories:\n  - Shooter\n"
	validTemplate := "apiVersion: gameplane.local/v1alpha1\nkind: GameTemplate\nmetadata:\n  name: cs2-match\nspec:\n  displayName: CS2 Match\n  game: cs2\n  version: 1.0.0\n  image: \"ghcr.io/valgul/cs2:v1@sha256:1111111111111111111111111111111111111111111111111111111111111111\"\n  ports:\n    - name: game\n      containerPort: 27015\n      protocol: UDP\n"

	reqBody := BuilderExportRequest{
		Name:         "cs2-match",
		ModuleYaml:   validModule,
		TemplateYaml: validTemplate,
		ReadmeMd:     "# CS2 Match\n",
	}
	jsonBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/modules/builder/export", bytes.NewReader(jsonBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	if ct := w.Header().Get("Content-Type"); ct != "application/gzip" {
		t.Errorf("expected application/gzip, got %q", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "cs2-match.tar.gz") {
		t.Errorf("expected Content-Disposition with cs2-match.tar.gz, got %q", cd)
	}

	// Verify tar.gz content
	gz, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatalf("invalid gzip stream: %v", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	foundFiles := make(map[string]bool)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar read error: %v", err)
		}
		foundFiles[hdr.Name] = true
	}

	for _, expected := range []string{"module.yaml", "template.yaml", "README.md", "icon.png"} {
		if !foundFiles[expected] {
			t.Errorf("expected file %q in archive, found: %+v", expected, foundFiles)
		}
	}
}

func TestBuilderExport_InstallToCluster(t *testing.T) {
	uploadSource := newUploadSource("uploads")
	r, k8s := setupTestBuilderRouter(t, uploadSource)

	validModule := "apiVersion: gameplane.local/module/v1\nname: cs2-match\ndisplayName: CS2 Match\nversion: 1.0.0\ngame: cs2\nsummary: A CS2 server\ncategories:\n  - Shooter\n"
	validTemplate := "apiVersion: gameplane.local/v1alpha1\nkind: GameTemplate\nmetadata:\n  name: cs2-match\nspec:\n  displayName: CS2 Match\n  game: cs2\n  version: 1.0.0\n  image: \"ghcr.io/valgul/cs2:v1@sha256:1111111111111111111111111111111111111111111111111111111111111111\"\n  ports:\n    - name: game\n      containerPort: 27015\n      protocol: UDP\n"

	reqBody := BuilderExportRequest{
		Name:         "cs2-match",
		ModuleYaml:   validModule,
		TemplateYaml: validTemplate,
		ReadmeMd:     "# CS2 Match\n",
		TargetSource: "uploads",
	}
	jsonBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/modules/builder/export", bytes.NewReader(jsonBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", w.Code, w.Body.String())
	}

	var resp BuilderExportResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !resp.Installed || resp.ModuleName != "cs2-match" {
		t.Fatalf("expected installed cs2-match, got %+v", resp)
	}

	// Check ConfigMap was created in k8s fake
	cm, err := k8s.CoreV1().ConfigMaps("gameplane-system").Get(context.Background(), "module-upload-cs2-match", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get created ConfigMap: %v", err)
	}
	if cm.Labels[labelModuleUpload] != "true" || cm.Labels[labelUploadModuleName] != "cs2-match" {
		t.Errorf("unexpected labels on ConfigMap: %+v", cm.Labels)
	}
	if _, ok := cm.BinaryData["module.yaml"]; !ok {
		t.Errorf("expected module.yaml in ConfigMap BinaryData: %+v", cm.BinaryData)
	}
}

func TestBuilderExport_ValidationFailure(t *testing.T) {
	r, _ := setupTestBuilderRouter(t)

	// Missing image in template will fail validation
	reqBody := BuilderExportRequest{
		Name:         "cs2-match",
		ModuleYaml:   "apiVersion: gameplane.local/module/v1\nname: cs2-match\nversion: 1.0.0\ngame: cs2\n",
		TemplateYaml: "apiVersion: gameplane.local/v1alpha1\nkind: GameTemplate\nspec:\n  game: cs2\n",
		ReadmeMd:     "# CS2 Match\n",
	}
	jsonBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/modules/builder/export", bytes.NewReader(jsonBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request on invalid manifest, got %d: %s", w.Code, w.Body.String())
	}
}

func TestBuilderExport_NameMismatch(t *testing.T) {
	r, _ := setupTestBuilderRouter(t)

	reqBody := BuilderExportRequest{
		Name:         "different-name",
		ModuleYaml:   "apiVersion: gameplane.local/module/v1\nname: cs2-match\nversion: 1.0.0\ngame: cs2\n",
		TemplateYaml: "apiVersion: gameplane.local/v1alpha1\nkind: GameTemplate\nspec:\n  displayName: CS2 Match\n  game: cs2\n  version: 1.0.0\n  image: \"ghcr.io/valgul/cs2:v1@sha256:1111111111111111111111111111111111111111111111111111111111111111\"\n",
		ReadmeMd:     "# CS2 Match\n",
	}
	jsonBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/modules/builder/export", bytes.NewReader(jsonBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request on name mismatch, got %d: %s", w.Code, w.Body.String())
	}
}

func TestBuilderScaffold_Errors(t *testing.T) {
	r, _ := setupTestBuilderRouter(t)

	// Invalid JSON
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/modules/builder/scaffold", strings.NewReader("invalid json"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid JSON, got %d", w.Code)
	}

	// Port collision
	reqBody := BuilderScaffoldRequest{
		Name:      "my-game",
		Archetype: "steamcmd",
		Ports: []BuilderPortDef{
			{Name: "p1", ContainerPort: 8080, Protocol: "UDP"},
			{Name: "p2", ContainerPort: 8080, Protocol: "UDP"},
		},
	}
	jb, _ := json.Marshal(reqBody)
	req = httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/modules/builder/scaffold", bytes.NewReader(jb))
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for port collision, got %d", w.Code)
	}
}

func TestBuilderValidate_Errors(t *testing.T) {
	r, _ := setupTestBuilderRouter(t)

	// Invalid JSON
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/modules/builder/validate", strings.NewReader("invalid json"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid JSON, got %d", w.Code)
	}

	// Empty dirName fallback
	reqBody := BuilderValidateRequest{
		ModuleYaml:   "invalid: [yaml",
		TemplateYaml: "invalid: [yaml",
	}
	jb, _ := json.Marshal(reqBody)
	req = httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/modules/builder/validate", bytes.NewReader(jb))
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for validate even with errors, got %d", w.Code)
	}
}

func TestBuilderPreview_Errors(t *testing.T) {
	r, _ := setupTestBuilderRouter(t)

	// Invalid JSON
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/modules/builder/preview", strings.NewReader("invalid json"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid JSON, got %d", w.Code)
	}

	// Invalid template
	reqBody := BuilderPreviewRequest{
		TemplateYaml: "invalid yaml: [",
	}
	jb, _ := json.Marshal(reqBody)
	req = httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/modules/builder/preview", bytes.NewReader(jb))
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid template YAML, got %d", w.Code)
	}
}

func TestBuilderExport_Errors(t *testing.T) {
	r, _ := setupTestBuilderRouter(t)

	// Invalid JSON
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/modules/builder/export", strings.NewReader("invalid json"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}

	// Missing Name
	reqBody := BuilderExportRequest{
		Name:         "",
		ModuleYaml:   "name: test",
		TemplateYaml: "kind: GameTemplate",
	}
	jb, _ := json.Marshal(reqBody)
	req = httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/modules/builder/export", bytes.NewReader(jb))
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing name, got %d", w.Code)
	}

	// Missing ModuleYaml
	reqBody.Name = "test"
	reqBody.ModuleYaml = ""
	jb, _ = json.Marshal(reqBody)
	req = httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/modules/builder/export", bytes.NewReader(jb))
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing moduleYaml, got %d", w.Code)
	}

	// Missing TemplateYaml
	reqBody.ModuleYaml = "name: test"
	reqBody.TemplateYaml = ""
	jb, _ = json.Marshal(reqBody)
	req = httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/modules/builder/export", bytes.NewReader(jb))
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing templateYaml, got %d", w.Code)
	}

	// Bad ModuleYaml YAML syntax
	reqBody.TemplateYaml = "kind: GameTemplate"
	reqBody.ModuleYaml = "invalid: [yaml"
	jb, _ = json.Marshal(reqBody)
	req = httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/modules/builder/export", bytes.NewReader(jb))
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid moduleYaml syntax, got %d", w.Code)
	}
}

func TestBuilderExport_InstallToCluster_UpdateAndConflicts(t *testing.T) {
	uploadSource := newUploadSource("uploads")
	r, k8s := setupTestBuilderRouter(t, uploadSource)

	validModule := "apiVersion: gameplane.local/module/v1\nname: cs2-match\ndisplayName: CS2 Match\nversion: 1.0.0\ngame: cs2\nsummary: A CS2 server\ncategories:\n  - Shooter\n"
	validTemplate := "apiVersion: gameplane.local/v1alpha1\nkind: GameTemplate\nmetadata:\n  name: cs2-match\nspec:\n  displayName: CS2 Match\n  game: cs2\n  version: 1.0.0\n  image: \"ghcr.io/valgul/cs2:v1@sha256:1111111111111111111111111111111111111111111111111111111111111111\"\n  ports:\n    - name: game\n      containerPort: 27015\n      protocol: UDP\n"

	// 1. TargetSource does not exist -> 404
	reqBody := BuilderExportRequest{
		Name:         "cs2-match",
		ModuleYaml:   validModule,
		TemplateYaml: validTemplate,
		TargetSource: "nonexistent",
	}
	jb, _ := json.Marshal(reqBody)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/modules/builder/export", bytes.NewReader(jb))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for nonexistent source, got %d: %s", w.Code, w.Body.String())
	}

	// 2. Pre-create ConfigMap with labelModuleUpload="true" -> successfully updates
	existingCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "module-upload-cs2-match",
			Namespace: "gameplane-system",
			Labels: map[string]string{
				labelModuleUpload:     "true",
				labelUploadModuleName: "cs2-match",
			},
		},
	}
	_, err := k8s.CoreV1().ConfigMaps("gameplane-system").Create(context.Background(), existingCM, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("failed to create existing ConfigMap: %v", err)
	}

	reqBody.TargetSource = "uploads"
	reqBody.IconBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=" // 1x1 png
	jb, _ = json.Marshal(reqBody)
	req = httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/modules/builder/export", bytes.NewReader(jb))
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 for update existing, got %d: %s", w.Code, w.Body.String())
	}

	// 3. Pre-create ConfigMap WITHOUT labelModuleUpload -> conflict 409
	nonUploadCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "module-upload-other",
			Namespace: "gameplane-system",
			Labels:    map[string]string{},
		},
	}
	_, _ = k8s.CoreV1().ConfigMaps("gameplane-system").Create(context.Background(), nonUploadCM, metav1.CreateOptions{})

	validModuleOther := "apiVersion: gameplane.local/module/v1\nname: other\ndisplayName: Other\nversion: 1.0.0\ngame: other\nsummary: Other server\ncategories:\n  - Other\n"
	validTemplateOther := "apiVersion: gameplane.local/v1alpha1\nkind: GameTemplate\nmetadata:\n  name: other\nspec:\n  displayName: Other\n  game: other\n  version: 1.0.0\n  image: \"ghcr.io/valgul/cs2:v1@sha256:1111111111111111111111111111111111111111111111111111111111111111\"\n  ports:\n    - name: game\n      containerPort: 27015\n      protocol: UDP\n"
	reqBodyOther := BuilderExportRequest{
		Name:         "other",
		ModuleYaml:   validModuleOther,
		TemplateYaml: validTemplateOther,
		TargetSource: "uploads",
	}
	jb, _ = json.Marshal(reqBodyOther)
	req = httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/modules/builder/export", bytes.NewReader(jb))
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 for conflict, got %d: %s", w.Code, w.Body.String())
	}
}

func TestBuilderArchetypes(t *testing.T) {
	r, _ := setupTestBuilderRouter(t)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/modules/builder/archetypes", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var resp BuilderArchetypesResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Archetypes) == 0 {
		t.Fatalf("expected non-empty archetypes array, got %d items", len(resp.Archetypes))
	}

	// Verify deterministic order: steamcmd first, java second, generic third
	if len(resp.Archetypes) > 0 && resp.Archetypes[0].ID != "steamcmd" {
		t.Errorf("expected first archetype to be 'steamcmd', got %q", resp.Archetypes[0].ID)
	}
	if len(resp.Archetypes) > 1 && resp.Archetypes[1].ID != "java" {
		t.Errorf("expected second archetype to be 'java', got %q", resp.Archetypes[1].ID)
	}
	if len(resp.Archetypes) > 2 && resp.Archetypes[2].ID != "generic" {
		t.Errorf("expected third archetype to be 'generic', got %q", resp.Archetypes[2].ID)
	}

	// Verify steamcmd has STEAMAPPID in defaultEnv
	steamcmd := resp.Archetypes[0]
	hasSteamAppID := false
	for _, env := range steamcmd.DefaultEnv {
		if env.Name == "STEAMAPPID" {
			hasSteamAppID = true
			break
		}
	}
	if !hasSteamAppID {
		t.Errorf("expected steamcmd to have STEAMAPPID in defaultEnv, got %+v", steamcmd.DefaultEnv)
	}

	// Verify all archetypes have required fields
	for i, arch := range resp.Archetypes {
		if arch.ID == "" {
			t.Errorf("archetype %d missing ID", i)
		}
		if arch.Title == "" {
			t.Errorf("archetype %d (%s) missing Title", i, arch.ID)
		}
		if arch.DefaultImage == "" {
			t.Errorf("archetype %d (%s) missing DefaultImage", i, arch.ID)
		}
	}
}
