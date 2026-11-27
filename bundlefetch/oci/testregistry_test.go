package oci

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func digestFor(data []byte) digest.Digest {
	sum := sha256.Sum256(data)
	return digest.Digest("sha256:" + hex.EncodeToString(sum[:]))
}

func digestFromString(s string) digest.Digest { return digest.Digest(s) }

// fakeRegistry is a barely-conforming OCI distribution v2 registry
// backed by in-memory maps. Enough to exercise our oras-go client's
// Tags + FetchReference + Blobs.Fetch paths.
type fakeRegistry struct {
	t *testing.T

	mu        sync.Mutex
	manifests map[string]map[string][]byte // repo → tag/digest → manifest bytes
	mIndex    map[string]map[string]string // repo → tag → digest
	blobs     map[string][]byte            // digest → bytes
	// served replaces the body sent for a manifest or blob reference
	// (tag or digest). Headers still describe the stored content.
	served map[string][]byte
	server *httptest.Server
}

func newFakeRegistry(t *testing.T) *fakeRegistry {
	t.Helper()
	r := &fakeRegistry{
		t:         t,
		manifests: map[string]map[string][]byte{},
		mIndex:    map[string]map[string]string{},
		blobs:     map[string][]byte{},
		served:    map[string][]byte{},
	}
	r.server = httptest.NewServer(http.HandlerFunc(r.handle))
	t.Cleanup(r.server.Close)
	return r
}

func (r *fakeRegistry) host() string {
	return strings.TrimPrefix(r.server.URL, "http://")
}

func (r *fakeRegistry) putBlob(data []byte) string {
	sum := sha256.Sum256(data)
	d := "sha256:" + hex.EncodeToString(sum[:])
	r.mu.Lock()
	r.blobs[d] = data
	r.mu.Unlock()
	return d
}

// pushManifest adds a manifest under repo:tag and returns its digest.
func (r *fakeRegistry) pushManifest(repo, tag string, manifest []byte) string {
	d := r.putBlob(manifest)
	r.mu.Lock()
	if r.manifests[repo] == nil {
		r.manifests[repo] = map[string][]byte{}
		r.mIndex[repo] = map[string]string{}
	}
	r.manifests[repo][tag] = manifest
	r.manifests[repo][d] = manifest
	r.mIndex[repo][tag] = d
	r.mu.Unlock()
	return d
}

// storedManifest returns the manifest bytes pushed under repo:tag.
func (r *fakeRegistry) storedManifest(repo, tag string) []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.manifests[repo][tag]
}

// serveBody makes GET requests for ref (a tag or digest) return body,
// while the response headers still describe the stored content.
func (r *fakeRegistry) serveBody(ref string, body []byte) {
	r.mu.Lock()
	r.served[ref] = body
	r.mu.Unlock()
}

func (r *fakeRegistry) handle(w http.ResponseWriter, req *http.Request) {
	path := req.URL.Path
	switch {
	case path == "/v2/" || path == "/v2":
		w.WriteHeader(http.StatusOK)
		return
	case strings.HasSuffix(path, "/tags/list"):
		repo := strings.TrimPrefix(strings.TrimSuffix(path, "/tags/list"), "/v2/")
		r.mu.Lock()
		tags := []string{}
		for t := range r.mIndex[repo] {
			tags = append(tags, t)
		}
		r.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"name": repo, "tags": tags})
		return
	}

	// /v2/<repo>/{manifests,blobs}/<reference>
	parts := strings.SplitN(strings.TrimPrefix(path, "/v2/"), "/", 64)
	if len(parts) < 3 {
		http.NotFound(w, req)
		return
	}
	ref := parts[len(parts)-1]
	kind := parts[len(parts)-2]
	repo := strings.Join(parts[:len(parts)-2], "/")

	r.mu.Lock()
	defer r.mu.Unlock()
	switch kind {
	case "manifests":
		var data []byte
		if m, ok := r.manifests[repo]; ok {
			data = m[ref]
		}
		if data == nil {
			http.NotFound(w, req)
			return
		}
		var parsed ocispec.Manifest
		_ = json.Unmarshal(data, &parsed)
		mt := parsed.MediaType
		if mt == "" {
			mt = ocispec.MediaTypeImageManifest
		}
		w.Header().Set("Content-Type", mt)
		sum := sha256.Sum256(data)
		w.Header().Set("Docker-Content-Digest", "sha256:"+hex.EncodeToString(sum[:]))
		if req.Method == http.MethodHead {
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
			return
		}
		if body, ok := r.served[ref]; ok {
			data = body
		}
		_, _ = w.Write(data)
	case "blobs":
		data, ok := r.blobs[ref]
		if !ok {
			http.NotFound(w, req)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
		if req.Method == http.MethodHead {
			return
		}
		if body, ok := r.served[ref]; ok {
			data = body
		}
		_, _ = w.Write(data)
	default:
		http.NotFound(w, req)
	}
}

// pushBundle is a convenience that constructs and pushes a Gameplane
// module bundle for tests.
func (r *fakeRegistry) pushBundle(repo, tag string, layers map[string][]byte) string {
	r.t.Helper()
	emptyConfig := []byte(`{}`)
	configDigest := r.putBlob(emptyConfig)

	manifest := ocispec.Manifest{
		MediaType:    ocispec.MediaTypeImageManifest,
		ArtifactType: ArtifactType,
		Config: ocispec.Descriptor{
			MediaType: MediaTypeConfig,
			Digest:    digestFor(emptyConfig),
			Size:      int64(len(emptyConfig)),
		},
	}
	_ = configDigest

	mediaTypeFor := map[string]string{
		LayerNameMetadata: MediaTypeMetadata,
		LayerNameTemplate: MediaTypeTemplate,
		LayerNameReadme:   MediaTypeReadme,
		LayerNameIcon:     MediaTypeIconPNG,
	}
	for name, data := range layers {
		d := r.putBlob(data)
		manifest.Layers = append(manifest.Layers, ocispec.Descriptor{
			MediaType:   mediaTypeFor[name],
			Digest:      digestFromString(d),
			Size:        int64(len(data)),
			Annotations: map[string]string{AnnotationTitle: name},
		})
	}
	mb, err := json.Marshal(manifest)
	if err != nil {
		r.t.Fatalf("marshal manifest: %v", err)
	}
	return r.pushManifest(repo, tag, mb)
}
