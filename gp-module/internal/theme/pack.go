package theme

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/GameplanePanel/gameplane/gp-module/internal/packager"
)

// OCI artifact, config and layer media types of a theme package
// (contracts/theme-package.md, "Files" and the OCI paragraph).
const (
	ArtifactType    = "application/vnd.gameplane.theme.v1+json"
	ConfigMediaType = "application/vnd.gameplane.theme.config.v1+json"
	MediaMetadata   = "application/vnd.gameplane.theme.metadata.v1+yaml"
	MediaCSS        = "text/css"
	MediaPNG        = "image/png"
	MediaWEBP       = "image/webp"

	ociManifestMediaType = "application/vnd.oci.image.manifest.v1+json"
	ociIndexMediaType    = "application/vnd.oci.image.index.v1+json"
	annotationTitle      = "org.opencontainers.image.title"
	annotationVersion    = "org.opencontainers.image.version"
	annotationRefName    = "org.opencontainers.image.ref.name"
)

// ErrInvalid is returned when a package is packaged or previewed before it
// passes validation.
var ErrInvalid = errors.New("theme package is invalid")

// Layer is one file of a package, stored as one OCI layer titled with its path.
type Layer struct {
	Title     string
	MediaType string
	Data      []byte
	Digest    string
	Size      int64
}

// Bundle is a validated package ready to be written or pushed: the config
// blob (always "{}") and one layer per file, theme.yaml first.
type Bundle struct {
	Name    string
	Version string
	Config  []byte
	Layers  []Layer
}

// BuildBundle turns a valid package into a bundle. It refuses a package with
// problems, so nothing invalid can reach a registry or an OCI layout.
func BuildBundle(p *Package) (*Bundle, error) {
	if !p.Valid() {
		return nil, fmt.Errorf("%w: %d problem(s), run `gp-module theme validate %s`", ErrInvalid, len(p.Problems), p.Dir)
	}
	m := p.Result.Manifest
	paths := orderedPaths(p.Files)
	b := &Bundle{Name: m.Name, Version: m.Version, Config: []byte("{}")}
	for _, path := range paths {
		mt, err := mediaTypeFor(path)
		if err != nil {
			return nil, err
		}
		data := p.Files[path]
		sum := sha256.Sum256(data)
		b.Layers = append(b.Layers, Layer{
			Title:     path,
			MediaType: mt,
			Data:      data,
			Digest:    "sha256:" + hex.EncodeToString(sum[:]),
			Size:      int64(len(data)),
		})
	}
	return b, nil
}

// orderedPaths lists the package files with theme.yaml first, theme.css next
// and the previews after, sorted, so the layout is reproducible.
func orderedPaths(files map[string][]byte) []string {
	var rest []string
	var out []string
	for path := range files {
		switch path {
		case "theme.yaml", "theme.css":
		default:
			rest = append(rest, path)
		}
	}
	sort.Strings(rest)
	if _, ok := files["theme.yaml"]; ok {
		out = append(out, "theme.yaml")
	}
	if _, ok := files["theme.css"]; ok {
		out = append(out, "theme.css")
	}
	return append(out, rest...)
}

// mediaTypeFor returns the OCI layer media type of one package file.
func mediaTypeFor(path string) (string, error) {
	switch {
	case path == "theme.yaml":
		return MediaMetadata, nil
	case path == "theme.css":
		return MediaCSS, nil
	case strings.HasSuffix(path, ".png"):
		return MediaPNG, nil
	case strings.HasSuffix(path, ".webp"):
		return MediaWEBP, nil
	}
	return "", fmt.Errorf("no media type for package file %q", path)
}

// ociDescriptor is an OCI content descriptor.
type ociDescriptor struct {
	MediaType    string            `json:"mediaType"`
	Digest       string            `json:"digest"`
	Size         int64             `json:"size"`
	ArtifactType string            `json:"artifactType,omitempty"`
	Annotations  map[string]string `json:"annotations,omitempty"`
}

// ociManifest is the OCI image manifest of a theme artifact.
type ociManifest struct {
	SchemaVersion int               `json:"schemaVersion"`
	MediaType     string            `json:"mediaType"`
	ArtifactType  string            `json:"artifactType"`
	Config        ociDescriptor     `json:"config"`
	Layers        []ociDescriptor   `json:"layers"`
	Annotations   map[string]string `json:"annotations,omitempty"`
}

// ociIndex is the index.json of an OCI image layout.
type ociIndex struct {
	SchemaVersion int             `json:"schemaVersion"`
	Manifests     []ociDescriptor `json:"manifests"`
}

// layoutRefName is the org.opencontainers.image.ref.name of a bundle's index
// entry. It keys the entry by theme name and version, so two themes with the
// same version can share one layout folder without overwriting each other.
func layoutRefName(b *Bundle) string {
	return b.Name + "-" + b.Version
}

// WriteOCILayout writes the bundle as an OCI image layout in dir (index.json,
// oci-layout and blobs/sha256). It returns the manifest digest. An existing
// layout is extended, so several themes and versions can share one folder.
func WriteOCILayout(b *Bundle, dir string) (string, error) {
	blobs := filepath.Join(dir, "blobs", "sha256")
	if err := os.MkdirAll(blobs, 0o750); err != nil {
		return "", fmt.Errorf("create layout: %w", err)
	}
	configDigest, configSize, err := writeBlob(blobs, b.Config)
	if err != nil {
		return "", err
	}
	manifest := ociManifest{
		SchemaVersion: 2,
		MediaType:     ociManifestMediaType,
		ArtifactType:  ArtifactType,
		Config:        ociDescriptor{MediaType: ConfigMediaType, Digest: configDigest, Size: configSize},
		Layers:        make([]ociDescriptor, 0, len(b.Layers)),
		Annotations:   map[string]string{annotationVersion: b.Version},
	}
	for _, l := range b.Layers {
		if _, _, err := writeBlob(blobs, l.Data); err != nil {
			return "", err
		}
		manifest.Layers = append(manifest.Layers, ociDescriptor{
			MediaType:   l.MediaType,
			Digest:      l.Digest,
			Size:        l.Size,
			Annotations: map[string]string{annotationTitle: l.Title},
		})
	}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode manifest: %w", err)
	}
	manifestDigest, manifestSize, err := writeBlob(blobs, manifestBytes)
	if err != nil {
		return "", err
	}
	index, err := readIndex(filepath.Join(dir, "index.json"))
	if err != nil {
		return "", err
	}
	// The entry for this theme and version is replaced; other themes and
	// versions stay, so several themes can share one --out folder.
	ref := layoutRefName(b)
	kept := index.Manifests[:0]
	for _, m := range index.Manifests {
		if m.Annotations[annotationRefName] != ref {
			kept = append(kept, m)
		}
	}
	index.Manifests = append(kept, ociDescriptor{
		MediaType:    ociManifestMediaType,
		Digest:       manifestDigest,
		Size:         manifestSize,
		ArtifactType: ArtifactType,
		Annotations:  map[string]string{annotationRefName: ref},
	})
	indexBytes, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode index: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.json"), indexBytes, 0o600); err != nil {
		return "", fmt.Errorf("write index.json: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "oci-layout"), []byte(`{"imageLayoutVersion":"1.0.0"}`), 0o600); err != nil {
		return "", fmt.Errorf("write oci-layout: %w", err)
	}
	return manifestDigest, nil
}

// readIndex loads an existing index.json, or returns an empty index when the
// layout is new.
func readIndex(path string) (ociIndex, error) {
	index := ociIndex{SchemaVersion: 2}
	data, err := os.ReadFile(filepath.Clean(path))
	if os.IsNotExist(err) {
		return index, nil
	}
	if err != nil {
		return index, fmt.Errorf("read index.json: %w", err)
	}
	if err := json.Unmarshal(data, &index); err != nil {
		return index, fmt.Errorf("parse index.json: %w", err)
	}
	index.SchemaVersion = 2
	return index, nil
}

// writeBlob stores data content-addressed under blobsDir and returns its digest
// and size.
func writeBlob(blobsDir string, data []byte) (string, int64, error) {
	sum := sha256.Sum256(data)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	path := filepath.Join(blobsDir, hex.EncodeToString(sum[:]))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", 0, fmt.Errorf("write blob %s: %w", digest, err)
	}
	return digest, int64(len(data)), nil
}

// PushOptions controls Push. PlainHTTP allows plain HTTP registry connections
// (for example the local dev registry), as `gp-module package --plain-http` does.
type PushOptions struct {
	PlainHTTP bool
}

// Push uploads the bundle to the registry prefix as <prefix>/<name>:<version>
// with ORAS, which reads the registry credentials from the Docker config, the
// same way the module packager does. It returns the pushed reference. The oras
// call goes through packager.RunOras, the exempt file for oras execution. Signing
// stays with `cosign sign`.
func Push(b *Bundle, dir, prefix string, opts PushOptions) (string, error) {
	if _, err := exec.LookPath("oras"); err != nil {
		return "", errors.New("oras binary not found in PATH; install ORAS (https://oras.land) or use --out to write an OCI layout")
	}
	tmp, err := os.MkdirTemp("", "gp-theme-config-")
	if err != nil {
		return "", fmt.Errorf("create config temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	configPath := filepath.Join(tmp, "config.json")
	if err := os.WriteFile(configPath, b.Config, 0o600); err != nil {
		return "", fmt.Errorf("write config: %w", err)
	}
	ref := fmt.Sprintf("%s/%s:%s", strings.TrimRight(prefix, "/"), b.Name, b.Version)
	args := []string{"push"}
	if opts.PlainHTTP {
		args = append(args, "--plain-http")
	}
	args = append(args, "--artifact-type", ArtifactType, "--config", configPath+":"+ConfigMediaType, ref)
	for _, l := range b.Layers {
		args = append(args, l.Title+":"+l.MediaType)
	}
	if err := packager.RunOras(context.Background(), dir, args...); err != nil {
		return "", fmt.Errorf("oras push failed: %w", err)
	}
	return ref, nil
}
