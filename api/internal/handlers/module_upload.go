// Module bundle upload surface (admin-only via RBAC):
//
//   - POST   /modules/sources/{name}/upload            — store a bundle
//   - DELETE /modules/sources/{name}/upload/{module}   — remove a bundle
//
// The body is a tar.gz or zip holding one module directory (module.yaml
// + template.yaml [+ README.md, icon.png, locales/<tag>.json]). After validation the files
// are written into a ConfigMap labeled gameplane.local/module-upload=true in
// the operator namespace — the exact shape an upload-type ModuleSource
// indexes, and the same thing `kubectl apply` of such a ConfigMap
// produces, so the operator stays authoritative. ?dryRun=true validates
// and returns the parsed metadata without storing anything.

package handlers

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"

	"github.com/go-chi/chi/v5"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"

	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/i18n/pack"
)

// Bundle files are a couple of YAML docs and an icon; the ConfigMap
// backing them caps out at 1 MiB including metadata, so stay under it.
const maxUploadBundleBytes = 900 << 10

// maxUploadExtractedBytes bounds the total bytes extraction may produce
// across every member of one archive. It is counted as members are read,
// repeated member names included. The kept bundle files must still fit
// maxUploadBundleBytes; this budget only leaves room for ignored extras.
const maxUploadExtractedBytes = 4 << 20

// maxUploadDecompressedBytes bounds the whole decompressed tar stream —
// member data plus headers, padding and skipped entries — so entries
// extraction ignores still count. The 1 MiB headroom covers tar framing.
const maxUploadDecompressedBytes = maxUploadExtractedBytes + 1<<20

// labelModuleUpload mirrors the operator's v1alpha1.LabelModuleUpload
// (the api module doesn't depend on the operator module).
const (
	labelModuleUpload     = "gameplane.local/module-upload"
	labelUploadModuleName = "gameplane.local/module-name"
)

// bundleFileNames are the canonical files copied into the ConfigMap;
// anything else in the archive (junk like .DS_Store, nested docs) is
// ignored. Translation files locales/<tag>.json are copied too, under the
// ConfigMap key locales.<tag>.json (a key cannot contain "/").
var bundleFileNames = []string{"module.yaml", "template.yaml", "README.md", "icon.png"}

const (
	uploadLocalesDir       = "locales"
	uploadLocaleKeyPrefix  = "locales."
	uploadLocaleFileSuffix = ".json"
)

// uploadedMetadata is the subset of module.yaml the API validates and
// echoes back for the dashboard's preview.
type uploadedMetadata struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Version     string `json:"version"`
	Game        string `json:"game"`
	Summary     string `json:"summary,omitempty"`
}

type uploadResponse struct {
	Module    uploadedMetadata `json:"module"`
	ConfigMap string           `json:"configMap,omitempty"`
	DryRun    bool             `json:"dryRun,omitempty"`
}

func (h modulesHandler) uploadBundle(w http.ResponseWriter, req *http.Request) {
	srcName := chi.URLParam(req, "name")
	if err := h.requireUploadSource(req, srcName); err != nil {
		writeUploadSourceErr(w, req, err)
		return
	}

	body, err := io.ReadAll(io.LimitReader(req.Body, maxUploadBundleBytes+1))
	if err != nil {
		httperr.WriteCode(w, req, http.StatusBadRequest, err)
		return
	}
	if len(body) > maxUploadBundleBytes {
		httperr.WriteCode(w, req, http.StatusRequestEntityTooLarge,
			fmt.Errorf("bundle exceeds the %d KiB limit", maxUploadBundleBytes>>10))
		return
	}

	files, meta, err := parseUploadedBundle(body)
	if err != nil {
		httperr.WriteCode(w, req, http.StatusBadRequest, err)
		return
	}

	if req.URL.Query().Get("dryRun") == "true" {
		writeJSON(w, uploadResponse{Module: *meta, DryRun: true})
		return
	}

	cmName := "module-upload-" + meta.Name
	cm := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      cmName,
			Namespace: h.namespace,
			Labels: map[string]string{
				labelModuleUpload:     "true",
				labelUploadModuleName: meta.Name,
			},
		},
		BinaryData: files,
	}
	cms := h.k.Typed.CoreV1().ConfigMaps(h.namespace)
	if _, err := cms.Create(req.Context(), cm, metav1.CreateOptions{}); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			httperr.Write(w, req, err)
			return
		}
		existing, err := cms.Get(req.Context(), cmName, metav1.GetOptions{})
		if err != nil {
			httperr.Write(w, req, err)
			return
		}
		// Refuse to clobber a ConfigMap that isn't an upload bundle.
		if existing.Labels[labelModuleUpload] != "true" {
			httperr.WriteCode(w, req, http.StatusConflict,
				fmt.Errorf("configmap %q exists and is not a module upload", cmName))
			return
		}
		existing.Labels[labelUploadModuleName] = meta.Name
		existing.BinaryData = files
		existing.Data = nil
		if _, err := cms.Update(req.Context(), existing, metav1.UpdateOptions{}); err != nil {
			httperr.Write(w, req, err)
			return
		}
	}
	writeJSONCreated(w, uploadResponse{Module: *meta, ConfigMap: cmName})
}

func (h modulesHandler) deleteUpload(w http.ResponseWriter, req *http.Request) {
	srcName := chi.URLParam(req, "name")
	moduleName := chi.URLParam(req, "module")
	if err := h.requireUploadSource(req, srcName); err != nil {
		writeUploadSourceErr(w, req, err)
		return
	}

	// Installed Modules pull their bundle through the source on every
	// upgrade/re-apply; removing the bundle would strand them.
	mods, err := h.k.Dynamic.Resource(kube.GVRModule).List(req.Context(), metav1.ListOptions{})
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	for i := range mods.Items {
		name, _, _ := unstructured.NestedString(mods.Items[i].Object, "spec", "name")
		src, _, _ := unstructured.NestedString(mods.Items[i].Object, "spec", "source", "name")
		if name == moduleName && src == srcName {
			httperr.WriteCode(w, req, http.StatusConflict,
				fmt.Errorf("module %q is installed as %q; uninstall it before removing the upload",
					moduleName, mods.Items[i].GetName()))
			return
		}
	}

	cms := h.k.Typed.CoreV1().ConfigMaps(h.namespace)
	list, err := cms.List(req.Context(), metav1.ListOptions{
		LabelSelector: labelModuleUpload + "=true," + labelUploadModuleName + "=" + moduleName,
	})
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	if len(list.Items) == 0 {
		httperr.WriteCode(w, req, http.StatusNotFound,
			fmt.Errorf("no uploaded bundle for module %q", moduleName))
		return
	}
	for i := range list.Items {
		if err := cms.Delete(req.Context(), list.Items[i].Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			httperr.Write(w, req, err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// errNotUploadSource marks the named-source-has-wrong-type condition;
// the handlers surface it as a 409 with the safe message intact.
var errNotUploadSource = errors.New("uploads need an upload-type source")

// requireUploadSource confirms the named ModuleSource exists and is
// upload-typed.
func (h modulesHandler) requireUploadSource(req *http.Request, name string) error {
	src, err := h.k.Dynamic.Resource(kube.GVRModuleSource).Get(req.Context(), name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	typ, _, _ := unstructured.NestedString(src.Object, "spec", "type")
	if typ != "upload" {
		return fmt.Errorf("source %q is type %q: %w", name, typ, errNotUploadSource)
	}
	return nil
}

func writeUploadSourceErr(w http.ResponseWriter, req *http.Request, err error) {
	if errors.Is(err, errNotUploadSource) {
		httperr.WriteCode(w, req, http.StatusConflict, err)
		return
	}
	httperr.Write(w, req, err)
}

// parseUploadedBundle extracts and validates one module bundle.
func parseUploadedBundle(body []byte) (map[string][]byte, *uploadedMetadata, error) {
	extracted, err := extractUploadArchive(body)
	if err != nil {
		return nil, nil, err
	}

	// Locate the (single) directory holding module.yaml — flat archives
	// and one-folder-wrapped archives both work.
	var dirs []string
	for p := range extracted {
		if path.Base(p) == "module.yaml" {
			dirs = append(dirs, path.Dir(p))
		}
	}
	switch len(dirs) {
	case 0:
		return nil, nil, errors.New("bundle has no module.yaml")
	case 1:
	default:
		return nil, nil, fmt.Errorf("bundle holds %d modules; upload exactly one", len(dirs))
	}
	dir := dirs[0]

	files := map[string][]byte{}
	total := 0
	for _, name := range bundleFileNames {
		key := name
		if dir != "." {
			key = dir + "/" + name
		}
		if data, ok := extracted[key]; ok {
			files[name] = data
			total += len(data)
		}
	}
	localeFiles, err := uploadedLocaleFiles(extracted, dir)
	if err != nil {
		return nil, nil, err
	}
	for key, data := range localeFiles {
		files[key] = data
		total += len(data)
	}
	if total > maxUploadBundleBytes {
		return nil, nil, fmt.Errorf("bundle files exceed the %d KiB limit", maxUploadBundleBytes>>10)
	}

	var meta uploadedMetadata
	if err := yaml.Unmarshal(files["module.yaml"], &meta); err != nil {
		return nil, nil, fmt.Errorf("parse module.yaml: %w", err)
	}
	switch {
	case meta.Name == "":
		return nil, nil, errors.New("module.yaml: name is required")
	case !dnsLabelRE.MatchString(meta.Name):
		return nil, nil, errors.New("module.yaml: name must be a DNS label (lowercase, digits, hyphens)")
	case meta.DisplayName == "":
		return nil, nil, errors.New("module.yaml: displayName is required")
	case meta.Version == "":
		return nil, nil, errors.New("module.yaml: version is required")
	case meta.Game == "":
		return nil, nil, errors.New("module.yaml: game is required")
	}

	tmplRaw, ok := files["template.yaml"]
	if !ok || len(tmplRaw) == 0 {
		return nil, nil, errors.New("bundle has no template.yaml")
	}
	var tmpl struct {
		Spec map[string]any `json:"spec"`
	}
	if err := yaml.Unmarshal(tmplRaw, &tmpl); err != nil {
		return nil, nil, fmt.Errorf("parse template.yaml: %w", err)
	}
	if img, _ := tmpl.Spec["image"].(string); img == "" {
		return nil, nil, errors.New("template.yaml: spec.image is required")
	}

	return files, &meta, nil
}

// uploadedLocaleFiles collects the locales/<tag>.json members of the module
// directory dir as ConfigMap keys ("locales.<tag>.json"). The English
// reference en.json is never shipped, non-JSON members are ignored, and a file
// name that is not a valid language tag rejects the upload. Size is not capped
// here: the caller counts these bytes against the bundle limit, so an oversized
// upload is refused loudly instead of losing a language.
func uploadedLocaleFiles(extracted map[string][]byte, dir string) (map[string][]byte, error) {
	prefix := uploadLocalesDir + "/"
	if dir != "." {
		prefix = dir + "/" + prefix
	}
	out := map[string][]byte{}
	for key, data := range extracted {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		name := strings.TrimPrefix(key, prefix)
		if strings.Contains(name, "/") || !strings.HasSuffix(name, uploadLocaleFileSuffix) {
			continue
		}
		tag := strings.TrimSuffix(name, uploadLocaleFileSuffix)
		if tag == "en" {
			continue
		}
		if !pack.ValidTag(tag) {
			return nil, fmt.Errorf("locales/%s: %q is not a valid language tag", name, tag)
		}
		out[uploadLocaleKeyPrefix+tag+uploadLocaleFileSuffix] = data
	}
	return out, nil
}

var errUploadTooLarge = errors.New("archive expands past the decompression limit")

// budgetReader wraps an io.Reader, counts bytes read, and returns an error
// once more than a given limit have been read.
type budgetReader struct {
	r     io.Reader
	limit int64
	read  int64
}

func newBudgetReader(r io.Reader, limit int64) *budgetReader {
	return &budgetReader{r: r, limit: limit}
}

func (br *budgetReader) Read(p []byte) (int, error) {
	// Read at most one byte past the limit, and always ask the underlying
	// reader: a stream of exactly limit bytes must still end in its own
	// io.EOF, and only a byte beyond the limit is an error.
	if quota := br.limit - br.read + 1; int64(len(p)) > quota {
		p = p[:max(quota, 0)]
	}
	n, err := br.r.Read(p)
	br.read += int64(n)
	if br.read > br.limit {
		return n, fmt.Errorf("archive expands past the %d KiB total limit: %w", br.limit>>10, errUploadTooLarge)
	}
	return n, err
}

// extractUploadArchive unpacks a tar.gz or zip body (detected by magic
// bytes) with path-traversal rejection. The caller caps the compressed
// body. Here each member is capped at maxUploadBundleBytes, the archive
// at 256 distinct paths, the running total of extracted bytes at
// maxUploadExtractedBytes, and the whole decompressed stream at
// maxUploadDecompressedBytes, so extraction stops at the first member that
// takes the archive past its budget.
func extractUploadArchive(body []byte) (map[string][]byte, error) {
	out := map[string][]byte{}
	remaining := int64(maxUploadExtractedBytes)
	add := func(name string, r io.Reader) error {
		p := path.Clean(strings.ReplaceAll(name, `\`, "/"))
		if p == "." || strings.HasSuffix(name, "/") {
			return nil
		}
		if path.IsAbs(p) || p == ".." || strings.HasPrefix(p, "../") {
			return fmt.Errorf("archive member %q escapes the extraction root", name)
		}
		if len(out) >= 256 {
			return errors.New("archive has too many files")
		}
		limit := min(int64(maxUploadBundleBytes), remaining)
		data, err := io.ReadAll(io.LimitReader(r, limit+1))
		if err != nil {
			return err
		}
		if len(data) > maxUploadBundleBytes {
			return fmt.Errorf("archive member %q exceeds the %d KiB limit", name, maxUploadBundleBytes>>10)
		}
		if int64(len(data)) > remaining {
			return fmt.Errorf("archive expands past the %d KiB total limit", maxUploadExtractedBytes>>10)
		}
		remaining -= int64(len(data))
		out[p] = data
		return nil
	}

	switch {
	case len(body) >= 2 && body[0] == 0x1f && body[1] == 0x8b:
		gz, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		defer func() { _ = gz.Close() }()
		br := newBudgetReader(gz, maxUploadDecompressedBytes)
		tr := tar.NewReader(br)
		for {
			hdr, err := tr.Next()
			if errors.Is(err, io.EOF) {
				return out, nil
			}
			if errors.Is(err, errUploadTooLarge) {
				return nil, err
			}
			if err != nil {
				return nil, err
			}
			if hdr.Typeflag != tar.TypeReg {
				continue
			}
			if err := add(hdr.Name, tr); err != nil {
				return nil, err
			}
		}
	case bytes.HasPrefix(body, []byte("PK\x03\x04")):
		zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if f.FileInfo().IsDir() {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			err = add(f.Name, rc)
			_ = rc.Close()
			if err != nil {
				return nil, err
			}
		}
		return out, nil
	default:
		return nil, errors.New("unsupported bundle format (upload a .tar.gz or .zip)")
	}
}
