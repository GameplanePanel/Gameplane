package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ktesting "k8s.io/client-go/testing"
)

func TestWakeRetrySharedTransientRecovery(t *testing.T) {
	w, client := newFakeGameServerWaker(t, "alpha", "games", time.Minute, nil)
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	var firstPatch []byte
	client.PrependReactor("patch", "gameservers", func(action ktesting.Action) (bool, runtime.Object, error) {
		patch := action.(ktesting.PatchAction).GetPatch()
		if calls.Add(1) == 1 {
			firstPatch = bytes.Clone(patch)
			close(started)
			<-release
			return true, nil, apierrors.NewServiceUnavailable("outage")
		}
		if !bytes.Equal(firstPatch, patch) {
			t.Error("shared retry changed the annotation token or patch bytes")
		}
		return false, nil, nil
	})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	results := make(chan error, 9)
	go func() { results <- w.RequestWake(t.Context()) }()
	waitLoadEvent(t, started, "first shared PATCH")
	for range 8 {
		waiting := &observedWaitContext{Context: t.Context(), observed: make(chan struct{})}
		go func() { results <- w.RequestWake(waiting) }()
		waitLoadEvent(t, waiting.observed, "shared waiter")
	}
	close(release)
	for range 9 {
		if err := receiveLoadEvent(t, results, "shared retry result"); err != nil {
			t.Fatalf("shared wake: %v", err)
		}
	}
	if err := w.RequestWake(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("PATCH calls = %d, want one failed and one successful shared attempt", got)
	}
}

func TestWakeRetryAttemptLimitAndErrorIdentity(t *testing.T) {
	failure := apierrors.NewServiceUnavailable("still unavailable")
	calls := 0
	err := retryWakePatch(t.Context(), func(context.Context) error {
		calls++
		return failure
	})
	var status *apierrors.StatusError
	if calls != 4 || !errors.Is(err, failure) || !errors.As(err, &status) {
		t.Fatalf("calls=%d error=%v, want four attempts and original StatusError", calls, err)
	}
}

func TestWakeRetryErrorClassification(t *testing.T) {
	resource := schema.GroupResource{Group: "gameplane.local", Resource: "gameservers"}
	for _, tc := range []struct {
		name  string
		err   error
		retry bool
	}{
		{"unavailable", apierrors.NewServiceUnavailable("outage"), true},
		{"throttled", apierrors.NewTooManyRequests("busy", 0), true},
		{"timeout", apierrors.NewTimeoutError("slow", 0), true},
		{"server timeout", apierrors.NewServerTimeout(resource, "patch", 0), true},
		{"internal", apierrors.NewInternalError(errors.New("temporary")), true},
		{"EOF", io.EOF, true},
		{"unexpected EOF", io.ErrUnexpectedEOF, true},
		{"reset", fmt.Errorf("transport: %w", syscall.ECONNRESET), true},
		{"refused", &url.Error{Op: "Patch", URL: "http://localhost", Err: syscall.ECONNREFUSED}, true},
		{"timeout transport", &net.DNSError{Err: "timeout", Name: "api", IsTimeout: true}, true},
		{"forbidden", apierrors.NewForbidden(resource, "alpha", errors.New("denied")), false},
		{"unauthorized", apierrors.NewUnauthorized("denied"), false},
		{"missing", apierrors.NewNotFound(resource, "alpha"), false},
		{"invalid", apierrors.NewInvalid(schema.GroupKind{Group: resource.Group, Kind: "GameServer"}, "alpha", nil), false},
		{"bad request", apierrors.NewBadRequest("bad patch"), false},
		{"unknown", errors.New("API unavailable"), false},
		{"permanent DNS", &net.DNSError{Err: "not found", Name: "api", IsNotFound: true}, false},
		{"canceled", context.Canceled, false},
		{"deadline", context.DeadlineExceeded, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := retryableWakeError(fmt.Errorf("PATCH: %w", tc.err)); got != tc.retry {
				t.Fatalf("retryable = %v, want %v for %v", got, tc.retry, tc.err)
			}
			if !tc.retry {
				calls := 0
				err := retryWakePatch(t.Context(), func(context.Context) error { calls++; return tc.err })
				if calls != 1 || !errors.Is(err, tc.err) {
					t.Fatalf("permanent error calls=%d result=%v", calls, err)
				}
			}
		})
	}
}

func TestWakeRetryCancellationDuringBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	failure := apierrors.NewServiceUnavailable("outage")
	calls := 0
	// Cancel synchronously when the helper evaluates Done for its backoff.
	// No scheduler delay can let a second PATCH race the test's cancellation.
	err := retryWakePatch(cancelBackoffContext{Context: ctx, cancel: cancel}, func(context.Context) error { calls++; return failure })
	if calls != 1 || !errors.Is(err, context.Canceled) || !errors.Is(err, failure) {
		t.Fatalf("calls=%d error=%v, want cancellation and last API error without another PATCH", calls, err)
	}
}

type cancelBackoffContext struct {
	context.Context
	cancel context.CancelFunc
}

func (c cancelBackoffContext) Done() <-chan struct{} {
	c.cancel()
	return c.Context.Done()
}

func TestWakeRetryDeadlineAndRetryAfter(t *testing.T) {
	// Longer than the first ordinary backoff, shorter than the server's hint.
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	failure := apierrors.NewTooManyRequests("busy", 1)
	calls := 0
	err := retryWakePatch(ctx, func(context.Context) error { calls++; return failure })
	if calls != 1 || !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, failure) {
		t.Fatalf("calls=%d error=%v, retry-after must respect total deadline", calls, err)
	}
}

func TestWakeRetryCanceledBeforeFirstAttempt(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := retryWakePatch(ctx, func(context.Context) error {
		t.Error("canceled caller issued a PATCH")
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want canceled", err)
	}
}

func TestWakeRetryDelayBounds(t *testing.T) {
	for _, tc := range []struct {
		failure   int
		low, high time.Duration
	}{
		{0, 75 * time.Millisecond, 125 * time.Millisecond},
		{1, 150 * time.Millisecond, 250 * time.Millisecond},
		{2, 300 * time.Millisecond, 500 * time.Millisecond},
		{3, 600 * time.Millisecond, 800 * time.Millisecond},
		{20, 600 * time.Millisecond, 800 * time.Millisecond},
	} {
		for _, fraction := range []float64{0, 0.25, 0.5, 0.75, 1} {
			got := wakeRetryDelay(tc.failure, nil, fraction)
			if got < tc.low || got > tc.high {
				t.Errorf("failure=%d jitter=%v delay=%v, want [%v,%v]", tc.failure, fraction, got, tc.low, tc.high)
			}
		}
		if got := wakeRetryDelay(tc.failure, nil, 0); got != tc.low {
			t.Errorf("failure=%d minimum=%v, want %v", tc.failure, got, tc.low)
		}
		if got := wakeRetryDelay(tc.failure, nil, 1); got != tc.high {
			t.Errorf("failure=%d maximum=%v, want %v", tc.failure, got, tc.high)
		}
	}
	for _, fraction := range []float64{0, 0.5, 1} {
		if got := wakeRetryDelay(0, apierrors.NewTooManyRequests("busy", 2), fraction); got != 2*time.Second {
			t.Errorf("server retry-after=%v, want 2s", got)
		}
	}
}
