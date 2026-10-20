package modsrc

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	gossh "golang.org/x/crypto/ssh"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// testSSHKey generates an ed25519 keypair, returning the private key
// as OpenSSH PEM plus a matching known_hosts line.
func testSSHKey(t *testing.T) (privPEM, knownHosts []byte) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	block, err := gossh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	sshPub, err := gossh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("public key: %v", err)
	}
	line := "github.com " + sshPub.Type() + " " +
		strings.TrimSpace(strings.SplitN(string(gossh.MarshalAuthorizedKey(sshPub)), " ", 2)[1])
	return pem.EncodeToMemory(block), []byte(line)
}

// stubClone swaps the gitClone package hook for the test's lifetime.
func stubClone(t *testing.T, fn func(ctx context.Context, url, ref string, auth transport.AuthMethod) (fs.FS, string, error)) {
	t.Helper()
	orig := gitClone
	gitClone = fn
	t.Cleanup(func() { gitClone = orig })
}

func gitSecret(name string, data map[string][]byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "gameplane-system"},
		Data:       data,
	}
}

func TestCheckGitURL(t *testing.T) {
	for _, ok := range []string{
		"https://github.com/GameplanePanel/module.git",
		"ssh://git@github.com/GameplanePanel/module.git",
		"git@github.com:gameplane-gg/modules.git",
		// Self-hosted git on a private literal is legitimate, not SSRF.
		"https://10.0.0.5/internal/modules.git",
		"git@192.168.1.10:team/modules.git",
	} {
		if err := checkGitURL(ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"http://github.com/x.git",
		"git://github.com/x.git",
		"file:///etc",
		"/srv/repo",
		// SSRF: link-local / cloud metadata are never a legitimate store.
		"ssh://git@169.254.169.254/x.git",
		"git@169.254.169.254:x/y.git",
		"https://metadata.google.internal/x.git",
	} {
		if err := checkGitURL(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestGitFetcher_IndexStampsCommitDigest(t *testing.T) {
	stubClone(t, func(_ context.Context, url, ref string, _ transport.AuthMethod) (fs.FS, string, error) {
		if url != "https://example.com/mods.git" || ref != "main" {
			return nil, "", fmt.Errorf("unexpected clone of %s@%s", url, ref)
		}
		return moduleDirFS(map[string]map[string]string{
			"modules/mc": validModuleFiles("mc", "1.0.0"),
			"junk":       {"file.txt": "x"},
		}), "abc123", nil
	})

	spec := &gameplanev1alpha1.GitSourceSpec{URL: "https://example.com/mods.git"}
	f, err := newGit(context.Background(), fake.NewClientBuilder().Build(), "gameplane-system", spec, nil)
	if err != nil {
		t.Fatalf("newGit: %v", err)
	}
	entries, warnings, err := f.Index(context.Background())
	if err != nil || len(warnings) != 0 {
		t.Fatalf("Index: %v warnings=%v", err, warnings)
	}
	if len(entries) != 1 || entries[0].Name != "mc" {
		t.Fatalf("entries = %+v", entries)
	}
	if entries[0].Digest != "git:abc123" {
		t.Errorf("digest = %q, want git:abc123", entries[0].Digest)
	}
	if entries[0].Reference != "git:https://example.com/mods.git@main/modules/mc" {
		t.Errorf("reference = %q", entries[0].Reference)
	}

	b, err := f.Pull(context.Background(), "mc", "1.0.0")
	if err != nil || b.Digest != "git:abc123" {
		t.Fatalf("Pull: %+v %v", b, err)
	}
}

func TestGitFetcher_SubPathScopesScan(t *testing.T) {
	stubClone(t, func(context.Context, string, string, transport.AuthMethod) (fs.FS, string, error) {
		return moduleDirFS(map[string]map[string]string{
			"modules/mc": validModuleFiles("mc", "1.0.0"),
			"other/junkmod": {
				FileMetadata: "name: junkmod\nversion: 1.0.0\n",
				FileTemplate: "spec: {}\n",
			},
		}), "abc123", nil
	})
	spec := &gameplanev1alpha1.GitSourceSpec{
		URL:     "https://example.com/mods.git",
		Ref:     "v2",
		SubPath: "modules/",
	}
	f, err := newGit(context.Background(), fake.NewClientBuilder().Build(), "gameplane-system", spec, nil)
	if err != nil {
		t.Fatalf("newGit: %v", err)
	}
	entries, _, err := f.Index(context.Background())
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "mc" {
		t.Fatalf("entries = %+v", entries)
	}
	if entries[0].Reference != "git:https://example.com/mods.git@v2/modules/mc" {
		t.Errorf("reference = %q", entries[0].Reference)
	}
}

func TestGitFetcher_CloneFailureIsTotalFailure(t *testing.T) {
	stubClone(t, func(context.Context, string, string, transport.AuthMethod) (fs.FS, string, error) {
		return nil, "", fmt.Errorf("authentication required")
	})
	spec := &gameplanev1alpha1.GitSourceSpec{URL: "https://example.com/private.git"}
	f, err := newGit(context.Background(), fake.NewClientBuilder().Build(), "gameplane-system", spec, nil)
	if err != nil {
		t.Fatalf("newGit: %v", err)
	}
	if _, _, err := f.Index(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "authentication required") {
		t.Fatalf("err = %v", err)
	}
}

func TestGitAuth(t *testing.T) {
	ctx := context.Background()
	const ns = "gameplane-system"

	t.Run("nil secretRef is anonymous", func(t *testing.T) {
		a, err := gitAuth(ctx, fake.NewClientBuilder().Build(), ns, nil, "https://x")
		if err != nil || a != nil {
			t.Fatalf("a=%v err=%v", a, err)
		}
	})

	t.Run("token becomes basic auth", func(t *testing.T) {
		c := fake.NewClientBuilder().
			WithObjects(gitSecret("creds", map[string][]byte{"token": []byte("ghp_x")})).Build()
		a, err := gitAuth(ctx, c, ns, &corev1.LocalObjectReference{Name: "creds"}, "https://x")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		basic, ok := a.(*githttp.BasicAuth)
		if !ok || basic.Password != "ghp_x" {
			t.Fatalf("a = %#v", a)
		}
	})

	t.Run("username+password becomes basic auth", func(t *testing.T) {
		c := fake.NewClientBuilder().
			WithObjects(gitSecret("creds", map[string][]byte{
				"username": []byte("alice"), "password": []byte("s3cret"),
			})).Build()
		a, err := gitAuth(ctx, c, ns, &corev1.LocalObjectReference{Name: "creds"}, "https://x")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		basic, ok := a.(*githttp.BasicAuth)
		if !ok || basic.Username != "alice" || basic.Password != "s3cret" {
			t.Fatalf("a = %#v", a)
		}
	})

	t.Run("https secret without usable keys errors", func(t *testing.T) {
		c := fake.NewClientBuilder().
			WithObjects(gitSecret("creds", map[string][]byte{"bogus": []byte("x")})).Build()
		if _, err := gitAuth(ctx, c, ns, &corev1.LocalObjectReference{Name: "creds"}, "https://x"); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("missing secret errors", func(t *testing.T) {
		c := fake.NewClientBuilder().Build()
		if _, err := gitAuth(ctx, c, ns, &corev1.LocalObjectReference{Name: "ghost"}, "https://x"); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("ssh requires private key and known_hosts", func(t *testing.T) {
		c := fake.NewClientBuilder().
			WithObjects(gitSecret("creds", map[string][]byte{"token": []byte("x")})).Build()
		_, err := gitAuth(ctx, c, ns, &corev1.LocalObjectReference{Name: "creds"}, "git@github.com:x/y.git")
		if err == nil || !strings.Contains(err.Error(), "ssh-privatekey") {
			t.Fatalf("err = %v", err)
		}

		key, knownHosts := testSSHKey(t)
		c = fake.NewClientBuilder().
			WithObjects(gitSecret("creds", map[string][]byte{"ssh-privatekey": key})).Build()
		_, err = gitAuth(ctx, c, ns, &corev1.LocalObjectReference{Name: "creds"}, "git@github.com:x/y.git")
		if err == nil || !strings.Contains(err.Error(), "known_hosts") {
			t.Fatalf("err = %v", err)
		}

		c = fake.NewClientBuilder().
			WithObjects(gitSecret("creds", map[string][]byte{
				"ssh-privatekey": key,
				"known_hosts":    knownHosts,
			})).Build()
		a, err := gitAuth(ctx, c, ns, &corev1.LocalObjectReference{Name: "creds"}, "git@github.com:x/y.git")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if _, ok := a.(*gitssh.PublicKeys); !ok {
			t.Fatalf("a = %#v", a)
		}
	})
}
