package source

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
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
	"github.com/go-git/go-git/v5/storage/memory"
	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/GameplanePanel/gameplane/netguard"
)

// registerGuardedGitHTTP installs the guarded transport once.
var registerGuardedGitHTTP = sync.OnceFunc(func() {
	c := netguard.HTTPClient(2*time.Minute, netguard.IsAllowed)
	c.CheckRedirect = func(_ *http.Request, via []*http.Request) error {
		if via[0].Header.Get("Authorization") != "" {
			return errors.New("authenticated Git redirect refused; configure the final repository URL")
		}
		return nil
	}
	gitclient.InstallProtocol("https", githttp.NewClient(c))
})

// RegisterGuardedGitHTTP installs an SSRF-guarded HTTPS transport on the first
// call. go-git checks its ten-hop redirect limit before calling our hook.
// Authenticated clones must use their final URL because go-git can reapply
// BasicAuth to later requests under a redirected repository path. GitClone
// calls it; it is exported so tests can drive the transport directly.
func RegisterGuardedGitHTTP() { registerGuardedGitHTTP() }

// scpLikeURL matches git's scp-style syntax ("git@github.com:org/repo").
var scpLikeURL = regexp.MustCompile(`^[\w.~-]+@[\w.-]+:`)

// IsSCPLikeURL reports whether raw uses git's scp-style syntax
// ("git@github.com:org/repo"), which is an ssh transport.
func IsSCPLikeURL(raw string) bool { return scpLikeURL.MatchString(raw) }

// GitCloneFunc clones one ref of a repository and returns the worktree plus the
// resolved commit hash. GitClone is the real one; callers keep it in a variable
// so unit tests can stub the network round-trip.
type GitCloneFunc func(ctx context.Context, url, ref string, auth transport.AuthMethod) (fs.FS, string, error)

// GitClone clones one ref of a repository, shallow and in-memory, and
// returns the worktree plus the resolved commit hash.
func GitClone(ctx context.Context, url, ref string, auth transport.AuthMethod) (fs.FS, string, error) {
	RegisterGuardedGitHTTP()
	// Pre-flight the destination so an ssh clone (whose dial we can't wrap
	// with a Control hook) can't be aimed at link-local/metadata. https
	// clones are additionally guarded at dial time by the transport above.
	if err := netguard.CheckHostAllowed(ctx, GitHost(url), netguard.IsAllowed); err != nil {
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

// GitLoader returns the Loader for a git repository plus the reference prefix
// ("git:<url>@<ref>[/<subPath>]") that locates it. The whole catalog tracks one
// ref (default "main"); every bundle is stamped with the resolved commit as its
// digest ("git:<sha>") so content drift on a moving branch is detected even when
// versions don't change. subPath, when set, scopes the scan to that directory.
// The caller checks the URL (CheckGitURL) and resolves auth first.
func GitLoader(repoURL, ref, subPath string, auth transport.AuthMethod, clone GitCloneFunc) (Loader, string) {
	if ref == "" {
		ref = "main"
	}
	sub := strings.Trim(subPath, "/")
	load := func(ctx context.Context) (fs.FS, string, error) {
		worktree, commit, err := clone(ctx, repoURL, ref, auth)
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
	refPrefix := "git:" + repoURL + "@" + ref
	if sub != "" {
		refPrefix += "/" + sub
	}
	return load, refPrefix
}

// CheckGitURL allows https and ssh transports only. Plain http and
// git:// move bytes unauthenticated and unverified; file: would read
// the operator's own filesystem (that's what local sources are for).
func CheckGitURL(raw string) error {
	switch {
	case strings.HasPrefix(raw, "https://"),
		strings.HasPrefix(raw, "ssh://"),
		scpLikeURL.MatchString(raw):
	default:
		return fmt.Errorf("git url %q: only https://, ssh:// or git@host: URLs are supported", raw)
	}
	// Reject an obviously-internal host literal up front (link-local /
	// metadata). DNS names are re-checked against the resolved IP at clone
	// time by GitClone. Private/loopback literals are allowed: self-hosted
	// git servers legitimately live there.
	host := GitHost(raw)
	if netguard.HostIsMetadata(host) {
		return fmt.Errorf("git url %q: metadata endpoints are not allowed", raw)
	}
	if ip := net.ParseIP(host); ip != nil && !netguard.IsAllowed(ip) {
		return fmt.Errorf("git url %q: %s is a blocked address (link-local/metadata/multicast)", raw, ip)
	}
	return nil
}

// GitHost extracts the hostname from an https://, ssh:// or scp-like
// (git@host:org/repo) git URL.
func GitHost(raw string) string {
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

// KnownHostsCallback builds a host-key verifier from known_hosts bytes.
// x/crypto's parser only reads files, so the bytes detour through a
// temp file that is removed as soon as the callback is constructed.
func KnownHostsCallback(data []byte) (gossh.HostKeyCallback, error) {
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
