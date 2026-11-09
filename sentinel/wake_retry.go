package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

const (
	wakePatchAttempts = 4
	wakeRetryInitial  = 100 * time.Millisecond
	wakeRetryMaximum  = 800 * time.Millisecond
)

// retryWakePatch runs inside the shared wake operation's existing context.
// Neither an attempt nor a backoff renews that operation's total budget.
func retryWakePatch(ctx context.Context, patch func(context.Context) error) error {
	var lastErr error
	for attempt := range wakePatchAttempts {
		if err := ctx.Err(); err != nil {
			return errors.Join(err, lastErr)
		}
		lastErr = patch(ctx)
		if lastErr == nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return errors.Join(err, lastErr)
		}
		if !retryableWakeError(lastErr) || attempt == wakePatchAttempts-1 {
			return lastErr
		}
		var sample [1]byte
		if _, err := rand.Read(sample[:]); err != nil {
			return errors.Join(lastErr, fmt.Errorf("generate wake retry jitter: %w", err))
		}
		timer := time.NewTimer(wakeRetryDelay(attempt, lastErr, float64(sample[0])/255))
		select {
		case <-ctx.Done():
			timer.Stop()
			// Keep both cancellation and the last underlying API error usable
			// with errors.Is/As when the budget expires during backoff.
			return errors.Join(ctx.Err(), lastErr)
		case <-timer.C:
		}
	}
	return lastErr
}

func retryableWakeError(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if apierrors.IsServiceUnavailable(err) || apierrors.IsTooManyRequests(err) ||
		apierrors.IsTimeout(err) || apierrors.IsServerTimeout(err) || apierrors.IsInternalError(err) {
		return true
	}
	// A permanent API status must not become retryable because its cause also
	// happens to implement a network error interface.
	var status apierrors.APIStatus
	if errors.As(err, &status) {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ETIMEDOUT) || errors.Is(err, syscall.EPIPE) {
		return true
	}
	var networkErr net.Error
	return errors.As(err, &networkErr) && networkErr.Timeout()
}

// wakeRetryDelay uses +/-25% jitter, capped after jitter so even a positive
// sample never exceeds the local 800 ms cap. Server hints are a minimum and
// may exceed that cap; retryWakePatch still waits only within ctx's budget.
func wakeRetryDelay(failure int, err error, fraction float64) time.Duration {
	delay := wakeRetryInitial
	for i := 0; i < failure && delay < wakeRetryMaximum; i++ {
		delay *= 2
	}
	delay = time.Duration(float64(delay) * (0.75 + 0.5*fraction))
	if delay > wakeRetryMaximum {
		delay = wakeRetryMaximum
	}
	if seconds, ok := apierrors.SuggestsClientDelay(err); ok {
		if suggested := time.Duration(seconds) * time.Second; suggested > delay {
			delay = suggested
		}
	}
	return delay
}
