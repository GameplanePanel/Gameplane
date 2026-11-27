package themes

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"oras.land/oras-go/v2/registry/remote/auth"

	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/bundlefetch/oci"
)

// Theme source credentials (spec 043, OD-5, settled 2026-11-27: "Secret name,
// like modules"). An admin creates the credential Secret in the credentials
// namespace and names it on the source, the way a ModuleSource names its
// pullSecretRef or secretRef. The panel never writes, changes or deletes that
// Secret: the source row keeps only its name (credential_ref) and the indexer
// reads it through kube.Client.Secrets (a Kubernetes Secret in combined mode, a
// panel-encrypted management object in standalone mode) for each pull. The
// Secret's values are never returned by the API and never written to a log, an
// error text or the audit target.
//
// The Secret holds, as for module sources:
//   - oci: a docker config JSON under .dockerconfigjson (the
//     kubernetes.io/dockerconfigjson shape);
//   - git, http: "token", or "username" and "password".

const (
	// gitHTTPUsername is the user name sent with a token for git sources. Forges
	// accept a token as the password with any user name.
	gitHTTPUsername = "gameplane"
)

// errCredentialEmpty reports a credential Secret that holds no data.
var errCredentialEmpty = errors.New("theme source credential Secret has no data")

// errNoCredentialStore is returned when a source names a credential Secret but
// the panel has no credential store (no Kubernetes client and no management
// store).
var errNoCredentialStore = &SourceError{Msg: "theme source credentials cannot be read on this panel"}

// dockerConfig is the docker config JSON an oci source credential carries. It is
// the same shape as a module source pull secret (kubernetes.io/dockerconfigjson).
type dockerConfig struct {
	Auths map[string]dockerAuth `json:"auths"`
}

// dockerAuth is one registry entry of a docker config.
type dockerAuth struct {
	Username      string `json:"username,omitempty"`
	Password      string `json:"password,omitempty"`
	Auth          string `json:"auth,omitempty"` // base64("user:pass")
	IdentityToken string `json:"identitytoken,omitempty"`
}

// checkCredentialSecret reads the credentialSecret field of a request. It
// returns set=false when the field is absent (none on create, keep on update),
// and set=true with a nil name for a JSON null or an empty string (clear on
// update). A name must be a Kubernetes object name (DNS-1123 subdomain), and a
// local source takes none.
func checkCredentialSecret(typ string, raw json.RawMessage) (name *string, set bool, err error) {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 {
		return nil, false, nil
	}
	if string(t) == "null" {
		return nil, true, nil
	}
	var v string
	if err := json.Unmarshal(t, &v); err != nil {
		return nil, false, sourceErrf("credentialSecret must be a Secret name or null")
	}
	if v == "" {
		return nil, true, nil
	}
	if typ == "local" {
		return nil, false, sourceErrf("local theme sources take no credential Secret")
	}
	if msgs := validation.IsDNS1123Subdomain(v); len(msgs) > 0 {
		return nil, false, sourceErrf("credentialSecret must be a Kubernetes Secret name: %s", strings.Join(msgs, "; "))
	}
	return &v, true, nil
}

// secretStore returns the credential store, or errNoCredentialStore when the
// panel has none.
func (s *Service) secretStore() (kube.SecretStore, error) {
	if s.kube == nil {
		return nil, errNoCredentialStore
	}
	st := s.kube.Secrets(s.credentialsNS)
	if st == nil {
		return nil, errNoCredentialStore
	}
	return st, nil
}

// readCredential returns the Secret data of the credential of src. It returns
// nil when the source names no Secret. A named Secret that cannot be read, holds
// no data, or lacks the keys its source type needs is an error; the error names
// the Secret but never its values.
func (s *Service) readCredential(ctx context.Context, src db.ThemeSource) (map[string][]byte, error) {
	if src.CredentialRef == nil || *src.CredentialRef == "" {
		return nil, nil
	}
	st, err := s.secretStore()
	if err != nil {
		return nil, err
	}
	name := *src.CredentialRef
	existing, err := st.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("read credential Secret %s of theme source %q: %w", name, src.Name, err)
	}
	if len(existing.Data) == 0 {
		return nil, fmt.Errorf("credential Secret %s: %w", name, errCredentialEmpty)
	}
	switch src.Type {
	case "oci":
		if len(existing.Data[corev1.DockerConfigJsonKey]) == 0 {
			return nil, fmt.Errorf("credential Secret %s has no %s key", name, corev1.DockerConfigJsonKey)
		}
	case "git", "http":
		hasToken := len(existing.Data["token"]) > 0
		hasBasic := len(existing.Data["username"]) > 0 && len(existing.Data["password"]) > 0
		if !hasToken && !hasBasic {
			return nil, fmt.Errorf("credential Secret %s needs a token key, or username and password keys", name)
		}
	}
	return existing.Data, nil
}

// ociHost returns the registry host of an oci source URL (registry/repository).
func ociHost(url string) string {
	host, _, _ := strings.Cut(url, "/")
	return host
}

// registryKey normalises a docker config auths key or an oci registry host to a
// bare host[:port]: the scheme and any path are dropped, the case is folded, and
// the Docker Hub index host maps to docker.io, so "https://index.docker.io/v1/"
// and "docker.io" match the same registry.
func registryKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+len("://"):]
	}
	host, _, _ := strings.Cut(s, "/")
	if host == "index.docker.io" {
		return "docker.io"
	}
	return host
}

// lookupAuth finds the auths entry for a registry host. Keys are compared after
// registryKey normalisation; when several keys normalise to the same host, the
// lexically first key wins so the choice is deterministic.
func lookupAuth(auths map[string]dockerAuth, host string) (dockerAuth, bool) {
	want := registryKey(host)
	keys := make([]string, 0, len(auths))
	for k := range auths {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if registryKey(k) == want {
			return auths[k], true
		}
	}
	return dockerAuth{}, false
}

// ociCredentialFunc resolves the docker config credential for the source's own
// registry. Any other registry gets anonymous access, so the credential never
// follows a redirect to another host.
func ociCredentialFunc(data map[string][]byte, host string) (oci.CredentialFunc, error) {
	raw, ok := data[corev1.DockerConfigJsonKey]
	if !ok {
		return nil, errors.New("theme source credential has no docker config")
	}
	var cfg dockerConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, errors.New("theme source credential is not a docker config")
	}
	entry, ok := lookupAuth(cfg.Auths, host)
	if !ok {
		return nil, fmt.Errorf("theme source credential has no entry for registry %s", host)
	}
	cred, err := entry.credential()
	if err != nil {
		return nil, errors.New("theme source credential auth entry is not valid")
	}
	return func(_ context.Context, registry string) (auth.Credential, error) {
		if registryKey(registry) != registryKey(host) {
			return auth.EmptyCredential, nil
		}
		return cred, nil
	}, nil
}

// credential converts one docker config entry to the oras credential.
func (a dockerAuth) credential() (auth.Credential, error) {
	cred := auth.Credential{Username: a.Username, Password: a.Password, RefreshToken: a.IdentityToken}
	if a.Auth != "" && (cred.Username == "" || cred.Password == "") {
		raw, err := base64.StdEncoding.DecodeString(a.Auth)
		if err != nil {
			return auth.EmptyCredential, err
		}
		user, pass, ok := strings.Cut(string(raw), ":")
		if !ok {
			return auth.EmptyCredential, errors.New("auth is not user:password")
		}
		cred.Username, cred.Password = user, pass
	}
	return cred, nil
}

// gitAuth turns a git source credential into a go-git auth method. It returns nil
// (anonymous) when there is no credential.
func gitAuth(data map[string][]byte) transport.AuthMethod {
	if len(data) == 0 {
		return nil
	}
	if tok := data["token"]; len(tok) > 0 {
		return &githttp.BasicAuth{Username: gitHTTPUsername, Password: string(tok)}
	}
	return &githttp.BasicAuth{Username: string(data["username"]), Password: string(data["password"])}
}

// httpAuthHeader turns an http source credential into an Authorization header
// value. It returns "" (anonymous) when there is no credential.
func httpAuthHeader(data map[string][]byte) string {
	if len(data) == 0 {
		return ""
	}
	if tok := data["token"]; len(tok) > 0 {
		return "Bearer " + string(tok)
	}
	req := &http.Request{Header: http.Header{}}
	req.SetBasicAuth(string(data["username"]), string(data["password"]))
	return req.Header.Get("Authorization")
}
