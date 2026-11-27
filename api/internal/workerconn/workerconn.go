// Package workerconn sends the first request to a freshly started archive
// worker (spec 041). The operator reports a worker ready as soon as its Pod
// is Ready, but the worker's ClusterIP Service only routes to it once its
// endpoints are published and programmed on the node, so the first dial can
// be refused for a moment. Do retries that refusal for a short window.
package workerconn

import (
	"context"
	"errors"
	"io"
	"net/http"
	"syscall"
	"time"
)

const (
	// RetryWindow bounds how long Do keeps retrying a refused connection.
	RetryWindow = 15 * time.Second
	// RetryInterval is the pause between two attempts.
	RetryInterval = 250 * time.Millisecond
)

// Do sends the request newRequest builds with ctx and retries it while the worker
// refuses the connection, for at most RetryWindow. body, when not nil, is
// handed to every request it builds and is retried only while none of it has
// been read, so a streamed upload is never sent twice. The returned error is
// the last attempt's.
func Do(
	ctx context.Context,
	send func(*http.Request) (*http.Response, error),
	newRequest func(ctx context.Context, body io.Reader) (*http.Request, error),
	body io.Reader,
) (*http.Response, error) {
	return do(ctx, send, newRequest, body, RetryWindow, RetryInterval)
}

func do(
	ctx context.Context,
	send func(*http.Request) (*http.Response, error),
	newRequest func(ctx context.Context, body io.Reader) (*http.Request, error),
	body io.Reader,
	window, interval time.Duration,
) (*http.Response, error) {
	var tracked *readTracker
	if body != nil {
		tracked = &readTracker{r: body}
	}
	deadline := time.Now().Add(window)
	for {
		var reqBody io.Reader
		if tracked != nil {
			reqBody = tracked
		}
		req, err := newRequest(ctx, reqBody)
		if err != nil {
			return nil, err
		}
		resp, err := send(req)
		if err == nil {
			return resp, nil
		}
		if !errors.Is(err, syscall.ECONNREFUSED) || (tracked != nil && tracked.read) || !time.Now().Add(interval).Before(deadline) {
			return nil, err
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, err
		case <-timer.C:
		}
	}
}

// readTracker records whether any byte of the body was read. It is not an
// io.Closer, so a failed attempt cannot close the caller's body.
type readTracker struct {
	r    io.Reader
	read bool
}

func (t *readTracker) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if n > 0 {
		t.read = true
	}
	return n, err
}
