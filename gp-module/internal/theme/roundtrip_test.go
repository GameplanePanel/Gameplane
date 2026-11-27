package theme

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GameplanePanel/gameplane/themepkg"
)

// readJSON decodes a JSON file into T, failing the test on error.
func readJSON[T any](t *testing.T, path string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(readFile(t, path), &v); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return v
}

// blobPath returns where an OCI layout keeps the blob with the given digest.
func blobPath(layout, digest string) string {
	return filepath.Join(layout, "blobs", "sha256", strings.TrimPrefix(digest, "sha256:"))
}

// TestRoundTripExportPackageValidate takes a spec 016 export through from-export,
// validate and package, reads the OCI layout back and checks it with themepkg
// again. Every layer must match its digest, and the tokens and CSS must survive.
func TestRoundTripExportPackageValidate(t *testing.T) {
	root := t.TempDir()
	dir := buildValidPackage(t, root)

	pkg, err := Inspect(dir)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if !pkg.Valid() {
		t.Fatalf("packaged theme is invalid: %v", pkg.Problems)
	}
	bundle, err := BuildBundle(pkg)
	if err != nil {
		t.Fatalf("BuildBundle: %v", err)
	}
	if bundle.Name != "deep-ocean" || bundle.Version != "1.2.0" {
		t.Fatalf("bundle identity = %s %s", bundle.Name, bundle.Version)
	}
	wantLayers := []struct{ title, mediaType string }{
		{"theme.yaml", MediaMetadata},
		{"theme.css", MediaCSS},
		{"previews/dark.png", MediaPNG},
	}
	if len(bundle.Layers) != len(wantLayers) {
		t.Fatalf("layers = %d, want %d", len(bundle.Layers), len(wantLayers))
	}
	for i, want := range wantLayers {
		got := bundle.Layers[i]
		if got.Title != want.title || got.MediaType != want.mediaType {
			t.Fatalf("layer %d = %s (%s), want %s (%s)", i, got.Title, got.MediaType, want.title, want.mediaType)
		}
	}

	layout := filepath.Join(root, "layout")
	manifestDigest, err := WriteOCILayout(bundle, layout)
	if err != nil {
		t.Fatalf("WriteOCILayout: %v", err)
	}
	index := readJSON[ociIndex](t, filepath.Join(layout, "index.json"))
	if len(index.Manifests) != 1 || index.Manifests[0].Digest != manifestDigest || index.Manifests[0].ArtifactType != ArtifactType {
		t.Fatalf("index.json = %+v, want one manifest %s of artifact type %s", index, manifestDigest, ArtifactType)
	}
	manifest := readJSON[ociManifest](t, blobPath(layout, manifestDigest))
	if manifest.ArtifactType != ArtifactType || manifest.Config.MediaType != ConfigMediaType || len(manifest.Layers) != len(wantLayers) {
		t.Fatalf("manifest = %+v", manifest)
	}

	files := map[string][]byte{}
	for _, l := range manifest.Layers {
		data := readFile(t, blobPath(layout, l.Digest))
		sum := sha256.Sum256(data)
		if got := "sha256:" + hex.EncodeToString(sum[:]); got != l.Digest || int64(len(data)) != l.Size {
			t.Fatalf("layer %s: blob digest %s size %d, descriptor %s size %d", l.Annotations[annotationTitle], got, len(data), l.Digest, l.Size)
		}
		files[l.Annotations[annotationTitle]] = data
	}
	if !bytes.Equal(files["theme.yaml"], readFile(t, filepath.Join(dir, "theme.yaml"))) {
		t.Fatalf("theme.yaml layer differs from the file it was built from")
	}

	res, probs := themepkg.Validate(files)
	if len(probs) != 0 {
		t.Fatalf("layout re-validates with problems: %v", probs)
	}
	wantDark, err := themepkg.Derive("#38BDF8", "#0B1220")
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if !tokensEqual(res.Manifest.Tokens.Dark, wantDark) {
		t.Fatalf("tokens changed across the round trip")
	}
	if string(files["theme.css"]) != ".card { border-radius: 12px; }" {
		t.Fatalf("theme.css changed across the round trip: %q", files["theme.css"])
	}
}

// TestRenderedManifestDecodesToSameTokens checks the scaffold writer: a manifest
// rendered by renderManifest decodes back to the tokens it was built from.
func TestRenderedManifestDecodesToSameTokens(t *testing.T) {
	in := themepkg.Manifest{
		APIVersion:  themepkg.APIVersion,
		Name:        "round",
		Version:     "2.0.0-rc.1",
		DisplayName: "Round",
		Author:      "Ann: the \"Tester\"",
		Description: "Quotes, colons # and hashes survive",
		Tags:        []string{"dark", "blue"},
		License:     "MIT",
		Tokens:      themepkg.ModeTokens{Dark: mustPreset(t, PresetLegacy, "dark")},
	}
	out, probs := themepkg.DecodeManifest(renderManifest(in))
	if len(probs) != 0 {
		t.Fatalf("rendered manifest has problems: %v", probs)
	}
	if out.Author != in.Author || out.Description != in.Description || out.Version != in.Version {
		t.Fatalf("decoded = %+v", out)
	}
	if !tokensEqual(out.Tokens.Dark, in.Tokens.Dark) || out.Tokens.Light != nil {
		t.Fatalf("tokens did not round trip")
	}
}

// mustPreset returns a built-in preset mode or fails the test.
func mustPreset(t *testing.T, preset, mode string) themepkg.Tokens {
	t.Helper()
	tokens, err := presetFor(preset, mode)
	if err != nil {
		t.Fatalf("presetFor: %v", err)
	}
	return tokens
}

// TestBuildBundleRefusesInvalidPackage keeps invalid packages away from layouts
// and registries: a package without previews must fail with ErrInvalid.
func TestBuildBundleRefusesInvalidPackage(t *testing.T) {
	root := t.TempDir()
	exportPath := filepath.Join(root, "export.json")
	writeFile(t, exportPath, []byte(sampleExport))
	dir := filepath.Join(root, "bare")
	if _, err := FromExport(exportPath, dir, "bare", ""); err != nil {
		t.Fatalf("FromExport: %v", err)
	}
	pkg, err := Inspect(dir)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if pkg.Valid() {
		t.Fatalf("a package without author, license or previews reported valid")
	}
	if _, err := BuildBundle(pkg); !errors.Is(err, ErrInvalid) {
		t.Fatalf("BuildBundle err = %v, want ErrInvalid", err)
	}
}

// TestWriteOCILayoutExtendsExistingLayout checks that a second version can share
// an OCI layout folder: both manifests stay in index.json.
func TestWriteOCILayoutExtendsExistingLayout(t *testing.T) {
	root := t.TempDir()
	dir := buildValidPackage(t, root)
	pkg, err := Inspect(dir)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	bundle, err := BuildBundle(pkg)
	if err != nil {
		t.Fatalf("BuildBundle: %v", err)
	}
	layout := filepath.Join(root, "shared")
	if _, err := WriteOCILayout(bundle, layout); err != nil {
		t.Fatalf("first WriteOCILayout: %v", err)
	}
	bundle.Version = "1.2.1"
	if _, err := WriteOCILayout(bundle, layout); err != nil {
		t.Fatalf("second WriteOCILayout: %v", err)
	}
	index := readJSON[ociIndex](t, filepath.Join(layout, "index.json"))
	refs := map[string]bool{}
	for _, m := range index.Manifests {
		refs[m.Annotations[annotationRefName]] = true
	}
	if len(index.Manifests) != 2 || !refs[bundle.Name+"-1.2.0"] || !refs[bundle.Name+"-1.2.1"] {
		t.Fatalf("index.json refs = %v, want both %s-1.2.0 and %s-1.2.1", refs, bundle.Name, bundle.Name)
	}
	// Writing the same version again replaces its entry instead of adding one.
	if _, err := WriteOCILayout(bundle, layout); err != nil {
		t.Fatalf("repeat WriteOCILayout: %v", err)
	}
	index = readJSON[ociIndex](t, filepath.Join(layout, "index.json"))
	if len(index.Manifests) != 2 {
		t.Fatalf("repeat write left %d manifests, want 2", len(index.Manifests))
	}
}
