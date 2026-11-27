package workerconn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"syscall"
	"testing"
	"time"
)

func refused() error {
	return &url.Error{Op: "Get", URL: "https://worker:9443/", Err: fmt.Errorf("dial tcp: %w", syscall.ECONNREFUSED)}
}

func getRequest(ctx context.Context, body io.Reader) (*http.Request, error) {
	return http.NewRequestWithContext(ctx, http.MethodGet, "https://worker:9443/export/uid", body)
}

func TestDoRetriesRefusedConnection(t *testing.T) {
	calls := 0
	send := func(*http.Request) (*http.Response, error) {
		calls++
		if calls < 3 {
			return nil, refused()
		}
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
	}
	resp, err := do(context.Background(), send, getRequest, nil, time.Second, time.Millisecond)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK || calls != 3 {
		t.Fatalf("status %d after %d calls, want 200 after 3", resp.StatusCode, calls)
	}
}

func TestDoStopsOnOtherErrors(t *testing.T) {
	calls := 0
	other := errors.New("tls: bad certificate")
	send := func(*http.Request) (*http.Response, error) {
		calls++
		return nil, other
	}
	resp, err := do(context.Background(), send, getRequest, nil, time.Second, time.Millisecond)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if !errors.Is(err, other) {
		t.Fatalf("err = %v, want %v", err, other)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func TestDoGivesUpAfterWindow(t *testing.T) {
	calls := 0
	send := func(*http.Request) (*http.Response, error) {
		calls++
		return nil, refused()
	}
	resp, err := do(context.Background(), send, getRequest, nil, 20*time.Millisecond, 5*time.Millisecond)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if !errors.Is(err, syscall.ECONNREFUSED) {
		t.Fatalf("err = %v, want connection refused", err)
	}
	if calls < 2 {
		t.Fatalf("calls = %d, want retries before giving up", calls)
	}
}

func TestDoStopsWhenContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	send := func(*http.Request) (*http.Response, error) {
		cancel()
		return nil, refused()
	}
	resp, err := do(ctx, send, getRequest, nil, time.Hour, time.Minute)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if !errors.Is(err, syscall.ECONNREFUSED) {
		t.Fatalf("err = %v, want connection refused", err)
	}
}

func TestDoNeverResendsAReadBody(t *testing.T) {
	calls := 0
	send := func(req *http.Request) (*http.Response, error) {
		calls++
		buf := make([]byte, 1)
		_, _ = req.Body.Read(buf)
		return nil, refused()
	}
	resp, err := do(context.Background(), send, getRequest, strings.NewReader("archive"), time.Second, time.Millisecond)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err == nil {
		t.Fatal("Do succeeded, want the refusal")
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 once the body was read", calls)
	}
}

func TestDoRetriesUnreadBodyAndDeliversIt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_, _ = w.Write(b)
	}))
	defer srv.Close()
	calls := 0
	send := func(req *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return nil, refused()
		}
		return srv.Client().Do(req)
	}
	newRequest := func(ctx context.Context, body io.Reader) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, srv.URL, body)
		if err != nil {
			return nil, err
		}
		req.ContentLength = int64(len("archive"))
		return req, nil
	}
	resp, err := do(context.Background(), send, newRequest, strings.NewReader("archive"), time.Second, time.Millisecond)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	got, _ := io.ReadAll(resp.Body)
	if string(got) != "archive" || calls != 2 {
		t.Fatalf("body %q after %d calls, want archive after 2", got, calls)
	}
}

func TestDoReturnsRequestBuildErrors(t *testing.T) {
	bad := errors.New("bad url")
	newRequest := func(context.Context, io.Reader) (*http.Request, error) { return nil, bad }
	send := func(*http.Request) (*http.Response, error) {
		t.Fatal("send called")
		return nil, nil
	}
	resp, err := Do(context.Background(), send, newRequest, nil)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if !errors.Is(err, bad) {
		t.Fatalf("err = %v, want %v", err, bad)
	}
}
