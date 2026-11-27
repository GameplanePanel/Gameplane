package source

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/go-git/go-git/v5/plumbing/transport"
	gitclient "github.com/go-git/go-git/v5/plumbing/transport/client"
	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/GameplanePanel/gameplane/netguard"
)

// gitsrcRun runs git in dir with a fixed identity and no user or system config
// and returns its trimmed stdout.
func gitsrcRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.test",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.test",
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull,
	)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			t.Fatalf("git %v: %v\n%s", args, err, exitErr.Stderr)
		}
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// gitsrcSmartServer serves one commit (branch main, tag v1.0.0) as "repo.git"
// over smart HTTP through the system git-http-backend, so GitClone exercises
// the real go-git http transport. It returns the base URL and the served
// commit. The test skips when git or git-http-backend is not installed.
func gitsrcSmartServer(t *testing.T) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	backend := filepath.Join(gitsrcRun(t, root, "--exec-path"), "git-http-backend")
	if _, err := os.Stat(backend); err != nil {
		t.Skipf("git-http-backend unavailable: %v", err)
	}
	work := filepath.Join(root, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	gitsrcRun(t, work, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(work, "module.yaml"), []byte("name: demo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitsrcRun(t, work, "add", "module.yaml")
	gitsrcRun(t, work, "commit", "-q", "-m", "init")
	gitsrcRun(t, work, "tag", "v1.0.0")
	head := gitsrcRun(t, work, "rev-parse", "HEAD")

	projects := filepath.Join(root, "projects")
	if err := os.MkdirAll(projects, 0o755); err != nil {
		t.Fatal(err)
	}
	gitsrcRun(t, root, "clone", "-q", "--bare", work, filepath.Join(projects, "repo.git"))

	srv := httptest.NewServer(&cgi.Handler{
		Path: backend,
		Root: "/",
		Env: []string{
			"GIT_PROJECT_ROOT=" + projects,
			"GIT_HTTP_EXPORT_ALL=1",
			"GIT_CONFIG_NOSYSTEM=1",
			"GIT_CONFIG_GLOBAL=" + os.DevNull,
		},
	})
	t.Cleanup(srv.Close)
	return srv.URL, head
}

// gitsrcKeyLine returns a known_hosts line binding host to the public key
// derived from seed. Keys are deterministic so the test needs no randomness.
func gitsrcKeyLine(host string, seed byte) (string, gossh.PublicKey) {
	priv := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{seed}, ed25519.SeedSize))
	pub, err := gossh.NewPublicKey(priv.Public())
	if err != nil {
		panic(err)
	}
	return host + " " + strings.TrimSpace(string(gossh.MarshalAuthorizedKey(pub))), pub
}

func TestGitIsSCPLikeURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{"scp github", "git@github.com:org/repo.git", true},
		{"scp bare host", "git@host:repo", true},
		{"scp punctuation in user", "a.b~c-d@h.x:p", true},
		{"https", "https://github.com/org/repo", false},
		{"ssh scheme", "ssh://git@host/x", false},
		{"no colon", "user@host/path", false},
		{"slash before at", "/tmp/a@b:c", false},
		{"empty host", "git@:repo", false},
		{"empty", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := IsSCPLikeURL(tc.raw); got != tc.want {
				t.Fatalf("IsSCPLikeURL(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestGitHost(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"https", "https://github.com/org/repo.git", "github.com"},
		{"https with port and userinfo", "https://user:pw@example.com:8443/x", "example.com"},
		{"ssh with port", "ssh://git@github.com:22/org/repo", "github.com"},
		{"scp", "git@github.com:org/repo", "github.com"},
		{"scp ipv4", "git@10.0.0.5:org/repo", "10.0.0.5"},
		{"https ipv6 literal", "https://[fe80::1]:8080/x", "fe80::1"},
		{"unparseable", "https://[::1", ""},
		{"no host", "not a url", ""},
		{"empty", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := GitHost(tc.raw); got != tc.want {
				t.Fatalf("GitHost(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestGitCheckGitURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"https dns name", "https://github.com/org/repo.git", false},
		{"ssh scheme", "ssh://git@github.com/org/repo.git", false},
		{"scp syntax", "git@github.com:org/repo.git", false},
		{"https private literal allowed", "https://10.0.0.5/repo.git", false},
		{"https loopback allowed", "https://127.0.0.1:8443/repo.git", false},
		{"https ipv6 loopback allowed", "https://[::1]/repo.git", false},
		{"https ula allowed", "https://[fd00::5]/repo.git", false},
		{"http refused", "http://github.com/org/repo.git", true},
		{"git protocol refused", "git://github.com/org/repo.git", true},
		{"file refused", "file:///etc/passwd", true},
		{"no scheme refused", "github.com/org/repo.git", true},
		{"empty refused", "", true},
		{"metadata name", "https://metadata.google.internal/x", true},
		{"metadata short name", "https://metadata/x", true},
		{"metadata name case and trailing dot", "https://METADATA.google.internal./x", true},
		{"link-local literal", "https://169.254.169.254/x", true},
		{"ssh link-local literal", "ssh://git@169.254.169.254/x", true},
		{"scp link-local literal", "git@169.254.169.254:org/repo", true},
		{"ipv6 link-local literal", "https://[fe80::1]/x", true},
		{"alibaba metadata literal", "https://100.100.100.200/x", true},
		{"nat64 wrapped metadata", "https://[64:ff9b::a9fe:a9fe]/x", true},
		{"unspecified literal", "https://0.0.0.0/x", true},
		{"multicast literal", "https://224.0.0.1/x", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := CheckGitURL(tc.raw)
			if (err != nil) != tc.wantErr {
				t.Fatalf("CheckGitURL(%q) error = %v, wantErr %v", tc.raw, err, tc.wantErr)
			}
		})
	}
}

func TestGitCloneRejectsBlockedHosts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  string
	}{
		{"metadata name", "https://metadata.google.internal/org/repo.git"},
		{"metadata short name", "https://metadata/org/repo.git"},
		{"link-local literal", "https://169.254.169.254/org/repo.git"},
		{"ipv6 link-local literal", "ssh://git@[fe80::1]/org/repo.git"},
		{"scp link-local literal", "git@169.254.169.254:org/repo.git"},
		{"empty host", "https:///org/repo.git"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fsys, commit, err := GitClone(t.Context(), tc.raw, "main", nil)
			if !errors.Is(err, netguard.ErrBlockedAddr) {
				t.Fatalf("GitClone(%q) error = %v, want ErrBlockedAddr", tc.raw, err)
			}
			if !strings.Contains(err.Error(), "git host for") {
				t.Fatalf("GitClone(%q) error %q lacks host context", tc.raw, err)
			}
			if fsys != nil || commit != "" {
				t.Fatalf("GitClone(%q) returned a worktree or commit on refusal", tc.raw)
			}
		})
	}
}

func TestGitCloneFailsWhenServerDown(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.NotFoundHandler())
	repo := srv.URL + "/repo.git"
	srv.Close() // a closed loopback port refuses the dial immediately

	fsys, commit, err := GitClone(t.Context(), repo, "main", nil)
	if err == nil {
		t.Fatal("GitClone against a closed port succeeded")
	}
	if !strings.Contains(err.Error(), "clone "+repo+"@main") {
		t.Fatalf("error %q does not name the repository and ref", err)
	}
	if fsys != nil || commit != "" {
		t.Fatal("failed clone returned a worktree or commit")
	}
}

func TestGitCloneSmartHTTP(t *testing.T) {
	t.Parallel()
	base, head := gitsrcSmartServer(t)
	repo := base + "/repo.git"
	tests := []struct {
		name    string
		ref     string
		wantErr bool
	}{
		{"branch", "main", false},
		{"tag after branch miss", "v1.0.0", false},
		{"unknown ref", "no-such-ref", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fsys, commit, err := GitClone(t.Context(), repo, tc.ref, nil)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("GitClone(%s) succeeded, want error", tc.ref)
				}
				if !strings.Contains(err.Error(), "clone "+repo+"@"+tc.ref) {
					t.Fatalf("error %q does not name repository and ref", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("GitClone(%s): %v", tc.ref, err)
			}
			if commit != head {
				t.Fatalf("commit = %q, want %q", commit, head)
			}
			body, err := fs.ReadFile(fsys, "module.yaml")
			if err != nil {
				t.Fatalf("read cloned file: %v", err)
			}
			if string(body) != "name: demo\n" {
				t.Fatalf("cloned file = %q", body)
			}
		})
	}
}

// gitsrcLoaderCall records one clone the stub received.
type gitsrcLoaderCall struct{ url, ref string }

func TestGitLoader(t *testing.T) {
	t.Parallel()
	const repo = "https://git.example.test/org/mods.git"
	tree := fstest.MapFS{
		"README.md":          {Data: []byte("root\n")},
		"mods/x/module.yaml": {Data: []byte("name: x\n")},
	}
	tests := []struct {
		name       string
		ref        string
		subPath    string
		cloneErr   error
		wantRef    string
		wantPrefix string
		readFile   string
		wantBody   string
		wantErr    string
	}{
		{
			name:       "default ref whole tree",
			ref:        "",
			wantRef:    "master",
			wantPrefix: "git:" + repo + "@master",
			readFile:   "README.md",
			wantBody:   "root\n",
		},
		{
			name:       "explicit ref scoped with slashes trimmed",
			ref:        "v2",
			subPath:    "/mods/x/",
			wantRef:    "v2",
			wantPrefix: "git:" + repo + "@v2/mods/x",
			readFile:   "module.yaml",
			wantBody:   "name: x\n",
		},
		{
			name:       "lone slash means no scope",
			ref:        "main",
			subPath:    "/",
			wantRef:    "main",
			wantPrefix: "git:" + repo + "@main",
			readFile:   "README.md",
			wantBody:   "root\n",
		},
		{
			name:       "missing scoped path opens but reads fail",
			subPath:    "absent",
			wantRef:    "master",
			wantPrefix: "git:" + repo + "@master/absent",
			readFile:   "module.yaml",
			wantErr:    "",
		},
		{
			name:       "unclean subPath rejected",
			subPath:    "a/../b",
			wantRef:    "master",
			wantPrefix: "git:" + repo + "@master/a/../b",
			wantErr:    "subPath",
		},
		{
			name:       "clone failure propagates",
			cloneErr:   errors.New("upstream exploded"),
			wantRef:    "master",
			wantPrefix: "git:" + repo + "@master",
			wantErr:    "upstream exploded",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var calls []gitsrcLoaderCall
			clone := func(_ context.Context, u, ref string, _ transport.AuthMethod) (fs.FS, string, error) {
				calls = append(calls, gitsrcLoaderCall{u, ref})
				if tc.cloneErr != nil {
					return nil, "", tc.cloneErr
				}
				return tree, "abc123", nil
			}
			load, prefix := GitLoader(repo, tc.ref, tc.subPath, nil, clone)
			if prefix != tc.wantPrefix {
				t.Fatalf("prefix = %q, want %q", prefix, tc.wantPrefix)
			}
			fsys, digest, err := load(t.Context())
			if len(calls) != 1 || calls[0].url != repo || calls[0].ref != tc.wantRef {
				t.Fatalf("clone calls = %+v, want one call for %s@%s", calls, repo, tc.wantRef)
			}
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("load error = %v, want substring %q", err, tc.wantErr)
				}
				if fsys != nil {
					t.Fatal("failed load returned a filesystem")
				}
				return
			}
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if digest != "git:abc123" {
				t.Fatalf("digest = %q, want git:abc123", digest)
			}
			body, readErr := fs.ReadFile(fsys, tc.readFile)
			if tc.wantBody == "" {
				if !errors.Is(readErr, fs.ErrNotExist) {
					t.Fatalf("read %s: %v, want fs.ErrNotExist", tc.readFile, readErr)
				}
				return
			}
			if readErr != nil || string(body) != tc.wantBody {
				t.Fatalf("read %s = %q, %v; want %q", tc.readFile, body, readErr, tc.wantBody)
			}
		})
	}
}

func TestGitKnownHostsCallback(t *testing.T) {
	t.Parallel()
	const host = "git.example.test"
	lineA, keyA := gitsrcKeyLine(host, 1)
	_, keyB := gitsrcKeyLine(host, 2)
	lineIP, _ := gitsrcKeyLine("192.0.2.10", 1)
	revokedA := "@revoked " + lineA
	remote := &net.TCPAddr{IP: net.ParseIP("192.0.2.10"), Port: 22}

	tests := []struct {
		name     string
		data     string
		newErr   bool
		hostname string
		key      gossh.PublicKey
		want     string // "ok", "key", "revoked" or "other"
		wantWant int    // expected KeyError.Want length when want is "key"
	}{
		{"matching key", lineA + "\n", false, host + ":22", keyA, "ok", 0},
		{"wrong key", lineA + "\n", false, host + ":22", keyB, "key", 1},
		{"unknown host", lineA + "\n", false, "other.example.test:22", keyA, "key", 0},
		{"empty file trusts nobody", "", false, host + ":22", keyA, "key", 0},
		{"comments only trusts nobody", "# note\n\n", false, host + ":22", keyA, "key", 0},
		{"no hostname falls back to remote ip", lineIP + "\n", false, "", keyA, "ok", 0},
		{"revoked key", revokedA + "\n", false, host + ":22", keyA, "revoked", 0},
		{"address without port", lineA + "\n", false, host, keyA, "other", 0},
		{"single token line", "garbage\n", true, "", nil, "", 0},
		{"unknown marker", "@bogus " + host + " ssh-ed25519 AAAA\n", true, "", nil, "", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cb, err := KnownHostsCallback([]byte(tc.data))
			if tc.newErr {
				if err == nil {
					t.Fatal("KnownHostsCallback accepted invalid data")
				}
				return
			}
			if err != nil {
				t.Fatalf("KnownHostsCallback: %v", err)
			}
			err = cb(tc.hostname, remote, tc.key)
			switch tc.want {
			case "ok":
				if err != nil {
					t.Fatalf("callback: %v, want nil", err)
				}
			case "key":
				var keyErr *knownhosts.KeyError
				if !errors.As(err, &keyErr) {
					t.Fatalf("callback error = %v, want *KeyError", err)
				}
				if len(keyErr.Want) != tc.wantWant {
					t.Fatalf("KeyError.Want has %d entries, want %d", len(keyErr.Want), tc.wantWant)
				}
			case "revoked":
				var revErr *knownhosts.RevokedError
				if !errors.As(err, &revErr) {
					t.Fatalf("callback error = %v, want *RevokedError", err)
				}
			case "other":
				var keyErr *knownhosts.KeyError
				var revErr *knownhosts.RevokedError
				if err == nil || errors.As(err, &keyErr) || errors.As(err, &revErr) {
					t.Fatalf("callback error = %v, want a non-key parse error", err)
				}
			}
		})
	}
}

// Not parallel: t.Setenv is incompatible with t.Parallel.
func TestGitKnownHostsCallbackTempFileFailure(t *testing.T) {
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "absent"))
	if _, err := KnownHostsCallback([]byte("host ssh-ed25519 AAAA\n")); err == nil {
		t.Fatal("KnownHostsCallback succeeded without a temp directory")
	}
}

func TestGitRegisterGuardedGitHTTP(t *testing.T) {
	t.Parallel()
	// The guarded client is installed once; repeat calls must be harmless and
	// leave an https transport registered for go-git.
	RegisterGuardedGitHTTP()
	RegisterGuardedGitHTTP()
	if gitclient.Protocols["https"] == nil {
		t.Fatal("https transport not registered")
	}
}
