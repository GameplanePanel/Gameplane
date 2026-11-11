//go:build e2e

package e2e

import (
	"net"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Serve exactly the tested commit, including fork PRs, without giving the
// controller a GitHub credential or making runner credentials part of a clone.
func argoGitSource(t *testing.T) (address, revision string) {
	t.Helper()
	run := func(program string, args ...string) string {
		t.Helper()
		out, err := exec.CommandContext(t.Context(), program, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v: %s", program, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	root := run("git", "rev-parse", "--show-toplevel")
	revision = run("git", "rev-parse", "HEAD")
	directory := t.TempDir()
	repo := filepath.Join(directory, "candidate.git")
	run("git", "init", "--quiet", "--bare", repo)
	run("git", "-C", repo, "fetch", "--quiet", "--depth=1", root, revision+":refs/heads/candidate")
	run("git", "-C", repo, "symbolic-ref", "HEAD", "refs/heads/candidate")
	gateway := run("docker", "network", "inspect", "kind", "--format", "{{(index .IPAM.Config 0).Gateway}}")
	if ip := net.ParseIP(gateway); ip == nil || !ip.IsPrivate() {
		t.Fatalf("expected a private kind bridge gateway, got %q", gateway)
	}
	var listen net.ListenConfig
	listener, err := listen.Listen(t.Context(), "tcp", net.JoinHostPort(gateway, "0"))
	if err != nil {
		t.Fatal(err)
	}
	gitHTTP := &cgi.Handler{Path: filepath.Join(run("git", "--exec-path"), "git-http-backend"), Dir: directory,
		Env: []string{"GIT_PROJECT_ROOT=" + directory, "GIT_HTTP_EXPORT_ALL=1"}}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		discover := r.Method == http.MethodGet && r.URL.Path == "/candidate.git/info/refs" && r.URL.Query().Get("service") == "git-upload-pack"
		fetch := r.Method == http.MethodPost && r.URL.Path == "/candidate.git/git-upload-pack"
		if !discover && !fetch {
			http.NotFound(w, r)
			return
		}
		gitHTTP.ServeHTTP(w, r)
	}))
	_ = srv.Listener.Close()
	srv.Listener = listener
	srv.Start()
	t.Cleanup(srv.Close)
	return srv.URL + "/candidate.git", revision
}
