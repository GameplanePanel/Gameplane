//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	revision = run("git", "rev-parse", "HEAD")
	directory := t.TempDir()
	repo := filepath.Join(directory, "candidate.git")
	if run("git", "rev-parse", "--is-shallow-repository") == "true" {
		t.Fatal("the Argo CD fixture needs a full checkout to serve the candidate branch")
	}
	run("git", "init", "--quiet", "--bare", repo)
	// Share the checkout's objects. The temporary repository publishes one
	// ref and has its own configuration, with no runner credentials.
	objects := run("git", "rev-parse", "--path-format=absolute", "--git-path", "objects")
	if err := os.WriteFile(filepath.Join(repo, "objects", "info", "alternates"), []byte(objects+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("git", "-C", repo, "update-ref", "refs/heads/candidate", revision)
	run("git", "-C", repo, "symbolic-ref", "HEAD", "refs/heads/candidate")
	gitHTTP := &cgi.Handler{Path: filepath.Join(run("git", "--exec-path"), "git-http-backend"), Dir: directory,
		Env: []string{"GIT_PROJECT_ROOT=" + directory, "GIT_HTTP_EXPORT_ALL=1"}}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		discover := r.Method == http.MethodGet && r.URL.Path == "/candidate.git/info/refs" && r.URL.Query().Get("service") == "git-upload-pack"
		fetch := r.Method == http.MethodPost && r.URL.Path == "/candidate.git/git-upload-pack"
		if !discover && !fetch {
			http.NotFound(w, r)
			return
		}
		gitHTTP.ServeHTTP(w, r)
	})
	// Docker may list the IPv6 bridge first even on an IPv4 Pod network.
	// Use the same Git client and network namespace that will fetch the chart.
	gateways := run("docker", "network", "inspect", "kind", "--format", "{{range .IPAM.Config}}{{println .Gateway}}{{end}}")
	var failures []string
	for _, gateway := range strings.Fields(gateways) {
		if ip := net.ParseIP(gateway); ip == nil || !ip.IsPrivate() {
			continue
		}
		var listen net.ListenConfig
		listener, err := listen.Listen(t.Context(), "tcp", net.JoinHostPort(gateway, "0"))
		if err != nil {
			failures = append(failures, err.Error())
			continue
		}
		srv := httptest.NewUnstartedServer(handler)
		_ = srv.Listener.Close()
		srv.Listener = listener
		srv.Start()
		address := srv.URL + "/candidate.git"
		probe, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		out, err := envInstance.Kubectl(probe, "-n", "argocd", "exec", "deploy/argocd-repo-server", "-c", "argocd-repo-server",
			"--", "git", "ls-remote", "--exit-code", address, "refs/heads/candidate")
		cancel()
		if err == nil && strings.TrimSpace(out) == revision+"\trefs/heads/candidate" {
			t.Cleanup(srv.Close)
			return address, revision
		}
		srv.Close()
		failures = append(failures, fmt.Sprintf("%s: %v %s", gateway, err, strings.TrimSpace(out)))
	}
	t.Fatalf("Argo CD repo-server could not read revision %s through the private kind gateways %q: %v", revision, gateways, failures)
	return "", ""
}
