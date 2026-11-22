package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
	"github.com/GameplanePanel/gameplane/api/internal/ws"
)

// fakeAgentFiles is an AgentFileReader backed by a map; a path absent from the
// map is ws.ErrAgentFileNotFound, err (when set) wins over everything.
type fakeAgentFiles struct {
	files map[string]string
	err   error
	calls []string
}

func (f *fakeAgentFiles) ReadFileForCluster(_ context.Context, _, _, _, path string, limit int64) ([]byte, error) {
	f.calls = append(f.calls, path)
	if f.err != nil {
		return nil, f.err
	}
	body, ok := f.files[path]
	if !ok {
		return nil, ws.ErrAgentFileNotFound
	}
	if int64(len(body)) > limit {
		body = body[:limit]
	}
	return []byte(body), nil
}

// modpackTestTemplate declares the three registries like the minecraft-java
// module: modrinth (adoptable), curseforge (needs the shared key on java8) and
// ftb (modpacks only). Versions: a Java 21 NeoForge entry and a java8-multiarch
// Forge entry. Fields displayName/version/image make it valid for envtest too.
func modpackTestTemplate(name string) *unstructured.Unstructured {
	versions := []any{
		map[string]any{"id": "1.21.1-neoforge", "displayName": "1.21.1 NeoForge", "image": "itzg/minecraft-server:java21",
			"loader": "neoforge", "gameVersion": "1.21.1", "default": true},
		map[string]any{"id": "1.12.2-forge", "displayName": "1.12.2 Forge", "image": "itzg/minecraft-server:java8-multiarch@sha256:0123456789abcdef",
			"loader": "forge", "gameVersion": "1.12.2"},
	}
	tmpl := newTemplateObj(name, nil, versions)
	_ = unstructured.SetNestedField(tmpl.Object, map[string]any{
		"providers": []any{
			map[string]any{"provider": "modrinth", "modpacks": map[string]any{
				"refEnv": "MODRINTH_MODPACK", "versionEnv": "MODRINTH_VERSION",
				"adoptVersionFile": map[string]any{"path": ".modrinth-modpack-manifest.json", "field": "versionId"},
				"env":              []any{map[string]any{"name": "TYPE", "value": "MODRINTH"}},
			}},
			map[string]any{"provider": "curseforge", "curseforgeGameID": int64(432), "modpacks": map[string]any{
				"refEnv": "CF_SLUG", "versionEnv": "CF_FILE_ID", "apiKeyEnv": "CF_API_KEY",
				"needsKeyImageTags": []any{"java8-multiarch", "java8"},
				"missingFilesPath":  "MODS_NEED_DOWNLOAD.txt",
				"env": []any{
					map[string]any{"name": "TYPE", "value": "AUTO_CURSEFORGE"},
					map[string]any{"name": "CF_DOWNLOADS_REPO", "value": "/data/downloads"},
				},
			}},
			map[string]any{"provider": "ftb", "modpacksOnly": true, "modpacks": map[string]any{
				"refEnv": "FTB_MODPACK_ID", "versionEnv": "FTB_MODPACK_VERSION_ID",
				"env": []any{map[string]any{"name": "TYPE", "value": "FTBA"}},
			}},
		},
	}, "spec", "capabilities", "mods", "registry")
	_ = unstructured.SetNestedField(tmpl.Object, "Modpack test", "spec", "displayName")
	_ = unstructured.SetNestedField(tmpl.Object, "1", "spec", "version")
	_ = unstructured.SetNestedField(tmpl.Object, "itzg/minecraft-server:java21", "spec", "image")
	return tmpl
}

// mountModpackRouter mounts the registry routes over a one-cluster registry
// (no RBAC middleware).
func mountModpackRouter(k *kube.Client, opts RegistryOptions) http.Handler {
	clients := kube.NewRegistry(scope.DefaultCluster)
	clients.Set(scope.DefaultCluster, k)
	r := chi.NewRouter()
	MountRegistryWithOptions(r, clients, fakeSet{p: &fakeProvider{}}, opts)
	return r
}

// modpackReq performs a request that asks for problem+json errors.
func modpackReq(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *strings.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		rdr = strings.NewReader(string(b))
	} else {
		rdr = strings.NewReader("")
	}
	req := httptest.NewRequestWithContext(t.Context(), method, path, rdr)
	req.Header.Set("Accept", "application/problem+json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// problemID returns the messageId of a problem+json response body.
func problemID(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var p struct {
		MessageID string `json:"messageId"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode problem %q: %v", rr.Body.String(), err)
	}
	return p.MessageID
}

func decodeModpackView(t *testing.T, rr *httptest.ResponseRecorder) modpackView {
	t.Helper()
	var v modpackView
	if err := json.Unmarshal(rr.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode view %q: %v", rr.Body.String(), err)
	}
	return v
}

// modpackRow joins columns with 3 spaces, like the image's table.
func modpackRow(cols ...string) string { return strings.Join(cols, "   ") }

// NOTE: hand-made sample (no real MODS_NEED_DOWNLOAD.txt has been captured).
func modsNeedDownloadSample() string {
	return strings.Join([]string{
		modpackRow("Mod", "Version Name", "Filename", "Download page"),
		modpackRow("Some Mod", "1.2.3", "somemod-1.2.3.jar", "https://www.curseforge.com/minecraft/mc-mods/some-mod/download/123"),
		modpackRow("Another Mod", "2.0.0 beta", "another-2.0.0.jar", "https://www.curseforge.com/minecraft/mc-mods/another-mod/download/456"),
		modpackRow("Short Mod", "short.jar", "https://www.curseforge.com/minecraft/mc-mods/short/download/9"),
		modpackRow("Evil Mod", "1.0", "evil.jar", "https://evil.example.com/minecraft/mc-mods/evil/download/1"),
		modpackRow("Look Alike", "1.0", "look.jar", "https://www.curseforge.com.evil.example/x"),
		modpackRow("Plain Http", "1.0", "plain.jar", "http://www.curseforge.com/x"),
		modpackRow("No Link", "1.0", "nolink.jar"),
		"",
	}, "\n")
}

func TestParseModsNeedDownload_RowsAndColumns(t *testing.T) {
	got := parseModsNeedDownload([]byte(modsNeedDownloadSample()))
	want := []modpackMissingFile{
		{Mod: "Some Mod", VersionName: "1.2.3", Filename: "somemod-1.2.3.jar", DownloadPage: "https://www.curseforge.com/minecraft/mc-mods/some-mod/download/123"},
		{Mod: "Another Mod", VersionName: "2.0.0 beta", Filename: "another-2.0.0.jar", DownloadPage: "https://www.curseforge.com/minecraft/mc-mods/another-mod/download/456"},
		{Mod: "Short Mod", VersionName: "", Filename: "short.jar", DownloadPage: "https://www.curseforge.com/minecraft/mc-mods/short/download/9"},
	}
	if len(got) != len(want) {
		t.Fatalf("rows = %d (%+v), want %d: header, non-CurseForge, http and link-less rows must be dropped", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestParseModsNeedDownload_Caps(t *testing.T) {
	line := modpackRow("Mod", "1", "f.jar", "https://www.curseforge.com/minecraft/mc-mods/m/download/1")
	many := strings.Repeat(line+"\n", 5000)
	if got := parseModsNeedDownload([]byte(many)); len(got) != missingFilesMaxRows {
		t.Errorf("rows = %d, want the %d-row cap", len(got), missingFilesMaxRows)
	}
	huge := strings.Repeat("x", 4*missingFilesReadCap)
	if got := parseModsNeedDownload([]byte(huge)); len(got) != 0 {
		t.Errorf("oversized junk produced %d rows", len(got))
	}
	long := modpackRow(strings.Repeat("m", 1000), "1", "f.jar", "https://www.curseforge.com/x/y")
	got := parseModsNeedDownload([]byte(long))
	if len(got) != 1 || len([]rune(got[0].Mod)) != missingFieldMaxRunes {
		t.Errorf("long field not truncated to %d runes: %+v", missingFieldMaxRunes, got)
	}
	ctl := modpackRow("Bad\x1b[31mMod", "1", "f.jar", "https://www.curseforge.com/x/y")
	if got := parseModsNeedDownload([]byte(ctl)); len(got) != 1 || strings.ContainsRune(got[0].Mod, 0x1b) {
		t.Errorf("control characters kept: %+v", got)
	}
}

func modpackServerObj(name, tmpl, version, phase string, pack map[string]any) *unstructured.Unstructured {
	gs := serverWithVersion(scope.DefaultNamespace, name, tmpl, version)
	if pack != nil {
		_ = unstructured.SetNestedMap(gs.Object, pack, "spec", "modpack")
	}
	if phase != "" {
		_ = unstructured.SetNestedField(gs.Object, phase, "status", "phase")
	}
	return gs
}

func TestGetModpack_MissingFiles(t *testing.T) {
	cf := map[string]any{"provider": "curseforge", "project": "atm10", "version": "6512345"}
	for _, tc := range []struct {
		name        string
		phase       string
		files       *fakeAgentFiles
		wantRows    int
		wantUnknown bool
		wantCalls   int
	}{
		{"file present", "Running", &fakeAgentFiles{files: map[string]string{"MODS_NEED_DOWNLOAD.txt": modsNeedDownloadSample()}}, 3, false, 1},
		{"file absent", "Running", &fakeAgentFiles{files: map[string]string{}}, 0, false, 1},
		{"agent error", "Running", &fakeAgentFiles{err: context.DeadlineExceeded}, 0, true, 1},
		{"no pod (stopped)", "Stopped", &fakeAgentFiles{files: map[string]string{}}, 0, true, 0},
		{"no pod (suspended)", "Suspended", &fakeAgentFiles{files: map[string]string{}}, 0, true, 0},
		{"no pod (no status)", "", &fakeAgentFiles{files: map[string]string{}}, 0, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k := fakeKubeClient(modpackTestTemplate("minecraft"), modpackServerObj("alpha", "minecraft", "1.21.1-neoforge", tc.phase, cf))
			rr := modpackReq(t, mountModpackRouter(k, RegistryOptions{Files: tc.files}), http.MethodGet, "/servers/alpha/modpack", nil)
			if rr.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rr.Code, rr.Body)
			}
			v := decodeModpackView(t, rr)
			if len(v.MissingFiles) != tc.wantRows || v.MissingFilesUnknown != tc.wantUnknown || len(tc.files.calls) != tc.wantCalls {
				t.Errorf("rows=%d unknown=%v calls=%v, want rows=%d unknown=%v calls=%d",
					len(v.MissingFiles), v.MissingFilesUnknown, tc.files.calls, tc.wantRows, tc.wantUnknown, tc.wantCalls)
			}
			if v.DownloadsPath != "downloads/" {
				t.Errorf("downloadsPath = %q, want downloads/", v.DownloadsPath)
			}
		})
	}
}

func TestGetModpack_ModrinthHasNoMissingFiles(t *testing.T) {
	files := &fakeAgentFiles{files: map[string]string{"MODS_NEED_DOWNLOAD.txt": modsNeedDownloadSample()}}
	mr := map[string]any{"provider": "modrinth", "project": "cobblemon", "version": "v1"}
	k := fakeKubeClient(modpackTestTemplate("minecraft"), modpackServerObj("alpha", "minecraft", "1.21.1-neoforge", "Running", mr))
	rr := modpackReq(t, mountModpackRouter(k, RegistryOptions{Files: files}), http.MethodGet, "/servers/alpha/modpack", nil)
	v := decodeModpackView(t, rr)
	if len(v.MissingFiles) != 0 || v.MissingFilesUnknown || v.DownloadsPath != "" || len(files.calls) != 0 {
		t.Errorf("modrinth pack must not read the list: %+v calls=%v", v, files.calls)
	}
}

func TestImageTag(t *testing.T) {
	for in, want := range map[string]string{
		"itzg/minecraft-server:java21":                        "java21",
		"itzg/minecraft-server:java8-multiarch@sha256:abcdef": "java8-multiarch",
		"registry.local:5000/itzg/minecraft-server:java8":     "java8",
		"registry.local:5000/itzg/minecraft-server":           "",
		"itzg/minecraft-server@sha256:abcdef":                 "",
		"":                                                    "",
	} {
		if got := imageTag(in); got != want {
			t.Errorf("imageTag(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestProviders_ModsBrowseAndBlockedReason(t *testing.T) {
	// java21 image: nothing blocked; ftb is modpacks-only.
	k := fakeKubeClient(modpackTestTemplate("minecraft"), modpackServerObj("alpha", "minecraft", "1.21.1-neoforge", "", nil))
	rr := modpackReq(t, mountModpackRouter(k, RegistryOptions{}), http.MethodGet, "/servers/alpha/mods/registry/providers", nil)
	var got []providerInfo
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil || len(got) != 3 {
		t.Fatalf("decode %q: %v", rr.Body, err)
	}
	if !got[0].ModsBrowse || got[2].ModsBrowse || got[2].Provider != "ftb" {
		t.Errorf("modsBrowse wrong: %+v", got)
	}
	for _, p := range got {
		if p.ModpacksBlockedReason != "" {
			t.Errorf("java21 image: %s blocked (%q)", p.Provider, p.ModpacksBlockedReason)
		}
	}
	// java8-multiarch image, sharing off (no home client): curseforge blocked only.
	k = fakeKubeClient(modpackTestTemplate("minecraft"), modpackServerObj("alpha", "minecraft", "1.12.2-forge", "", nil))
	rr = modpackReq(t, mountModpackRouter(k, RegistryOptions{}), http.MethodGet, "/servers/alpha/mods/registry/providers", nil)
	got = nil
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got[1].Provider != "curseforge" || got[1].ModpacksBlockedReason != "curseforge_key_not_shared" || got[0].ModpacksBlockedReason != "" {
		t.Errorf("blocked reasons wrong: %+v", got)
	}
}
