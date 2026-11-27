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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"oras.land/oras-go/v2/registry/remote/auth"

	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/bundlefetch/oci"
)

// Theme source credentials (spec 043, OD-5). A credential is stored as a labelled
// Secret in the credentials namespace, through kube.Client.Secrets: a Kubernetes
// Secret in combined mode, a panel-encrypted management object in standalone
// mode. The source row keeps only the Secret name (credential_ref). The value is
// never returned by the API and never written to a log, an error text or the
// audit target.

const (
	// ThemeSourceSecretLabel marks the credential Secrets of theme sources. The
	// service reads and deletes only Secrets that carry it.
	ThemeSourceSecretLabel = "gameplane.local/theme-source"

	// credentialSecretPrefix starts every credential Secret name. A source name
	// is at most 40 characters (maxSourceNameLen), so the prefix plus the name
	// fits the 63-character limit of a Kubernetes object name.
	credentialSecretPrefix = "gameplane-theme-source-"

	// managedByLabel and managedByValue match the labels the API sets on the
	// Secrets it creates for admins (handlers.ManagedByLabel).
	managedByLabel = "gameplane.local/managed-by"
	managedByValue = "gameplane-api"

	// gitHTTPUsername is the user name sent with a token for git sources. Forges
	// accept a token as the password with any user name.
	gitHTTPUsername = "gameplane"
)

// errCredentialNotManaged reports a Secret with the credential name that the
// theme service did not create. It is never read or deleted.
var errCredentialNotManaged = errors.New("theme source credential Secret is not managed by Gameplane")

// errCredentialEmpty reports a credential Secret that holds no data.
var errCredentialEmpty = errors.New("theme source credential Secret has no data")

// errNoCredentialStore is returned when credentials are needed but the panel has
// no credential store (no Kubernetes client and no management store).
var errNoCredentialStore = &SourceError{Msg: "theme source credentials cannot be stored on this panel"}

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

// tokenCredential is the credential of a git or http source: a token, or a user
// name and password.
type tokenCredential struct {
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Token    string `json:"token,omitempty"`
}

// hasCredentialInput reports whether a request carried a credential value. An
// absent field and a JSON null are both "no value".
func hasCredentialInput(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) > 0 && string(t) != "null"
}

// isCredentialNull reports whether a request sent an explicit JSON null, which
// clears the credential on update.
func isCredentialNull(raw json.RawMessage) bool {
	return string(bytes.TrimSpace(raw)) == "null"
}

// checkCredential validates a credential for a source type and returns the
// Secret data to store. Error texts are generic: they never echo the input,
// because a malformed credential may still contain a secret value.
func checkCredential(typ string, raw json.RawMessage) (map[string][]byte, error) {
	switch typ {
	case "oci":
		var cfg dockerConfig
		if err := decodeCredentialStrict(raw, &cfg); err != nil {
			return nil, sourceErrf("credential for an oci source must be docker config JSON with an auths object")
		}
		if len(cfg.Auths) == 0 {
			return nil, sourceErrf("credential needs an auths object with at least one registry")
		}
		for _, a := range cfg.Auths {
			if a.Username == "" && a.Password == "" && a.Auth == "" && a.IdentityToken == "" {
				return nil, sourceErrf("an auths entry has no username, password, auth or identitytoken")
			}
		}
		canonical, err := json.Marshal(cfg)
		if err != nil {
			return nil, fmt.Errorf("encode credential: %w", err)
		}
		return map[string][]byte{corev1.DockerConfigJsonKey: canonical}, nil
	case "git", "http":
		var tc tokenCredential
		if err := decodeCredentialStrict(raw, &tc); err != nil {
			return nil, sourceErrf("credential for a %s source must be {\"token\"} or {\"username\",\"password\"}", typ)
		}
		if tc.Token != "" {
			if tc.Username != "" || tc.Password != "" {
				return nil, sourceErrf("credential takes a token or a username and password, not both")
			}
			return map[string][]byte{"token": []byte(tc.Token)}, nil
		}
		if tc.Username == "" || tc.Password == "" {
			return nil, sourceErrf("credential needs a token or a username and password")
		}
		return map[string][]byte{"username": []byte(tc.Username), "password": []byte(tc.Password)}, nil
	case "local":
		return nil, sourceErrf("local theme sources take no credential")
	default:
		return nil, sourceErrf("unknown source type %q", typ)
	}
}

// decodeCredentialStrict decodes a credential object, refusing unknown fields
// and trailing data. Its error is discarded by callers: it is never shown.
func decodeCredentialStrict(raw json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimSpace(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("trailing data after credential")
	}
	return nil
}

// credentialSecretName returns the name of the Secret that holds the credential
// of the source named source.
func credentialSecretName(source string) string {
	return credentialSecretPrefix + source
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

// writeCredential creates or replaces the credential Secret of source with data
// and returns its name. An existing Secret that is not labelled as a theme source
// credential is refused, so this path cannot overwrite another Secret.
func (s *Service) writeCredential(ctx context.Context, source string, data map[string][]byte) (string, error) {
	st, err := s.secretStore()
	if err != nil {
		return "", err
	}
	name := credentialSecretName(source)
	labels := map[string]string{ThemeSourceSecretLabel: "true", managedByLabel: managedByValue}
	desired := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: s.credentialsNS, Labels: labels},
		Type:       corev1.SecretTypeOpaque,
		Data:       data,
	}
	_, err = st.Create(ctx, desired, metav1.CreateOptions{})
	if err == nil {
		return name, nil
	}
	if !apierrors.IsAlreadyExists(err) {
		return "", fmt.Errorf("store theme source credential: %w", err)
	}
	existing, err := st.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("read theme source credential: %w", err)
	}
	if existing.Labels[ThemeSourceSecretLabel] != "true" {
		// A foreign Secret with the same name is a conflict the admin can fix by
		// renaming the source, so it is a 422 refusal, not a server error.
		return "", sourceErrf("a Secret named %s already exists and is not a theme source credential", name)
	}
	if existing.Labels == nil {
		existing.Labels = map[string]string{}
	}
	for k, v := range labels {
		existing.Labels[k] = v
	}
	existing.Data = data
	existing.StringData = nil
	if _, err := st.Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
		return "", fmt.Errorf("update theme source credential: %w", err)
	}
	return name, nil
}

// deleteCredential removes the credential Secret of source. A missing Secret, or
// one that is not labelled as a theme source credential, is not an error.
func (s *Service) deleteCredential(ctx context.Context, source string) error {
	st, err := s.secretStore()
	if err != nil {
		return err
	}
	name := credentialSecretName(source)
	existing, err := st.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read theme source credential: %w", err)
	}
	if existing.Labels[ThemeSourceSecretLabel] != "true" {
		return nil
	}
	if err := st.Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete theme source credential: %w", err)
	}
	return nil
}

// readCredential returns the Secret data of the credential of src. It returns
// nil when the source has no credential. A referenced credential that cannot be
// read, or is not a theme source credential, is an error; the error names the
// Secret but never its value.
func (s *Service) readCredential(ctx context.Context, src db.ThemeSource) (map[string][]byte, error) {
	if src.CredentialRef == nil || *src.CredentialRef == "" {
		return nil, nil
	}
	st, err := s.secretStore()
	if err != nil {
		return nil, err
	}
	existing, err := st.Get(ctx, *src.CredentialRef, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("read credential of theme source %q: %w", src.Name, err)
	}
	if existing.Labels[ThemeSourceSecretLabel] != "true" {
		return nil, errCredentialNotManaged
	}
	if len(existing.Data) == 0 {
		return nil, errCredentialEmpty
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
		if registry != host {
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
