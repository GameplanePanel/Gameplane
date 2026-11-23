package rbac

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// FileAccess is the single authorization surface for GameServer file operations (spec 040).
// The dashboard file routes, the SFTP endpoint's session routes and the SFTP server listing all
// go through it, so the rules for "may this user do X to these files" live in one place.
//
// The permission keys used by file routes are defined only here (filePermRead, filePermWrite).
// Spec 039 may replace the rules behind this interface without changing its callers.

// FileAction names one file operation.
type FileAction string

// File actions. Each maps to read-class or write-class authorization, see filePermission.
const (
	FileList     FileAction = "list"
	FileStat     FileAction = "stat"
	FileRead     FileAction = "read"
	FileDownload FileAction = "download"
	FileCreate   FileAction = "create"
	FileWrite    FileAction = "write"
	FileUpload   FileAction = "upload"
	FileRename   FileAction = "rename"
	FileMkdir    FileAction = "mkdir"
	FileDelete   FileAction = "delete"
)

// FileTarget identifies the server and the paths an action touches. Paths is empty for
// route-level checks; FileRename requires two paths (from, to) when the handler checks its body.
type FileTarget struct {
	Cluster   string
	Namespace string
	Server    string
	Paths     []string
}

// ServerRef names one GameServer.
type ServerRef struct {
	Cluster   string
	Namespace string
	Name      string
}

// FileAccess decides which servers a user may see and which file operations they may perform.
type FileAccess interface {
	// VisibleServers lists the servers on cluster the user may see: every server in a namespace
	// the user can read, plus servers they own or collaborate on.
	VisibleServers(ctx context.Context, user *auth.User, cluster string) ([]ServerRef, error)
	// Allow returns ErrFileAccessDenied unless user may perform action on target.
	Allow(ctx context.Context, user *auth.User, action FileAction, target FileTarget) error
	// FilterEntries returns the directory entries of target the user may see.
	FilterEntries(ctx context.Context, user *auth.User, target FileTarget, names []string) ([]string, error)
}

// ErrFileAccessDenied is returned when a file operation is not permitted.
var ErrFileAccessDenied = errors.New("file access denied")

// ServerLister lists GameServers of one namespace on one cluster. *kube.Registry implements it.
type ServerLister interface {
	ListServers(ctx context.Context, cluster, ns string) ([]unstructured.Unstructured, error)
}

// Permission keys for file operations. These are the only place they appear for file routes.
const (
	filePermRead  = "servers:read"
	filePermWrite = "servers:write"
)

// FileChecker is the current FileAccess implementation: the namespace permission first, then the
// owner/collaborator grant on the exact server.
type FileChecker struct {
	fetch ServerFetcher
	list  ServerLister
}

// NewFileAccess returns the FileAccess used by the API. fetch resolves servers for the owner and
// collaborator fallback; list enumerates servers for VisibleServers. Either may be nil, which
// disables the feature that needs it.
func NewFileAccess(fetch ServerFetcher, list ServerLister) *FileChecker {
	return &FileChecker{fetch: fetch, list: list}
}

// filePermission maps an action to the permission it needs. Read-class actions need servers:read,
// everything that changes files needs servers:write (the same split the rule table made for GET
// and non-GET requests under /servers).
func filePermission(action FileAction) (string, bool) {
	switch action {
	case FileList, FileStat, FileRead, FileDownload:
		return filePermRead, true
	case FileCreate, FileWrite, FileUpload, FileRename, FileMkdir, FileDelete:
		return filePermWrite, true
	}
	return "", false
}

// Allow returns ErrFileAccessDenied unless user may perform action on target.
func (c *FileChecker) Allow(ctx context.Context, user *auth.User, action FileAction, target FileTarget) error {
	_, err := c.AllowBound(ctx, user, action, target)
	return err
}

// AllowBound is Allow that also returns the context carrying the bound owner/collaborator grant
// (bindServerGrant), which the proxy and the stream revalidation need. When the grant comes from
// the namespace permission, the returned context is the input context.
func (c *FileChecker) AllowBound(ctx context.Context, user *auth.User, action FileAction, target FileTarget) (context.Context, error) {
	if user == nil {
		return ctx, ErrFileAccessDenied
	}
	if action == FileRename {
		// The route-level check (no paths yet) gates on write-class access; the rename handler
		// passes [from, to] and checks delete on from and write on to.
		if len(target.Paths) == 0 {
			return c.allowOne(ctx, user, filePermWrite, target)
		}
		if len(target.Paths) != 2 {
			return ctx, ErrFileAccessDenied
		}
		// Delete on from, then write on to. Both are write-class today; spec 039 may split them.
		bound, err := c.allowOne(ctx, user, filePermWrite, target)
		if err != nil {
			return ctx, err
		}
		return c.allowOne(bound, user, filePermWrite, target)
	}
	perm, ok := filePermission(action)
	if !ok {
		return ctx, ErrFileAccessDenied
	}
	return c.allowOne(ctx, user, perm, target)
}

// allowOne checks one permission on target, then the owner/collaborator fallback.
func (c *FileChecker) allowOne(ctx context.Context, user *auth.User, perm string, target FileTarget) (context.Context, error) {
	if user.Can(perm, true, target.Cluster, target.Namespace) {
		return ctx, nil
	}
	if c.fetch != nil {
		if bound, ok := bindServerGrant(ctx, c.fetch, target.Cluster, target.Namespace, target.Server, user.ID, false); ok {
			return bound, nil
		}
	}
	return ctx, ErrFileAccessDenied
}

// FilterEntries returns names unchanged (directory listings are not filtered per entry today). It
// still fails when the user may not list the target.
func (c *FileChecker) FilterEntries(ctx context.Context, user *auth.User, target FileTarget, names []string) ([]string, error) {
	if err := c.Allow(ctx, user, FileList, target); err != nil {
		return nil, err
	}
	return names, nil
}

// VisibleServers lists the servers on cluster the user may see, sorted by namespace then name.
func (c *FileChecker) VisibleServers(ctx context.Context, user *auth.User, cluster string) ([]ServerRef, error) {
	if user == nil {
		return nil, ErrFileAccessDenied
	}
	if c.fetch == nil || !containsID(c.fetch.IDs(), cluster) {
		return nil, scope.ErrForbiddenCluster
	}
	if c.list == nil {
		return nil, errors.New("file access: server lister not configured")
	}
	var out []ServerRef
	for _, ns := range scope.AllowedNamespaces {
		items, err := c.list.ListServers(ctx, cluster, ns)
		if err != nil {
			return nil, fmt.Errorf("list servers in %s: %w", ns, err)
		}
		readAll := user.Can(filePermRead, true, cluster, ns)
		for i := range items {
			obj := &items[i]
			if obj.GetDeletionTimestamp() != nil {
				continue
			}
			if !readAll && ownershipRole(obj, user.ID) == roleNone {
				continue
			}
			out = append(out, ServerRef{Cluster: cluster, Namespace: ns, Name: obj.GetName()})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func containsID(ids []string, id string) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

// fileRouteRe matches /servers/{name}/files/{action}. The name cannot contain "/" or ":".
var fileRouteRe = regexp.MustCompile(`^/servers/([^/:]+)/files/([a-z]+)$`)

// fileRouteActions maps method and action segment to the FileAction the route performs.
//
// POST archive is write-class, as it was under the old rule table (any non-GET under /servers
// needed servers:write), and GET archive is read-class. The FileAction list has no archive
// action yet; spec 039 decides it.
var fileRouteActions = map[string]map[string]FileAction{
	http.MethodGet: {
		"list": FileList, "stat": FileStat, "read": FileRead,
		"download": FileDownload, "archive": FileDownload,
	},
	http.MethodPost: {
		"archive": FileWrite, "write": FileWrite, "create": FileCreate,
		"upload": FileUpload, "mkdir": FileMkdir, "rename": FileRename,
	},
	http.MethodDelete: {"delete": FileDelete},
	http.MethodPut:    {"stream": FileUpload},
}

// FileActionForRoute reports whether method and path are one of the server file routes, and if so
// which action and server name they target. ok is false for every other request, which keeps the
// rule table's decision.
func FileActionForRoute(method, path string) (action FileAction, server string, ok bool) {
	m := fileRouteRe.FindStringSubmatch(path)
	if m == nil {
		return "", "", false
	}
	act, found := fileRouteActions[method][m[2]]
	if !found {
		return "", "", false
	}
	return act, m[1], true
}
