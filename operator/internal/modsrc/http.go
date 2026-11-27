package modsrc

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/GameplanePanel/gameplane/bundlefetch/source"
	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// newHTTP builds a Fetcher over an archive (.tar.gz/.tgz/.zip) served
// at an http(s) URL. The archive is downloaded fresh on each index and
// scanned for module directories like every filesystem source.
func newHTTP(ctx context.Context, c client.Client, namespace string, spec *gameplanev1alpha1.HTTPSourceSpec, allow []string) (Fetcher, error) {
	if err := checkHTTPURL(spec.URL, spec.Insecure); err != nil {
		return nil, err
	}
	authHeader, err := httpAuth(ctx, c, namespace, spec.SecretRef, spec.URL)
	if err != nil {
		return nil, err
	}
	return newFSFetcher(source.ArchiveLoader(spec.URL, authHeader), spec.URL, allow), nil
}

// checkHTTPURL enforces https (plain http only with insecure: true) and refuses
// the link-local range (see source.CheckHTTPURL).
func checkHTTPURL(raw string, insecure bool) error { return source.CheckHTTPURL(raw, insecure) }

// extractArchive unpacks a tar.gz or zip into an in-memory filesystem (see
// source.ExtractArchive).
func extractArchive(data []byte) (fs.FS, error) { return source.ExtractArchive(data) }

// httpAuth resolves the source's credential Secret into an
// Authorization header value: "token" → Bearer, else basic auth from
// "username"+"password".
func httpAuth(ctx context.Context, c client.Client, namespace string, secretRef *corev1.LocalObjectReference, sourceURL string) (string, error) {
	if secretRef == nil || secretRef.Name == "" {
		return "", nil
	}
	sec, err := readSourceCredential(ctx, c, namespace, secretRef.Name, "gameplane.local/http-source-url", sourceURL)
	if err != nil {
		return "", fmt.Errorf("get http credentials %s/%s: %w", namespace, secretRef.Name, err)
	}
	if token, ok := sec.Data["token"]; ok && len(token) > 0 {
		return "Bearer " + string(token), nil
	}
	user, uok := sec.Data["username"]
	pass, pok := sec.Data["password"]
	if uok && pok {
		req := &http.Request{Header: http.Header{}}
		req.SetBasicAuth(string(user), string(pass))
		return req.Header.Get("Authorization"), nil
	}
	return "", fmt.Errorf("secret %s needs \"token\" or \"username\"+\"password\"", secretRef.Name)
}
