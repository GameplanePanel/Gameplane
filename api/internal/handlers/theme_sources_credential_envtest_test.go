package handlers

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/themes"
	"github.com/GameplanePanel/gameplane/bundlefetch/oci"
	"github.com/GameplanePanel/gameplane/bundlefetch/source"
	"github.com/GameplanePanel/gameplane/bundlefetch/verify"
)

// This file covers the credential path of the admin theme source routes (spec
// 043, OD-5): a credential is stored as a labelled Secret named after the source,
// the source row keeps only the Secret name, no response or error carries the
// value, and the Secret follows the source's lifecycle (kept on PUT without a
// credential, replaced, cleared with null, removed when the location moves or
// the source is deleted).

// themeCredNS is the credentials namespace the fake Kubernetes client uses.
const themeCredNS = "gameplane-test"

// themeCredSecretValue is the secret part of the docker config the tests store.
// Responses and errors must never contain it.
const themeCredSecretValue = "hunter2-secret"

// themeCredDockerConfig is a docker config JSON credential for the test registry.
const themeCredDockerConfig = `{"auths":{"registry.test":{"username":"robot","password":"` + themeCredSecretValue + `"}}}`

// newThemeCredEnv is newThemeCatEnv with a Kubernetes fake that holds the
// credential Secrets. It returns the fake so tests can read the Secrets.
func newThemeCredEnv(t *testing.T) (*themeCatEnv, *fake.Clientset) {
	t.Helper()
	env := newThemeCatEnv(t)
	cs := fake.NewClientset()
	env.svc = themes.New(themes.Deps{
		Store:         env.store,
		Kube:          &kube.Client{Typed: cs},
		CredentialsNS: themeCredNS,
		OCI:           func(oci.CredentialFunc, bool) source.OCIClient { return env.oci },
		Verifier: func(context.Context, string, bool) (verify.Verifier, error) {
			return themeCatVerifier{}, nil
		},
	})
	return env, cs
}

// themeCredName returns a source name unique to the test.
func themeCredName() string {
	return fmt.Sprintf("cred%d", themeCatSeq.Add(1))
}

// themeCredSecretName is the Secret name the service derives from a source name.
func themeCredSecretName(src string) string {
	return "gameplane-theme-source-" + src
}

// themeCredSecret returns the credential Secret of a source, and false when there
// is none.
func themeCredSecret(t *testing.T, cs *fake.Clientset, src string) (*corev1.Secret, bool) {
	t.Helper()
	sec, err := cs.CoreV1().Secrets(themeCredNS).Get(context.Background(), themeCredSecretName(src), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, false
	}
	if err != nil {
		t.Fatalf("get credential secret of %s: %v", src, err)
	}
	return sec, true
}

// themeCredAssertHidden fails when a response body carries the credential field
// name or the stored secret value.
func themeCredAssertHidden(t *testing.T, body string) {
	t.Helper()
	themeSrcAssertNoCredential(t, body)
	if strings.Contains(body, themeCredSecretValue) {
		t.Fatalf("response carries the credential value: %s", body)
	}
}

// themeCredStoredRef returns the credential_ref of a source row.
func themeCredStoredRef(t *testing.T, env *themeCatEnv, name string) *string {
	t.Helper()
	src, err := env.store.GetThemeSource(context.Background(), name)
	if err != nil {
		t.Fatalf("read source %s: %v", name, err)
	}
	return src.CredentialRef
}

func TestThemeSourceCredentialStoredAsLabelledSecret(t *testing.T) {
	env, cs := newThemeCredEnv(t)
	defer env.svc.WaitIdle()
	h := env.router("admin")
	name := themeCredName()
	body := themeSrcBody(name, "oci", themeSrcSpec(name), `,"credential":`+themeCredDockerConfig)

	rr := themeInstDo(t, h, http.MethodPost, "/themes/sources", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create with credential: status %d body %s", rr.Code, rr.Body.String())
	}
	themeCredAssertHidden(t, rr.Body.String())

	sec, ok := themeCredSecret(t, cs, name)
	if !ok {
		t.Fatalf("no credential secret for %s", name)
	}
	if sec.Labels[themes.ThemeSourceSecretLabel] != "true" {
		t.Fatalf("secret labels %v, want %s=true", sec.Labels, themes.ThemeSourceSecretLabel)
	}
	if !strings.Contains(string(sec.Data[corev1.DockerConfigJsonKey]), themeCredSecretValue) {
		t.Fatalf("secret does not hold the docker config credential")
	}
	ref := themeCredStoredRef(t, env, name)
	if ref == nil || *ref != themeCredSecretName(name) {
		t.Fatalf("credential_ref = %v, want %s", ref, themeCredSecretName(name))
	}

	items := themeSrcItems(t, h)
	if listed := themeSrcFind(t, items, name); listed.Type != "oci" {
		t.Fatalf("listed source: %+v", listed)
	}
	if rr := themeInstDo(t, h, http.MethodGet, "/themes/sources", ""); strings.Contains(rr.Body.String(), themeCredSecretValue) {
		t.Fatalf("list carries the credential value")
	}
}

func TestThemeSourceCredentialUpdateKeepReplaceClear(t *testing.T) {
	env, cs := newThemeCredEnv(t)
	defer env.svc.WaitIdle()
	h := env.router("admin")
	name := themeCredName()
	create := themeSrcBody(name, "oci", themeSrcSpec(name), `,"credential":`+themeCredDockerConfig)
	if rr := themeInstDo(t, h, http.MethodPost, "/themes/sources", create); rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d body %s", rr.Code, rr.Body.String())
	}
	put := func(extra string) {
		t.Helper()
		rr := themeInstDo(t, h, http.MethodPut, "/themes/sources/"+name, themeSrcBody(name, "oci", themeSrcSpec(name), extra))
		if rr.Code != http.StatusOK {
			t.Fatalf("put %q: status %d body %s", extra, rr.Code, rr.Body.String())
		}
		themeCredAssertHidden(t, rr.Body.String())
	}

	// Omitted credential keeps the stored one.
	put("")
	if _, ok := themeCredSecret(t, cs, name); !ok || themeCredStoredRef(t, env, name) == nil {
		t.Fatalf("PUT without a credential dropped the stored credential")
	}

	// A JSON null clears it and removes the Secret.
	put(`,"credential":null`)
	if _, ok := themeCredSecret(t, cs, name); ok {
		t.Fatalf("PUT with null left the credential secret behind")
	}
	if ref := themeCredStoredRef(t, env, name); ref != nil {
		t.Fatalf("credential_ref after clear = %v, want nil", *ref)
	}

	// A new credential is stored again.
	put(`,"credential":` + themeCredDockerConfig)
	if _, ok := themeCredSecret(t, cs, name); !ok {
		t.Fatalf("PUT with a credential stored no secret")
	}

	// Moving the location drops the credential, so it never follows the source to
	// another host.
	moved := name + "-moved"
	rr := themeInstDo(t, h, http.MethodPut, "/themes/sources/"+name, themeSrcBody(moved, "oci", themeSrcSpec(moved), ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("move location: status %d body %s", rr.Code, rr.Body.String())
	}
	if _, ok := themeCredSecret(t, cs, name); ok {
		t.Fatalf("credential secret survived a location change")
	}
	if ref := themeCredStoredRef(t, env, name); ref != nil {
		t.Fatalf("credential_ref after location change = %v, want nil", *ref)
	}
}

func TestThemeSourceCredentialDeleteRemovesSecret(t *testing.T) {
	env, cs := newThemeCredEnv(t)
	defer env.svc.WaitIdle()
	h := env.router("admin")
	name := themeCredName()
	create := themeSrcBody(name, "oci", themeSrcSpec(name), `,"credential":`+themeCredDockerConfig)
	if rr := themeInstDo(t, h, http.MethodPost, "/themes/sources", create); rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d body %s", rr.Code, rr.Body.String())
	}

	// A duplicate create is refused and leaves the existing credential alone.
	dup := themeSrcBody(name, "oci", themeSrcSpec(name), `,"credential":{"auths":{"registry.test":{"username":"x","password":"other"}}}`)
	if rr := themeInstDo(t, h, http.MethodPost, "/themes/sources", dup); rr.Code != http.StatusConflict {
		t.Fatalf("duplicate create: status %d body %s", rr.Code, rr.Body.String())
	}
	sec, ok := themeCredSecret(t, cs, name)
	if !ok || !strings.Contains(string(sec.Data[corev1.DockerConfigJsonKey]), themeCredSecretValue) {
		t.Fatalf("duplicate create replaced the existing credential")
	}

	if rr := themeInstDo(t, h, http.MethodDelete, "/themes/sources/"+name, ""); rr.Code != http.StatusNoContent {
		t.Fatalf("delete: status %d body %s", rr.Code, rr.Body.String())
	}
	if _, ok := themeCredSecret(t, cs, name); ok {
		t.Fatalf("delete left the credential secret behind")
	}
}

func TestThemeSourceCredentialRefusalsStoreNothing(t *testing.T) {
	env, cs := newThemeCredEnv(t)
	defer env.svc.WaitIdle()
	h := env.router("admin")
	cases := []struct {
		name       string
		credential string
	}{
		{"no auths object", `{"username":"u","password":"p"}`},
		{"empty auths", `{"auths":{}}`},
		{"empty registry entry", `{"auths":{"registry.test":{}}}`},
		{"unknown field", `{"auths":{"registry.test":{"username":"u","password":"p"}},"extra":"x"}`},
		{"not an object", `"` + themeCredSecretValue + `"`},
	}
	for _, c := range cases {
		name := themeCredName()
		body := themeSrcBody(name, "oci", themeSrcSpec(name), `,"credential":`+c.credential)
		rr := themeInstDo(t, h, http.MethodPost, "/themes/sources", body)
		if rr.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: status %d, want 422; body %s", c.name, rr.Code, rr.Body.String())
		}
		themeCredAssertHidden(t, rr.Body.String())
		if _, ok := themeCredSecret(t, cs, name); ok {
			t.Errorf("%s: refused credential stored a secret", c.name)
		}
		for _, it := range themeSrcItems(t, h) {
			if it.Name == name {
				t.Errorf("%s: refused source was stored", c.name)
			}
		}
	}
}
