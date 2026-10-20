package modsrc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
	"github.com/GameplanePanel/gameplane/operator/internal/oci"
)

// newUpload builds a Fetcher over uploaded module bundles: ConfigMaps
// in the operator namespace labeled gameplane.local/module-upload=true,
// each holding one bundle's files in data/binaryData. The API's upload
// endpoint writes these, but a hand-applied ConfigMap indexes exactly
// the same way — the operator stays authoritative.
func newUpload(c client.Client, namespace string, allow []string) Fetcher {
	return &uploadFetcher{c: c, namespace: namespace, allow: allow}
}

type uploadFetcher struct {
	c         client.Client
	namespace string
	allow     []string
}

func (f *uploadFetcher) Index(ctx context.Context) ([]gameplanev1alpha1.ModuleEntry, []string, error) {
	bundles, warnings, err := f.scan(ctx)
	if err != nil {
		return nil, nil, err
	}
	entries := make([]gameplanev1alpha1.ModuleEntry, 0, len(bundles))
	for _, b := range bundles {
		meta := b.bundle.Metadata
		entries = append(entries, gameplanev1alpha1.ModuleEntry{
			Name:          meta.Name,
			DisplayName:   meta.DisplayName,
			Summary:       meta.Summary,
			Game:          meta.Game,
			Categories:    meta.Categories,
			Icon:          meta.Icon,
			Reference:     "upload:" + b.configMap,
			Versions:      []string{meta.Version},
			LatestVersion: meta.Version,
			Digest:        b.bundle.Digest,
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries, warnings, nil
}

func (f *uploadFetcher) Pull(ctx context.Context, name, version string) (*Bundle, error) {
	bundles, _, err := f.scan(ctx)
	if err != nil {
		return nil, err
	}
	for _, b := range bundles {
		if b.bundle.Metadata.Name != name {
			continue
		}
		if version != "" && b.bundle.Metadata.Version != version {
			return nil, fmt.Errorf("module %q is at version %q, requested %q (bundle re-uploaded?)",
				name, b.bundle.Metadata.Version, version)
		}
		return b.bundle, nil
	}
	return nil, fmt.Errorf("no uploaded bundle for module %q", name)
}

type uploadedBundle struct {
	configMap string
	bundle    *Bundle
}

func (f *uploadFetcher) scan(ctx context.Context) ([]uploadedBundle, []string, error) {
	var cms corev1.ConfigMapList
	if err := f.c.List(ctx, &cms,
		client.InNamespace(f.namespace),
		client.MatchingLabels{gameplanev1alpha1.LabelModuleUpload: "true"},
	); err != nil {
		return nil, nil, fmt.Errorf("list upload ConfigMaps: %w", err)
	}
	sort.Slice(cms.Items, func(i, j int) bool { return cms.Items[i].Name < cms.Items[j].Name })

	var bundles []uploadedBundle
	var warnings []string
	seen := map[string]string{} // module name → configmap
	for i := range cms.Items {
		cm := &cms.Items[i]
		files := make(map[string][]byte, len(cm.BinaryData)+len(cm.Data))
		for name, data := range cm.BinaryData {
			files[name] = data
		}
		for name, s := range cm.Data {
			if _, dup := files[name]; !dup {
				files[name] = []byte(s)
			}
		}
		bundle, err := FromFiles(digestFiles(files), uploadBundleFiles(files))
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("upload:%s: %v", cm.Name, err))
			continue
		}
		name := bundle.Metadata.Name
		if !allowed(name, f.allow) {
			continue
		}
		if prev, dup := seen[name]; dup {
			warnings = append(warnings, fmt.Sprintf("upload:%s: duplicate module name %q (already provided by upload:%s)",
				cm.Name, name, prev))
			continue
		}
		seen[name] = cm.Name
		bundles = append(bundles, uploadedBundle{configMap: cm.Name, bundle: bundle})
	}
	return bundles, warnings, nil
}

// uploadLocaleKeyPrefix and uploadLocaleKeySuffix frame the ConfigMap key of
// an uploaded translation file: "locales.<tag>.json". ConfigMap keys cannot
// contain "/", so this stands for the bundle path "locales/<tag>.json".
const (
	uploadLocaleKeyPrefix = "locales."
	uploadLocaleKeySuffix = ".json"
)

// uploadBundleFiles returns files with the locales.<tag>.json ConfigMap keys
// renamed to the canonical locales/<tag>.json names FromFiles reads. A file over
// oci.MaxLocaleLayerSize is replaced by oci.OversizedLocale so the reconciler
// reports it. The input map is not modified (the digest covers the original).
func uploadBundleFiles(files map[string][]byte) map[string][]byte {
	out := make(map[string][]byte, len(files))
	for name, data := range files {
		if strings.HasPrefix(name, uploadLocaleKeyPrefix) && strings.HasSuffix(name, uploadLocaleKeySuffix) &&
			len(name) > len(uploadLocaleKeyPrefix)+len(uploadLocaleKeySuffix) {
			tag := name[len(uploadLocaleKeyPrefix) : len(name)-len(uploadLocaleKeySuffix)]
			if len(data) > oci.MaxLocaleLayerSize {
				data = oci.OversizedLocale
			}
			out[oci.LocaleLayerPrefix+tag+oci.LocaleLayerSuffix] = data
			continue
		}
		out[name] = data
	}
	return out
}

// digestFiles is the map-shaped sibling of contentDigest: a
// deterministic hash over sorted filename+content pairs.
func digestFiles(files map[string][]byte) string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, name := range names {
		_, _ = io.WriteString(h, "/"+name)
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(files[name])
		_, _ = h.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}
