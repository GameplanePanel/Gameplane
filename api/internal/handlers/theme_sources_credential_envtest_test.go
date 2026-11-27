package handlers

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/themes"
	"github.com/GameplanePanel/gameplane/bundlefetch/oci"
	"github.com/GameplanePanel/gameplane/bundlefetch/source"
	"github.com/GameplanePanel/gameplane/bundlefetch/verify"
)

// This file covers the credential path of the admin theme source routes (spec
// 043, OD-5, settled "Secret name, like modules"): the admin creates a Secret in
// the credentials namespace and names it on the source (credentialSecret), the
// source row keeps only that name, the indexer reads the Secret for each pull,
// no response or error carries the Secret's values, and the panel never creates,
// changes or deletes the Secret.

// themeCredNS is the credentials namespace the fake Kubernetes client uses.
const themeCredNS = "gameplane-test"

// themeCredSecretValue is the secret part of the docker config the tests store.
// Responses and errors must never contain it.
const themeCredSecretValue = "hunter2-secret"

// themeCredDockerConfig is a docker config JSON credential for the test registry.
const themeCredDockerConfig = `{"auths":{"registry.test":{"username":"robot","password":"` + themeCredSecretValue + `"}}}`

// themeCredEnv is a theme catalog env with a Kubernetes fake for the credential
// Secrets. It records the last registry credential function the indexer built.
type themeCredEnv struct {
	*themeCatEnv
	cs *fake.Clientset

	mu    sync.Mutex
	creds oci.CredentialFunc
}

// newThemeCredEnv is newThemeCatEnv with a Kubernetes fake that holds the
// credential Secrets.
func newThemeCredEnv(t *testing.T) *themeCredEnv {
	t.Helper()
	env := &themeCredEnv{themeCatEnv: newThemeCatEnv(t), cs: fake.NewClientset()}
	env.svc = themes.New(themes.Deps{
		Store:         env.store,
		Kube:          &kube.Client{Typed: env.cs},
		CredentialsNS: themeCredNS,
		OCI: func(creds oci.CredentialFunc, _ bool) source.OCIClient {
			env.mu.Lock()
			env.creds = creds
			env.mu.Unlock()
			return env.oci
		},
		Verifier: func(context.Context, string, bool) (verify.Verifier, error) {
			return themeCatVerifier{}, nil
		},
	})
	return env
}

// lastCreds returns the registry credential function of the last index.
func (e *themeCredEnv) lastCreds() oci.CredentialFunc {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.creds
}

// themeCredName returns a source name unique to the test.
func themeCredName() string {
	return fmt.Sprintf("cred%d", themeCatSeq.Add(1))
}

// themeCredMakeSecret creates the admin's credential Secret name with data, as
// an admin would before naming it on a source.
func themeCredMakeSecret(t *testing.T, cs *fake.Clientset, name string, data map[string][]byte) {
	t.Helper()
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: themeCredNS},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data:       data,
	}
	if _, err := cs.CoreV1().Secrets(themeCredNS).Create(context.Background(), sec, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create secret %s: %v", name, err)
	}
}

// themeCredDockerData is the Secret data of a docker config credential.
func themeCredDockerData() map[string][]byte {
	return map[string][]byte{corev1.DockerConfigJsonKey: []byte(themeCredDockerConfig)}
}

// themeCredAssertNoPanelWrites fails when the service created, changed or
// deleted any Secret. The fake records every call; the tests' own creates are
// made before the service runs and are counted in allowedCreates.
func themeCredAssertNoPanelWrites(t *testing.T, cs *fake.Clientset, allowedCreates int) {
	t.Helper()
	creates := 0
	for _, a := range cs.Actions() {
		if a.GetResource().Resource != "secrets" {
			continue
		}
		switch a.GetVerb() {
		case "create":
			creates++
		case "update", "patch", "delete":
			t.Fatalf("the panel wrote a credential Secret: %s %v", a.GetVerb(), a)
		}
	}
	if creates != allowedCreates {
		t.Fatalf("secret creates = %d, want only the test's own %d", creates, allowedCreates)
	}
}

// themeCredAssertHidden fails when a response body carries the stored secret
// value.
func themeCredAssertHidden(t *testing.T, body string) {
	t.Helper()
	if strings.Contains(body, themeCredSecretValue) {
		t.Fatalf("response carries the credential value: %s", body)
	}
}

// themeCredStoredRef returns the credential_ref of a source row.
func themeCredStoredRef(t *testing.T, env *themeCredEnv, name string) *string {
	t.Helper()
	src, err := env.store.GetThemeSource(context.Background(), name)
	if err != nil {
		t.Fatalf("read source %s: %v", name, err)
	}
	return src.CredentialRef
}

func TestThemeSourceCredentialSecretNamedAndRead(t *testing.T) {
	env := newThemeCredEnv(t)
	h := env.router("admin")
	name := themeCredName()
	secret := name + "-pull"
	themeCredMakeSecret(t, env.cs, secret, themeCredDockerData())
	themeSrcPublish(t, env.themeCatEnv, name)

	body := themeSrcBody(name, "oci", themeSrcSpec(name), `,"credentialSecret":"`+secret+`"`)
	rr := themeInstDo(t, h, http.MethodPost, "/themes/sources", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create with a credential Secret: status %d body %s", rr.Code, rr.Body.String())
	}
	themeCredAssertHidden(t, rr.Body.String())
	env.svc.WaitIdle()

	if ref := themeCredStoredRef(t, env, name); ref == nil || *ref != secret {
		t.Fatalf("credential_ref = %v, want %s", ref, secret)
	}
	listed := themeSrcFind(t, themeSrcItems(t, h), name)
	if listed.CredentialSecret == nil || *listed.CredentialSecret != secret {
		t.Fatalf("listed credentialSecret = %v, want %s", listed.CredentialSecret, secret)
	}
	if listed.LastError != nil {
		t.Fatalf("index with a valid credential Secret failed: %s", *listed.LastError)
	}
	creds := env.lastCreds()
	if creds == nil {
		t.Fatalf("the indexer built no registry credential from the Secret")
	}
	cred, err := creds(context.Background(), "registry.test")
	if err != nil || cred.Username != "robot" || cred.Password != themeCredSecretValue {
		t.Fatalf("registry credential = %+v, %v; want the Secret's robot login", cred, err)
	}
	if rr := themeInstDo(t, h, http.MethodGet, "/themes/sources", ""); strings.Contains(rr.Body.String(), themeCredSecretValue) {
		t.Fatalf("list carries the credential value")
	}
	themeCredAssertNoPanelWrites(t, env.cs, 1)
}

func TestThemeSourceCredentialSecretKeepReplaceClear(t *testing.T) {
	env := newThemeCredEnv(t)
	defer env.svc.WaitIdle()
	h := env.router("admin")
	name := themeCredName()
	first, second := name+"-a", name+"-b"
	themeCredMakeSecret(t, env.cs, first, themeCredDockerData())
	themeCredMakeSecret(t, env.cs, second, themeCredDockerData())
	create := themeSrcBody(name, "oci", themeSrcSpec(name), `,"credentialSecret":"`+first+`"`)
	if rr := themeInstDo(t, h, http.MethodPost, "/themes/sources", create); rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d body %s", rr.Code, rr.Body.String())
	}
	put := func(spec, extra string) {
		t.Helper()
		rr := themeInstDo(t, h, http.MethodPut, "/themes/sources/"+name, themeSrcBody(name, "oci", spec, extra))
		if rr.Code != http.StatusOK {
			t.Fatalf("put %q: status %d body %s", extra, rr.Code, rr.Body.String())
		}
		themeCredAssertHidden(t, rr.Body.String())
	}
	wantRef := func(step string, want *string) {
		t.Helper()
		got := themeCredStoredRef(t, env, name)
		if (got == nil) != (want == nil) || (got != nil && *got != *want) {
			t.Fatalf("%s: credential_ref = %v, want %v", step, got, want)
		}
	}

	// An omitted credentialSecret keeps the stored name.
	put(themeSrcSpec(name), "")
	wantRef("omitted", &first)

	// Moving the location keeps the name, as on a ModuleSource.
	put(themeSrcSpec(name+"-moved"), "")
	wantRef("moved location", &first)

	// Another name replaces it.
	put(themeSrcSpec(name), `,"credentialSecret":"`+second+`"`)
	wantRef("replaced", &second)

	// A JSON null clears it; so does an empty string.
	put(themeSrcSpec(name), `,"credentialSecret":null`)
	wantRef("null", nil)
	put(themeSrcSpec(name), `,"credentialSecret":"`+first+`"`)
	put(themeSrcSpec(name), `,"credentialSecret":""`)
	wantRef("empty string", nil)

	// Deleting the source leaves the admin's Secrets in place.
	if rr := themeInstDo(t, h, http.MethodDelete, "/themes/sources/"+name, ""); rr.Code != http.StatusNoContent {
		t.Fatalf("delete: status %d body %s", rr.Code, rr.Body.String())
	}
	for _, s := range []string{first, second} {
		if _, err := env.cs.CoreV1().Secrets(themeCredNS).Get(context.Background(), s, metav1.GetOptions{}); err != nil {
			t.Fatalf("secret %s after source delete: %v", s, err)
		}
	}
	themeCredAssertNoPanelWrites(t, env.cs, 2)
}

func TestThemeSourceCredentialSecretProblemsFailTheSource(t *testing.T) {
	env := newThemeCredEnv(t)
	h := env.router("admin")
	noKey := themeCredName() + "-nokey"
	themeCredMakeSecret(t, env.cs, noKey, map[string][]byte{"password": []byte(themeCredSecretValue)})
	cases := []struct {
		name   string
		secret string
		want   string
	}{
		{"missing secret", "no-such-secret", "no-such-secret"},
		{"no docker config key", noKey, corev1.DockerConfigJsonKey},
	}
	for _, c := range cases {
		name := themeCredName()
		themeSrcPublish(t, env.themeCatEnv, name)
		body := themeSrcBody(name, "oci", themeSrcSpec(name), `,"credentialSecret":"`+c.secret+`"`)
		if rr := themeInstDo(t, h, http.MethodPost, "/themes/sources", body); rr.Code != http.StatusCreated {
			t.Fatalf("%s: create: status %d body %s", c.name, rr.Code, rr.Body.String())
		}
		env.svc.WaitIdle()
		listed := themeSrcFind(t, themeSrcItems(t, h), name)
		if listed.LastError == nil || !strings.Contains(*listed.LastError, c.want) {
			t.Errorf("%s: lastError = %v, want it to name %q", c.name, listed.LastError, c.want)
		}
		if listed.LastError != nil {
			themeCredAssertHidden(t, *listed.LastError)
		}
	}
	themeCredAssertNoPanelWrites(t, env.cs, 1)
}

func TestThemeSourceCredentialSecretRefusals(t *testing.T) {
	env := newThemeCredEnv(t)
	defer env.svc.WaitIdle()
	h := env.router("admin")
	cases := []struct {
		name  string
		typ   string
		spec  string
		value string
	}{
		{"not a Kubernetes name", "oci", "", `"Not_A_Name"`},
		{"not a string", "oci", "", `{"name":"x"}`},
		{"local source", "local", `{"local":{"path":"themes"}}`, `"theme-creds"`},
	}
	for _, c := range cases {
		name := themeCredName()
		spec := c.spec
		if spec == "" {
			spec = themeSrcSpec(name)
		}
		body := themeSrcBody(name, c.typ, spec, `,"credentialSecret":`+c.value)
		rr := themeInstDo(t, h, http.MethodPost, "/themes/sources", body)
		if rr.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: status %d, want 422; body %s", c.name, rr.Code, rr.Body.String())
		}
		for _, it := range themeSrcItems(t, h) {
			if it.Name == name {
				t.Errorf("%s: refused source was stored", c.name)
			}
		}
	}
	themeCredAssertNoPanelWrites(t, env.cs, 0)
}

// TestThemeSourceCredentialSecretNeedsStore checks that a panel without a credential store refuses a
// credential Secret name instead of storing a reference it cannot read.
func TestThemeSourceCredentialSecretNeedsStore(t *testing.T) {
	env := newThemeCatEnv(t)
	defer env.svc.WaitIdle()
	h := env.router("admin")
	name := themeCredName()
	body := themeSrcBody(name, "oci", themeSrcSpec(name), `,"credentialSecret":"theme-creds"`)
	if rr := themeInstDo(t, h, http.MethodPost, "/themes/sources", body); rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("create without a credential store: status %d, want 422; body %s", rr.Code, rr.Body.String())
	}
}
