package ws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/messages"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// Annotations shared with the operator (controller.FilesRequestedAtAnnotation and
// controller.IdleAsleepSinceAnnotation). Duplicated on purpose: handlers/lifecycle.go does the same.
const (
	filesRequestedAtAnnotation = "gameplane.local/files-requested-at"
	filesIdleAsleepAnnotation  = "gameplane.local/idle-asleep-since"
)

const (
	// filesRequestInterval bounds how often a file request rewrites files-requested-at. The
	// operator's idle timer for the file-only pod restarts from that stamp.
	filesRequestInterval = time.Minute
	// filesReadyTimeout is how long a file request waits for the file-only pod before 503.
	filesReadyTimeout = 60 * time.Second
	filesPollInterval = time.Second
)

// errFilesStarting means the file-only pod did not answer within filesReadyTimeout.
var errFilesStarting = errors.New("files are starting")

// filesRoute wraps a file proxy so a stopped or asleep server gets a file-only pod first.
// The route-level RBAC has already run, so unauthorized callers never reach ensureFiles.
func (p *proxy) filesRoute(path string, limit int64) http.HandlerFunc {
	return p.agentRoute(func(selected *proxy) http.HandlerFunc {
		inner := selected.httpProxyLimit(path, limit)
		return func(w http.ResponseWriter, req *http.Request) {
			if !selected.ensureFiles(w, req) {
				return
			}
			inner(w, req)
		}
	})
}

// ensureFiles makes sure a stopped server answers file operations. It returns true when
// the proxy may continue. Otherwise it has already written the response.
func (p *proxy) ensureFiles(w http.ResponseWriter, req *http.Request) bool {
	return p.ensureFilesClocked(w, req, time.Now, time.Sleep)
}

// ensureFilesClocked is ensureFiles with an injected clock, so tests can run the 60 s wait.
func (p *proxy) ensureFilesClocked(w http.ResponseWriter, req *http.Request, now func() time.Time, sleep func(time.Duration)) bool {
	// Without a cluster client or agent transport, the proxy writes its usual error.
	if p.k == nil || p.k.Dynamic == nil || p.k.Typed == nil || p.transport == nil {
		return true
	}
	ns, err := scope.Resolve(req)
	if err != nil {
		httperr.Write(w, req, err)
		return false
	}
	err = p.ensureFilesWith(req.Context(), ns, chi.URLParam(req, "name"), now, sleep)
	switch {
	case err == nil:
		return true
	case errors.Is(err, errFilesStarting):
		w.Header().Set("Retry-After", "2")
		httperr.WriteMessage(w, req, messages.New(messages.ID(httperr.IDFilesStarting), nil))
	default:
		httperr.Write(w, req, err)
	}
	return false
}

// ensureFilesWith is the testable core of ensureFiles. It returns nil for a running server
// (after one GET), or once a stopped server's file-only pod answers. It returns
// errFilesStarting when that does not happen within filesReadyTimeout.
func (p *proxy) ensureFilesWith(ctx context.Context, ns, name string, now func() time.Time, sleep func(time.Duration)) error {
	gs, err := p.k.GetServer(ctx, ns, name)
	if err != nil {
		return fmt.Errorf("get files server: %w", err)
	}
	// The UID bound by agentRoute must still be this server. A same-named replacement gets
	// no file pod from this request.
	if p.remoteUID != "" && string(gs.GetUID()) != p.remoteUID {
		return apierrors.NewNotFound(kube.GVRs["servers"].GroupResource(), name)
	}
	if !filesServerStopped(gs) {
		return nil
	}
	if err := p.requestFilesPod(ctx, gs, now()); err != nil {
		return err
	}
	return p.awaitFilesPod(ctx, ns, name, now, sleep)
}

// filesServerStopped reports whether the server has no game pod to serve files: suspended,
// or idle-asleep (the operator scales the StatefulSet to zero in both cases).
func filesServerStopped(gs *unstructured.Unstructured) bool {
	suspended, _, _ := unstructured.NestedBool(gs.Object, "spec", "suspend")
	return suspended || gs.GetAnnotations()[filesIdleAsleepAnnotation] != ""
}

// requestFilesPod stamps files-requested-at, at most once per filesRequestInterval. The stamp
// is the only write: spec and every wake annotation are left alone, so a file request never
// wakes a server.
func (p *proxy) requestFilesPod(ctx context.Context, gs *unstructured.Unstructured, at time.Time) error {
	if filesRequestFresh(gs.GetAnnotations()[filesRequestedAtAnnotation], at) {
		return nil
	}
	body, err := json.Marshal(map[string]any{
		"metadata": map[string]any{
			"annotations": map[string]string{filesRequestedAtAnnotation: at.UTC().Format(time.RFC3339)},
		},
	})
	if err != nil {
		return fmt.Errorf("encode files request: %w", err)
	}
	if _, err := p.k.Dynamic.Resource(kube.GVRs["servers"]).
		Namespace(gs.GetNamespace()).
		Patch(ctx, gs.GetName(), types.MergePatchType, body, metav1.PatchOptions{}); err != nil {
		return fmt.Errorf("request files pod: %w", err)
	}
	return nil
}

// filesRequestFresh reports whether a files-requested-at stamp is recent enough that no new
// patch is needed. A missing, unparsable or far-future stamp is not fresh.
func filesRequestFresh(stamp string, at time.Time) bool {
	t, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return false
	}
	age := at.Sub(t)
	return age > -filesRequestInterval && age < filesRequestInterval
}

// awaitFilesPod polls until the game pod is gone and the agent answers, or the deadline passes.
func (p *proxy) awaitFilesPod(ctx context.Context, ns, name string, now func() time.Time, sleep func(time.Duration)) error {
	deadline := now().Add(filesReadyTimeout)
	for {
		if p.filesReady(ctx, ns, name) {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !now().Before(deadline) {
			return errFilesStarting
		}
		sleep(filesPollInterval)
	}
}

// filesReady is true when the game pod <name>-0 is NotFound (its volume is free) and the
// agent answers a stat of the root. Any error or 5xx means not ready yet.
func (p *proxy) filesReady(ctx context.Context, ns, name string) bool {
	_, err := p.k.Typed.CoreV1().Pods(ns).Get(ctx, name+"-0", metav1.GetOptions{})
	if err == nil || !apierrors.IsNotFound(err) {
		return false
	}
	resp, err := p.transport.Do(ctx, agentRequest{
		target: agentTarget{name: name, namespace: ns, uid: p.remoteUID},
		method: http.MethodGet,
		path:   "/files/stat",
		// The root always exists, so a healthy agent answers below 500.
		rawQuery: "path=%2F",
	})
	if err != nil {
		return false
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	_ = resp.Body.Close()
	return resp.StatusCode < http.StatusInternalServerError
}
