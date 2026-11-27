package modsrc

import (
	"context"
	"fmt"
	"io/fs"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/GameplanePanel/gameplane/bundlefetch/source"
	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// registerGuardedGitHTTP installs the SSRF-guarded HTTPS transport (see
// source.RegisterGuardedGitHTTP).
func registerGuardedGitHTTP() { source.RegisterGuardedGitHTTP() }

// gitClone clones one ref of a repository, shallow and in-memory, and
// returns the worktree plus the resolved commit hash. A package var so
// unit tests can stub the network round-trip; everything around it
// (scheme checks, auth resolution, subPath, scanning, digest stamping)
// runs for real.
var gitClone = func(ctx context.Context, url, ref string, auth transport.AuthMethod) (fs.FS, string, error) {
	return source.GitClone(ctx, url, ref, auth)
}

// newGit builds a Fetcher over a git repository. The whole catalog
// tracks one ref; every module is stamped with the resolved commit as
// its digest ("git:<sha>") so content drift on a moving branch is
// detected even when module.yaml versions don't change.
func newGit(ctx context.Context, c client.Client, namespace string, spec *gameplanev1alpha1.GitSourceSpec, allow []string) (Fetcher, error) {
	if err := checkGitURL(spec.URL); err != nil {
		return nil, err
	}
	auth, err := gitAuth(ctx, c, namespace, spec.SecretRef, spec.URL)
	if err != nil {
		return nil, err
	}
	// The closure reads gitClone at call time, so a test stub swapped in
	// after newGit still takes effect.
	load, refPrefix := source.GitLoader(spec.URL, spec.Ref, spec.SubPath, auth,
		func(ctx context.Context, url, ref string, auth transport.AuthMethod) (fs.FS, string, error) {
			return gitClone(ctx, url, ref, auth)
		})
	return newFSFetcher(load, refPrefix, allow), nil
}

// checkGitURL allows https and ssh transports only (see source.CheckGitURL).
func checkGitURL(raw string) error { return source.CheckGitURL(raw) }

// gitAuth resolves the source's credential Secret into a go-git auth
// method. Key contract (documented in docs/module-authoring.md):
// "token" or "username"+"password" for https; "ssh-privatekey"
// (+ optional "known_hosts") for ssh.
func gitAuth(ctx context.Context, c client.Client, namespace string, secretRef *corev1.LocalObjectReference, url string) (transport.AuthMethod, error) {
	if secretRef == nil || secretRef.Name == "" {
		return nil, nil
	}
	sec, err := readSourceCredential(ctx, c, namespace, secretRef.Name, "gameplane.local/git-source-url", url)
	if err != nil {
		return nil, fmt.Errorf("get git credentials %s/%s: %w", namespace, secretRef.Name, err)
	}

	if strings.HasPrefix(url, "ssh://") || source.IsSCPLikeURL(url) {
		key, ok := sec.Data["ssh-privatekey"]
		if !ok {
			return nil, fmt.Errorf("secret %s has no \"ssh-privatekey\" key (required for ssh URLs)", secretRef.Name)
		}
		user := "git"
		if u, ok := sec.Data["username"]; ok && len(u) > 0 {
			user = string(u)
		}
		keys, err := gitssh.NewPublicKeys(user, key, "")
		if err != nil {
			return nil, fmt.Errorf("parse ssh-privatekey in %s: %w", secretRef.Name, err)
		}
		// Host keys are always verified — the secret must pin them.
		kh, ok := sec.Data["known_hosts"]
		if !ok || len(kh) == 0 {
			return nil, fmt.Errorf("secret %s needs a \"known_hosts\" key for ssh URLs (run `ssh-keyscan <host>`)", secretRef.Name)
		}
		cb, err := source.KnownHostsCallback(kh)
		if err != nil {
			return nil, fmt.Errorf("parse known_hosts in %s: %w", secretRef.Name, err)
		}
		keys.HostKeyCallback = cb
		return keys, nil
	}

	if token, ok := sec.Data["token"]; ok && len(token) > 0 {
		// GitHub/GitLab accept PATs as the password with any username.
		return &githttp.BasicAuth{Username: "gameplane", Password: string(token)}, nil
	}
	user, uok := sec.Data["username"]
	pass, pok := sec.Data["password"]
	if uok && pok {
		return &githttp.BasicAuth{Username: string(user), Password: string(pass)}, nil
	}
	return nil, fmt.Errorf("secret %s needs \"token\" or \"username\"+\"password\" for https URLs", secretRef.Name)
}
