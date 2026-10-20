package kube

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
)

// stdinAttachTimeout is the BACKSTOP for one WriteStdinLines call — it only
// bites when the attach negotiation hangs. The normal case ends much sooner:
// see the drain path below. Unlike the operator's stop-attach (whose command
// exits the game and closes the stream), a quick-action command like "say" or
// "save" does NOT terminate the game, so the attach session never EOF-closes
// on its own. Waiting this whole timeout on every action would delay the
// response ~10s and read as a hang.
const stdinAttachTimeout = 10 * time.Second

// stdinFlushGrace is how long we let the SPDY stream flush after remotecommand
// has consumed all our stdin bytes, before we tear the session down. The bytes
// are handed to the stream synchronously; this covers the wire hop to the
// kubelet. Kept short so a stdin action's response is near-instant (the spec's
// "reports sent"), not paced by stdinAttachTimeout.
const stdinFlushGrace = 250 * time.Millisecond

// WriteStdinLines attaches to a game container's stdin via pods/attach and
// writes each line followed by \n, then tears the session down. Fire-and-
// forget: pod attach doesn't EOF-close for a non-terminating command, so once
// our bytes are drained into the stream we give it a brief flush grace and
// cancel, rather than blocking on a session end that never comes. Mirrors the
// operator's stop-attach (gameserver_stop_attach.go) and the Console tab's
// attach shape (api/internal/ws/attach.go) — TTY:true, Stdout requested +
// discarded — a tty/non-tty mismatch behaves differently across runtimes.
func (c *Client) WriteStdinLines(ctx context.Context, ns, pod, container string, lines []string) error {
	url := c.Typed.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(pod).
		Namespace(ns).
		SubResource("attach").
		VersionedParams(&corev1.PodAttachOptions{
			Container: container,
			Stdin:     true,
			Stdout:    true,
			Stderr:    false, // merged with stdout under TTY
			TTY:       true,
		}, scheme.ParameterCodec).
		URL()

	exec, err := c.NewSPDYExecutor("POST", url)
	if err != nil {
		return fmt.Errorf("build stdin attach executor for pod %s/%s: %w", ns, pod, err)
	}

	var buf bytes.Buffer
	for _, line := range lines {
		buf.WriteString(line)
		buf.WriteString("\n")
	}

	// stdinAttachTimeout is only the backstop for a hung negotiation. The
	// normal case ends via the drain path: notifyOnEOF closes drained once
	// remotecommand has read every byte, and the goroutine then grants a short
	// flush grace and cancels — attach never EOF-closes for a command that
	// doesn't terminate the game, so we can't wait for the remote to end it.
	streamCtx, cancel := context.WithTimeout(ctx, stdinAttachTimeout)
	defer cancel()

	drained := make(chan struct{})
	stdin := &notifyOnEOF{r: &buf, drained: drained}

	go func() {
		select {
		case <-drained:
			t := time.NewTimer(stdinFlushGrace)
			defer t.Stop()
			select {
			case <-t.C:
			case <-streamCtx.Done():
			}
			cancel()
		case <-streamCtx.Done():
		}
	}()

	err = exec.StreamWithContext(streamCtx, remotecommand.StreamOptions{
		Stdin:  stdin,
		Stdout: io.Discard,
		Tty:    true,
	})
	// We end the session deliberately once the write has flushed, so a
	// context cancel/deadline is the expected success path, not a failure.
	if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("write stdin to pod %s/%s: %w", ns, pod, err)
	}
	return nil
}

// notifyOnEOF wraps a reader and closes drained the first time the underlying
// reader reports EOF — i.e. once remotecommand has consumed all the stdin
// bytes we intend to write. Reads after EOF keep returning EOF; drained is
// closed exactly once.
type notifyOnEOF struct {
	r       io.Reader
	drained chan struct{}
	done    bool
}

func (n *notifyOnEOF) Read(p []byte) (int, error) {
	nr, err := n.r.Read(p)
	if errors.Is(err, io.EOF) && !n.done {
		n.done = true
		close(n.drained)
	}
	return nr, err
}
