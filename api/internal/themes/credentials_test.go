package themes

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
)

// credTestNS is the credentials namespace of these tests.
const credTestNS = "gameplane-test"

// credTestService returns a Service whose credential store holds secrets.
func credTestService(t *testing.T, secrets ...*corev1.Secret) *Service {
	t.Helper()
	cs := fake.NewClientset()
	for _, s := range secrets {
		s.Namespace = credTestNS
		if _, err := cs.CoreV1().Secrets(credTestNS).Create(context.Background(), s, metav1.CreateOptions{}); err != nil {
			t.Fatalf("create secret %s: %v", s.Name, err)
		}
	}
	return &Service{kube: &kube.Client{Typed: cs}, credentialsNS: credTestNS}
}

// credTestSource is a source of type typ that names the Secret ref.
func credTestSource(typ, ref string) db.ThemeSource {
	return db.ThemeSource{Name: "src", Type: typ, CredentialRef: &ref}
}

func TestReadCredentialKeysPerType(t *testing.T) {
	svc := credTestService(t,
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "token"}, Data: map[string][]byte{"token": []byte("tok")}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "basic"}, Data: map[string][]byte{"username": []byte("u"), "password": []byte("p")}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "user-only"}, Data: map[string][]byte{"username": []byte("u")}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "empty"}},
	)
	ctx := context.Background()
	cases := []struct {
		typ, ref string
		wantErr  string
	}{
		{"git", "token", ""},
		{"http", "basic", ""},
		{"git", "user-only", "needs a token key"},
		{"http", "empty", "has no data"},
		{"oci", "token", corev1.DockerConfigJsonKey},
		{"git", "missing", "missing"},
	}
	for _, c := range cases {
		_, err := svc.readCredential(ctx, credTestSource(c.typ, c.ref))
		switch {
		case c.wantErr == "" && err != nil:
			t.Errorf("%s/%s: unexpected error %v", c.typ, c.ref, err)
		case c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)):
			t.Errorf("%s/%s: err = %v, want it to contain %q", c.typ, c.ref, err, c.wantErr)
		}
	}
	if data, err := svc.readCredential(ctx, db.ThemeSource{Name: "src", Type: "git"}); data != nil || err != nil {
		t.Errorf("source without a Secret name: %v, %v; want nil, nil", data, err)
	}
	if _, err := (&Service{}).readCredential(ctx, credTestSource("git", "token")); err == nil {
		t.Errorf("panel without a credential store read a credential")
	}
}

func TestGitAndHTTPAuthFromSecretData(t *testing.T) {
	if gitAuth(nil) != nil || httpAuthHeader(nil) != "" {
		t.Fatalf("no credential must give anonymous access")
	}
	tok := map[string][]byte{"token": []byte("tok")}
	if got := httpAuthHeader(tok); got != "Bearer tok" {
		t.Errorf("http token header = %q", got)
	}
	basic := map[string][]byte{"username": []byte("u"), "password": []byte("p")}
	if got := httpAuthHeader(basic); got != "Basic "+base64.StdEncoding.EncodeToString([]byte("u:p")) {
		t.Errorf("http basic header = %q", got)
	}
	if a := gitAuth(tok); a == nil || !strings.Contains(a.String(), gitHTTPUsername) {
		t.Errorf("git token auth = %v, want user %s", a, gitHTTPUsername)
	}
	if a := gitAuth(basic); a == nil || !strings.Contains(a.String(), "u") {
		t.Errorf("git basic auth = %v", a)
	}
}

func TestOCICredentialFuncMatchesRegistryHost(t *testing.T) {
	cfg := `{"auths":{"https://index.docker.io/v1/":{"auth":"` + base64.StdEncoding.EncodeToString([]byte("robot:pw")) + `"}}}`
	creds, err := ociCredentialFunc(map[string][]byte{corev1.DockerConfigJsonKey: []byte(cfg)}, "docker.io")
	if err != nil {
		t.Fatalf("ociCredentialFunc: %v", err)
	}
	got, err := creds(context.Background(), "DOCKER.IO")
	if err != nil || got.Username != "robot" || got.Password != "pw" {
		t.Fatalf("docker.io credential = %+v, %v", got, err)
	}
	if other, _ := creds(context.Background(), "evil.example"); other.Username != "" || other.Password != "" {
		t.Fatalf("credential sent to another registry: %+v", other)
	}
	if _, err := ociCredentialFunc(map[string][]byte{corev1.DockerConfigJsonKey: []byte(cfg)}, "ghcr.io"); err == nil || !strings.Contains(err.Error(), "no entry for registry ghcr.io") {
		t.Fatalf("missing registry entry: err = %v", err)
	}
	if _, err := ociCredentialFunc(map[string][]byte{}, "docker.io"); err == nil {
		t.Fatalf("Secret without a docker config gave a credential")
	}
}
