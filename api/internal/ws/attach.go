package ws

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"

	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// mountAttach exposes a WebSocket that bridges the browser to a running
// game container's stdin/stdout via the Kubernetes pod-attach API.
//
// The pod must have been started with tty=true and stdin=true on the
// "game" container — the operator does this when GameTemplate
// .spec.consoleMode == "pty". Attempting to attach to a container that
// wasn't started with a TTY succeeds at the API level but produces a
// degraded experience (no resize, raw bytes, no shell line editing).
//
// Wire protocol on the WS:
//   - browser → server: {"kind":"stdin","body":"<base64>"} or
//     {"kind":"resize","cols":N,"rows":M}
//   - server → browser: {"kind":"stdout","body":"<base64>"} or
//     {"kind":"err","body":"<message>"}
//
// stderr is intentionally merged into stdout — that's what the kubelet
// produces under TTY=true, and the xterm.js front end has no use for a
// separate stream.
func mountAttach(r chi.Router, reg *kube.Registry) {
	r.Get("/ws/servers/{name}/console-pty", func(w http.ResponseWriter, req *http.Request) {
		k, ok := streamClient(w, req, reg)
		if !ok {
			return
		}
		if k.Config == nil {
			http.Error(w, "cluster unavailable", http.StatusServiceUnavailable)
			return
		}
		(&attachProxy{k: k}).handle(w, req)
	})
}

type attachProxy struct {
	k *kube.Client
}

type ptyEnvelope struct {
	Kind string `json:"kind"`
	Body string `json:"body,omitempty"` // base64-encoded for stdin/stdout
	Cols uint16 `json:"cols,omitempty"`
	Rows uint16 `json:"rows,omitempty"`
}

func (a *attachProxy) handle(w http.ResponseWriter, req *http.Request) {
	name := chi.URLParam(req, "name")
	ns, err := scope.Resolve(req)
	if err != nil {
		httperr.Write(w, req, err)
		return
	}

	// StatefulSet replica naming. The operator pins replicas=1 for game
	// servers so the -0 suffix is the only pod that ever exists for a
	// given GameServer.
	if len(validation.IsDNS1123Subdomain(name)) != 0 {
		http.Error(w, "invalid server name", http.StatusBadRequest)
		return
	}
	pod, err := serverPod(req.Context(), a.k, ns, name)
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	podName := pod.Name

	wsConn, err := websocket.Accept(w, req, nil)
	if err != nil {
		return
	}
	defer func() { _ = wsConn.Close(websocket.StatusNormalClosure, "") }()

	// Bound the lifetime of the attach + the I/O goroutines to the WS.
	// Closing wsConn cancels Read; cancelling ctx tears down the SPDY
	// stream from the executor side. Either side closing collapses both.
	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()

	url := a.k.Typed.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(ns).
		SubResource("attach").
		VersionedParams(&corev1.PodAttachOptions{
			Container: "game",
			Stdin:     true,
			Stdout:    true,
			Stderr:    false, // merged with stdout under TTY
			TTY:       true,
		}, scheme.ParameterCodec).
		URL()

	exec, err := a.k.NewSPDYExecutor("POST", url)
	if err != nil {
		slog.Error("build attach executor", "name", name, "ns", ns, "err", err)
		writeEnvErr(ctx, wsConn, "attach unavailable")
		return
	}

	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	sizes := newTermSizeQueue()

	// stdout pump: read raw bytes from the SPDY stream and frame each
	// chunk as a base64 envelope on the WS.
	go pumpStdout(ctx, wsConn, stdoutR)

	// browser pump: parse envelopes, feed stdin and resize.
	go pumpBrowser(ctx, wsConn, stdinW, sizes, cancel)

	streamErr := exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:             stdinR,
		Stdout:            stdoutW,
		Tty:               true,
		TerminalSizeQueue: sizes,
	})

	// Closing the writer ends the stdout pump; closing stdinR signals
	// the browser pump to exit on its next write attempt.
	_ = stdoutW.Close()
	_ = stdinR.Close()
	sizes.close()

	if streamErr != nil && !errors.Is(streamErr, context.Canceled) {
		slog.Info("attach stream ended", "name", name, "ns", ns, "err", streamErr)
		writeEnvErr(ctx, wsConn, "attach stream ended")
	}
}

func pumpStdout(ctx context.Context, ws *websocket.Conn, src io.Reader) {
	buf := make([]byte, 4096)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			env := ptyEnvelope{
				Kind: "stdout",
				Body: base64.StdEncoding.EncodeToString(buf[:n]),
			}
			data, mErr := json.Marshal(env)
			if mErr != nil {
				return
			}
			if wErr := ws.Write(ctx, websocket.MessageText, data); wErr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func pumpBrowser(ctx context.Context, ws *websocket.Conn, stdin io.WriteCloser, sizes *termSizeQueue, cancel context.CancelFunc) {
	// On exit, close stdin so the executor sees EOF and unblocks.
	defer func() {
		_ = stdin.Close()
		cancel()
	}()
	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			return
		}
		var env ptyEnvelope
		if err := json.Unmarshal(data, &env); err != nil {
			// Malformed envelope — ignore the frame rather than tearing
			// down the session over a single bad message.
			continue
		}
		switch env.Kind {
		case "stdin":
			raw, dErr := base64.StdEncoding.DecodeString(env.Body)
			if dErr != nil {
				continue
			}
			if _, wErr := stdin.Write(raw); wErr != nil {
				return
			}
		case "resize":
			if env.Cols == 0 || env.Rows == 0 {
				continue
			}
			sizes.push(remotecommand.TerminalSize{Width: env.Cols, Height: env.Rows})
		default:
			// Unknown kind — drop. Keeps the protocol forward-compatible
			// if the frontend learns new envelope types.
		}
	}
}

func writeEnvErr(ctx context.Context, ws *websocket.Conn, msg string) {
	env := ptyEnvelope{Kind: "err", Body: msg}
	data, err := json.Marshal(env)
	if err != nil {
		return
	}
	_ = ws.Write(ctx, websocket.MessageText, data)
}

// termSizeQueue is a tiny, drop-on-overflow queue that feeds resize
// events into the executor's TerminalSizeQueue. Non-blocking pushes
// keep the WS read loop snappy when the user resizes faster than
// remotecommand consumes.
type termSizeQueue struct {
	ch     chan remotecommand.TerminalSize
	closed chan struct{}
}

func newTermSizeQueue() *termSizeQueue {
	return &termSizeQueue{
		ch:     make(chan remotecommand.TerminalSize, 4),
		closed: make(chan struct{}),
	}
}

func (q *termSizeQueue) Next() *remotecommand.TerminalSize {
	select {
	case s := <-q.ch:
		return &s
	case <-q.closed:
		return nil
	}
}

func (q *termSizeQueue) push(s remotecommand.TerminalSize) {
	select {
	case q.ch <- s:
	default:
		// Drop — newer events supersede older ones anyway.
	}
}

func (q *termSizeQueue) close() {
	select {
	case <-q.closed:
		// already closed
	default:
		close(q.closed)
	}
}
