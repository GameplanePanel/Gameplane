package steam

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestSingleflightCollapse(t *testing.T) {
	// Test that N concurrent requests for the same ids collapse to ONE upstream call.
	callCount := 0
	var mu sync.Mutex

	sf := &singleflightGroup{}

	n := 10
	resultChan := make(chan map[string]string, n)
	errChan := make(chan error, n)

	ids := []string{"id1", "id2", "id3"}

	// Launch N goroutines requesting the same ids.
	for i := 0; i < n; i++ {
		go func() {
			res, err := sf.Do(context.Background(), ids, func(_ context.Context) (map[string]string, error) {
				mu.Lock()
				callCount++
				mu.Unlock()

				// Simulate a slow upstream call.
				time.Sleep(10 * time.Millisecond)

				return map[string]string{
					"id1": "Player1",
					"id2": "Player2",
					"id3": "Player3",
				}, nil
			})
			resultChan <- res
			errChan <- err
		}()
	}

	// Wait for all to complete.
	for i := 0; i < n; i++ {
		res := <-resultChan
		err := <-errChan

		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}

		if len(res) != 3 {
			t.Errorf("expected 3 results, got %d", len(res))
		}

		for _, id := range ids {
			if _, ok := res[id]; !ok {
				t.Errorf("expected id %s in result", id)
			}
		}
	}

	// Verify only one upstream call was made.
	if callCount != 1 {
		t.Errorf("expected 1 upstream call, got %d", callCount)
	}
}

func TestSingleflightDisjointSetsNotCollapsed(t *testing.T) {
	// Test that disjoint id sets are NOT collapsed.
	callCount := 0
	var mu sync.Mutex

	sf := &singleflightGroup{}

	// Goroutine 1: request ids {id1, id2}
	go func() {
		sf.Do(context.Background(), []string{"id1", "id2"}, func(_ context.Context) (map[string]string, error) {
			mu.Lock()
			callCount++
			mu.Unlock()
			return map[string]string{}, nil
		})
	}()

	// Let goroutine 1 start.
	time.Sleep(5 * time.Millisecond)

	// Goroutine 2: request ids {id3, id4}
	sf.Do(context.Background(), []string{"id3", "id4"}, func(_ context.Context) (map[string]string, error) {
		mu.Lock()
		callCount++
		mu.Unlock()
		return map[string]string{}, nil
	})

	// Verify two calls were made (not collapsed).
	if callCount != 2 {
		t.Errorf("expected 2 upstream calls for disjoint sets, got %d", callCount)
	}
}

func TestSingleflightErrorFansOut(t *testing.T) {
	// Test that an upstream error is returned to all waiters.
	callCount := 0
	var mu sync.Mutex

	sf := &singleflightGroup{}

	n := 5
	errChan := make(chan error, n)

	ids := []string{"id1", "id2"}

	for i := 0; i < n; i++ {
		go func() {
			_, err := sf.Do(context.Background(), ids, func(_ context.Context) (map[string]string, error) {
				mu.Lock()
				callCount++
				mu.Unlock()

				// Simulate a slow upstream call to keep the flight in-flight
				// long enough for other callers to join the same flight.
				time.Sleep(10 * time.Millisecond)

				return nil, fmt.Errorf("upstream failed")
			})
			errChan <- err
		}()
	}

	// Wait for all to complete.
	for i := 0; i < n; i++ {
		err := <-errChan
		if err == nil {
			t.Error("expected error, got nil")
		}
	}

	// Only one upstream call should have been made.
	if callCount != 1 {
		t.Errorf("expected 1 upstream call, got %d", callCount)
	}
}

func TestSingleflightCancelledWaiterDoesNotAffectOthers(t *testing.T) {
	// One waiter cancels its context while the shared flight is in progress.
	// The flight must not be cancelled, and every waiter (including the cancelled one)
	// must receive the same result.
	sf := &singleflightGroup{}
	ids := []string{"id1", "id2"}
	const n = 5
	want := map[string]string{"id1": "Player1", "id2": "Player2"}

	release := make(chan struct{})
	var mu sync.Mutex
	callCount := 0
	var flightCtxErr error

	cancelCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type outcome struct {
		res map[string]string
		err error
	}
	outcomes := make(chan outcome, n)

	var started sync.WaitGroup
	for i := 0; i < n; i++ {
		ctx := context.Background()
		if i == 0 {
			ctx = cancelCtx
		}
		started.Add(1)
		go func() {
			started.Done()
			res, err := sf.Do(ctx, ids, func(fctx context.Context) (map[string]string, error) {
				mu.Lock()
				callCount++
				mu.Unlock()

				<-release

				// Record whether the shared flight observed a cancellation.
				mu.Lock()
				flightCtxErr = fctx.Err()
				mu.Unlock()
				return map[string]string{"id1": "Player1", "id2": "Player2"}, nil
			})
			outcomes <- outcome{res, err}
		}()
	}

	// Wait for every goroutine to start, then give them time to join the flight.
	started.Wait()
	time.Sleep(10 * time.Millisecond)

	// Cancel the first waiter's context while the flight is still blocked, then let it finish.
	cancel()
	close(release)

	for i := 0; i < n; i++ {
		o := <-outcomes
		if o.err != nil {
			t.Errorf("waiter %d: expected nil error, got %v", i, o.err)
			continue
		}
		if len(o.res) != len(want) {
			t.Errorf("waiter %d: expected %d results, got %v", i, len(want), o.res)
			continue
		}
		for id, val := range want {
			if o.res[id] != val {
				t.Errorf("waiter %d: expected %s=%s, got %q", i, id, val, o.res[id])
			}
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if callCount != 1 {
		t.Errorf("expected 1 upstream call, got %d", callCount)
	}
	if flightCtxErr != nil {
		t.Errorf("shared flight context was cancelled by a single waiter: %v", flightCtxErr)
	}
}

func TestSingleflightSecondCallerJoinsInFlight(t *testing.T) {
	// Test that a second caller joining an in-flight call receives the shared result
	// without triggering a second upstream invocation.
	callCount := 0
	var mu sync.Mutex
	callStarted := make(chan struct{})
	callBlocked := make(chan struct{})
	done := make(chan struct{})

	sf := &singleflightGroup{}

	ids := []string{"id1"}

	// Goroutine 1: starts the call and blocks until we tell it to proceed.
	var result1 map[string]string
	var err1 error

	go func() {
		result1, err1 = sf.Do(context.Background(), ids, func(_ context.Context) (map[string]string, error) {
			mu.Lock()
			callCount++
			mu.Unlock()

			callStarted <- struct{}{}
			<-callBlocked // Wait for signal to proceed

			return map[string]string{"id1": "Player1"}, nil
		})
		close(done)
	}()

	// Wait for the call to start.
	<-callStarted

	// Goroutine 2: request the same ids.
	resultChan := make(chan map[string]string, 1)
	errChan := make(chan error, 1)

	go func() {
		res, err := sf.Do(context.Background(), ids, func(_ context.Context) (map[string]string, error) {
			// Should not reach here; should reuse the in-flight call.
			return nil, fmt.Errorf("unexpected call")
		})
		resultChan <- res
		errChan <- err
	}()

	// Give goroutine 2 time to join the flight.
	time.Sleep(10 * time.Millisecond)

	// Release the shared flight before reading results.
	// This allows goroutine 1 to complete and deliver the result to goroutine 2.
	callBlocked <- struct{}{}

	// Wait for goroutine 2's result.
	result2 := <-resultChan
	err2 := <-errChan

	if err2 != nil {
		t.Errorf("expected no error for goroutine 2, got %v", err2)
	}

	if result2 == nil || result2["id1"] != "Player1" {
		t.Errorf("expected valid result for goroutine 2, got %v", result2)
	}

	// Wait for goroutine 1 to complete.
	<-done

	// Goroutine 1 should succeed.
	if err1 != nil {
		t.Errorf("expected success for goroutine 1, got %v", err1)
	}

	if result1 == nil || result1["id1"] != "Player1" {
		t.Error("expected valid result for goroutine 1")
	}

	// Only one upstream call should have been made.
	if callCount != 1 {
		t.Errorf("expected 1 upstream call, got %d", callCount)
	}
}
