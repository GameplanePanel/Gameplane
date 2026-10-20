package modsrc

import (
	"context"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-billy/v5/helper/iofs"
	"github.com/go-git/go-billy/v5/memfs"
	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
	gitclient "github.com/go-git/go-git/v5/plumbing/transport/client"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"github.com/go-git/go-git/v5/storage/memory"
	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/GameplanePanel/gameplane/netguard"
	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// registerGuardedGitHTTP installs an SSRF-guarded http client as go-git's
// https transport so a clone cannot be redirected at the cloud metadata
// endpoint (see internal/netguard). It is process-global go-git state, set
// once on the first real clone; tests stub gitClone and never reach it.
var registerGuardedGitHTTP = sync.OnceFunc(func() {
	gitclient.InstallProtocol("https", githttp.NewClient(netguard.HTTPClient(2*time.Minute, netguard.IsAllowed)))
})

// scpLikeURL matches git's scp-style syntax ("git@github.com:org/repo").
var scpLikeURL = regexp.MustCompile(`^[\w.~-]+@[\w.-]+:`)

// gitClone clones one ref of a repository, shallow and in-memory, and
// returns the worktree plus the resolved commit hash. A package var so
// unit tests can stub the network round-trip; everything around it
// (scheme checks, auth resolution, subPath, scanning, digest stamping)
// runs for real.
var gitClone = func(ctx context.Context, url, ref string, auth transport.AuthMethod) (fs.FS, string, error) {
	registerGuardedGitHTTP()
	// Pre-flight the destination so an ssh clone (whose dial we can't wrap
	// with a Control hook) can't be aimed at link-local/metadata. https
	// clones are additionally guarded at dial time by the transport above.
	if err := netguard.CheckHostAllowed(ctx, gitHost(url), netguard.IsAllowed); err != nil {
		return nil, "", fmt.Errorf("git host for %q: %w", url, err)
	}
	clone := func(refName plumbing.ReferenceName) (*gogit.Repository, fs.FS, error) {
		wt := memfs.New()
		repo, err := gogit.CloneContext(ctx, memory.NewStorage(), wt, &gogit.CloneOptions{
			URL:           url,
			ReferenceName: refName,
			SingleBranch:  true,
			Depth:         1,
			Auth:          auth,
			Tags:          gogit.NoTags,
		})
		if err != nil {
			return nil, nil, err
		}
		return repo, iofs.New(wt), nil
	}
	// ref may name a branch or a tag; try branch first.
	repo, worktree, err := clone(plumbing.NewBranchReferenceName(ref))
	if err != nil {
		var tagErr error
		if repo, worktree, tagErr = clone(plumbing.NewTagReferenceName(ref)); tagErr != nil {
			return nil, "", fmt.Errorf("clone %s@%s: %w", url, ref, err)
		}
	}
	head, err := repo.Head()
	if err != nil {
		return nil, "", fmt.Errorf("resolve HEAD of %s@%s: %w", url, ref, err)
	}
	return worktree, head.Hash().String(), nil
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
	ref := spec.Ref
	if ref == "" {
		ref = "main"
	}
	sub := strings.Trim(spec.SubPath, "/")
	load := func(ctx context.Context) (fs.FS, string, error) {
		worktree, commit, err := gitClone(ctx, spec.URL, ref, auth)
		if err != nil {
			return nil, "", err
		}
		if sub != "" {
			scoped, err := fs.Sub(worktree, sub)
			if err != nil {
				return nil, "", fmt.Errorf("subPath %q: %w", sub, err)
			}
			worktree = scoped
		}
		return worktree, "git:" + commit, nil
	}
	refPrefix := "git:" + spec.URL + "@" + ref
	if sub != "" {
		refPrefix += "/" + sub
	}
	return newFSFetcher(load, refPrefix, allow), nil
}

// checkGitURL allows https and ssh transports only. Plain http and
// git:// move bytes unauthenticated and unverified; file: would read
// the operator's own filesystem (that's what local sources are for).
func checkGitURL(raw string) error {
	switch {
	case strings.HasPrefix(raw, "https://"),
		strings.HasPrefix(raw, "ssh://"),
		scpLikeURL.MatchString(raw):
	default:
		return fmt.Errorf("git url %q: only https://, ssh:// or git@host: URLs are supported", raw)
	}
	// Reject an obviously-internal host literal up front (link-local /
	// metadata). DNS names are re-checked against the resolved IP at clone
	// time by gitClone. Private/loopback literals are allowed: self-hosted
	// git servers legitimately live there.
	host := gitHost(raw)
	if netguard.HostIsMetadata(host) {
		return fmt.Errorf("git url %q: metadata endpoints are not allowed", raw)
	}
	if ip := net.ParseIP(host); ip != nil && !netguard.IsAllowed(ip) {
		return fmt.Errorf("git url %q: %s is a blocked address (link-local/metadata/multicast)", raw, ip)
	}
	return nil
}

// gitHost extracts the hostname from an https://, ssh:// or scp-like
// (git@host:org/repo) git URL.
func gitHost(raw string) string {
	if scpLikeURL.MatchString(raw) {
		rest := raw[strings.IndexByte(raw, '@')+1:]
		if i := strings.IndexByte(rest, ':'); i >= 0 {
			return rest[:i]
		}
		return rest
	}
	if u, err := url.Parse(raw); err == nil {
		return u.Hostname()
	}
	return ""
}

// gitAuth resolves the source's credential Secret into a go-git auth
// method. Key contract (documented in docs/module-authoring.md):
// "token" or "username"+"password" for https; "ssh-privatekey"
// (+ optional "known_hosts") for ssh.
func gitAuth(ctx context.Context, c client.Client, namespace string, secretRef *corev1.LocalObjectReference, url string) (transport.AuthMethod, error) {
	if secretRef == nil || secretRef.Name == "" {
		return nil, nil
	}
	var sec corev1.Secret
	if err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: secretRef.Name}, &sec); err != nil {
		return nil, fmt.Errorf("get git credentials %s/%s: %w", namespace, secretRef.Name, err)
	}

	if strings.HasPrefix(url, "ssh://") || scpLikeURL.MatchString(url) {
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
		cb, err := knownHostsCallback(kh)
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

// knownHostsCallback builds a host-key verifier from known_hosts bytes.
// x/crypto's parser only reads files, so the bytes detour through a
// temp file that is removed as soon as the callback is constructed.
func knownHostsCallback(data []byte) (gossh.HostKeyCallback, error) {
	f, err := os.CreateTemp("", "gameplane-known-hosts-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	return knownhosts.New(f.Name())
}
