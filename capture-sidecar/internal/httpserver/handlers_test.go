package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/capture-sidecar/internal/capture"
	"github.com/gopacket/gopacket/pcapgo"
)

// ---------------------------------------------------------------------------
// Test doubles
// ---------------------------------------------------------------------------

// fakeSource is a synthetic capture.PacketSource. It hands out queued frames
// and then idles until the capture's context is canceled or it is closed, so
// the handler tests can drive the real packet path with no root, no NIC and
// no CAP_NET_RAW.
type fakeSource struct {
	mu      sync.Mutex
	queue   [][]byte
	closed  bool
	readErr error
}

func (f *fakeSource) feed(frames ...[]byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queue = append(f.queue, frames...)
}

// failWith makes every subsequent read fail, simulating a link going down
// mid-capture.
func (f *fakeSource) failWith(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.readErr = err
}

func (f *fakeSource) ReadPacket(ctx context.Context) (*capture.RawPacket, error) {
	for {
		f.mu.Lock()
		if f.readErr != nil {
			err := f.readErr
			f.mu.Unlock()
			return nil, err
		}
		if len(f.queue) > 0 {
			data := f.queue[0]
			f.queue = f.queue[1:]
			f.mu.Unlock()
			return &capture.RawPacket{Data: data, Timestamp: time.Now()}, nil
		}
		closed := f.closed
		f.mu.Unlock()

		if closed {
			return nil, errors.New("fake source closed")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Millisecond):
		}
	}
}

func (f *fakeSource) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

// sourceFactory hands each capture its own fakeSource and remembers them, so a
// test can feed the source that belongs to the capture it just started.
type sourceFactory struct {
	mu      sync.Mutex
	opened  []*fakeSource
	openErr error
}

func (sf *sourceFactory) open(_, _ string) (capture.PacketSource, error) {
	sf.mu.Lock()
	defer sf.mu.Unlock()
	if sf.openErr != nil {
		return nil, sf.openErr
	}
	src := &fakeSource{}
	sf.opened = append(sf.opened, src)
	return src, nil
}

func (sf *sourceFactory) last(t *testing.T) *fakeSource {
	t.Helper()
	sf.mu.Lock()
	defer sf.mu.Unlock()
	if len(sf.opened) == 0 {
		t.Fatal("no packet source was opened")
	}
	return sf.opened[len(sf.opened)-1]
}

func (sf *sourceFactory) count() int {
	sf.mu.Lock()
	defer sf.mu.Unlock()
	return len(sf.opened)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newTestServer(t *testing.T) (*Server, *sourceFactory) {
	t.Helper()
	// budgetBytes 0 disables the F-187 volume-budget check: unrelated tests
	// use captureFilePath-derived fixtures that would otherwise interact
	// with it. See newTestServerWithBudget for tests that exercise it.
	srv := NewServer(context.Background(), t.TempDir(), 0)
	factory := &sourceFactory{}
	srv.newSource = factory.open
	t.Cleanup(func() {
		// Never leave an armed duration timer and a live packet goroutine
		// behind: they would outlive the test and pollute a later leak check.
		srv.mu.Lock()
		state := srv.currentCapture
		srv.mu.Unlock()
		if state != nil {
			srv.finish("", state, reasonUserRequested, nil)
			<-state.done
		}
	})
	return srv, factory
}

func startBody(filter string, durationSeconds, sizeBytes int64) string {
	return fmt.Sprintf(`{"filter":%q,"maxDurationSeconds":%d,"maxSizeBytes":%d}`, filter, durationSeconds, sizeBytes)
}

func doStart(t *testing.T, s *Server, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/captures/"+id+"/start", strings.NewReader(body))
	req.SetPathValue("id", id)
	rr := httptest.NewRecorder()
	s.HandleStart(rr, req)
	return rr
}

func doStop(t *testing.T, s *Server, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/captures/"+id+"/stop", strings.NewReader(body))
	req.SetPathValue("id", id)
	rr := httptest.NewRecorder()
	s.HandleStop(rr, req)
	return rr
}

func doStatus(t *testing.T, s *Server, id string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/captures/"+id+"/status", nil)
	req.SetPathValue("id", id)
	rr := httptest.NewRecorder()
	s.HandleStatus(rr, req)
	return rr
}

func doDownload(t *testing.T, s *Server, id string, rangeHeader string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/captures/"+id+"/file", nil)
	req.SetPathValue("id", id)
	if rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}
	rr := httptest.NewRecorder()
	s.HandleDownload(rr, req)
	return rr
}

func doDelete(t *testing.T, s *Server, id string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/captures/"+id, nil)
	req.SetPathValue("id", id)
	rr := httptest.NewRecorder()
	s.HandleDelete(rr, req)
	return rr
}

func decodeStop(t *testing.T, rr *httptest.ResponseRecorder) stopResponse {
	t.Helper()
	var resp stopResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode stop response: %v", err)
	}
	return resp
}

func decodeCompleted(t *testing.T, rr *httptest.ResponseRecorder) statusResponseCompleted {
	t.Helper()
	var resp statusResponseCompleted
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode completed status: %v", err)
	}
	return resp
}

// frame builds a fixed-size synthetic Ethernet frame filled with marker.
func frame(marker byte) []byte {
	data := make([]byte, 64)
	for i := range data {
		data[i] = marker
	}
	return data
}

// waitForPackets polls the status endpoint until the capture reports at least
// want packets, so a test never races the packet-reading goroutine.
func waitForPackets(t *testing.T, s *Server, id string, want int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rr := doStatus(t, s, id)
		if rr.Code == http.StatusOK {
			var resp statusResponseRunning
			if err := json.NewDecoder(rr.Body).Decode(&resp); err == nil && resp.PacketsWritten >= want {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("capture %s never reported %d packets", id, want)
}

// waitForTerminal polls until the capture leaves the running state.
func waitForTerminal(t *testing.T, s *Server, id string) statusResponseCompleted {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rr := doStatus(t, s, id)
		if rr.Code == http.StatusOK && !strings.Contains(rr.Body.String(), `"status":"running"`) {
			return decodeCompleted(t, rr)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("capture %s never reached a terminal state", id)
	return statusResponseCompleted{}
}

// readPCAPNG re-reads a finalized capture with gopacket's PCAPNG reader, which
// is an implementation independent of the writer's own bookkeeping.
func readPCAPNG(t *testing.T, path string) [][]byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open capture file: %v", err)
	}
	defer func() { _ = f.Close() }()

	reader, err := pcapgo.NewNgReader(f, pcapgo.DefaultNgReaderOptions)
	if err != nil {
		t.Fatalf("parse PCAPNG: %v", err)
	}
	var packets [][]byte
	for {
		data, _, err := reader.ReadPacketData()
		if errors.Is(err, io.EOF) {
			return packets
		}
		if err != nil {
			t.Fatalf("read PCAPNG packet %d: %v", len(packets), err)
		}
		packets = append(packets, data)
	}
}

// ---------------------------------------------------------------------------
// Routing
// ---------------------------------------------------------------------------

// TestRoutes_PatternsAreValid exercises the real ServeMux. net/http validates
// route patterns at registration time and panics on an invalid one - a
// wildcard segment must be exactly "{name}", so the chi-style "{id}:start"
// suffix is fatal here - and calling the handler functions directly, as the
// rest of this file does, can never catch that.
func TestRoutes_PatternsAreValid(t *testing.T) {
	srv, factory := newTestServer(t)
	mux := srv.Routes(nil)

	t.Run("healthz", func(t *testing.T) {
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil))
		if rr.Code != http.StatusOK || rr.Body.String() != "ok" {
			t.Fatalf("healthz = %d %q", rr.Code, rr.Body.String())
		}
	})

	t.Run("start extracts the id wildcard", func(t *testing.T) {
		rr := httptest.NewRecorder()
		body := strings.NewReader(startBody("tcp port 8080", 300, 1000000))
		mux.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/captures/cap-route/start", body))
		if rr.Code != http.StatusOK {
			t.Fatalf("start via mux = %d, body %q", rr.Code, rr.Body.String())
		}
		var resp startResponse
		if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp.CaptureID != "cap-route" {
			t.Fatalf("captureId = %q, want cap-route", resp.CaptureID)
		}
		if factory.count() != 1 {
			t.Fatalf("opened %d packet sources, want 1", factory.count())
		}
	})

	t.Run("status and file route to their handlers", func(t *testing.T) {
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/captures/cap-route/status", nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("status via mux = %d", rr.Code)
		}

		rr = httptest.NewRecorder()
		mux.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/captures/cap-route/file", nil))
		if rr.Code != http.StatusConflict {
			t.Fatalf("file via mux = %d, want 409 while running", rr.Code)
		}
	})

	t.Run("stop finishes the capture", func(t *testing.T) {
		rr := httptest.NewRecorder()
		body := strings.NewReader(`{"reason":"user_requested"}`)
		mux.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/captures/cap-route/stop", body))
		if rr.Code != http.StatusOK {
			t.Fatalf("stop via mux = %d, body %q", rr.Code, rr.Body.String())
		}
	})

	t.Run("the old chi-style colon paths are not routes", func(t *testing.T) {
		rr := httptest.NewRecorder()
		body := strings.NewReader(startBody("tcp port 8080", 300, 1000000))
		mux.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/captures/cap-x:start", body))
		// The colon path /captures/cap-x:start is a two-segment path that matches the
		// DELETE /captures/{id} pattern (with id="cap-x:start"), but is rejected on method.
		// This produces 405 Method Not Allowed, proving the colon path is not a start route.
		// We accept either 404 (not found) or 405 (method not allowed) as evidence the
		// colon path did not reach HandleStart.
		if rr.Code != http.StatusNotFound && rr.Code != http.StatusMethodNotAllowed {
			t.Fatalf("colon-suffix start = %d, want 404 or 405", rr.Code)
		}
	})
}

func TestRoutes_MiddlewareWrapsEveryCaptureEndpoint(t *testing.T) {
	srv, _ := newTestServer(t)

	var wrapped int
	mux := srv.Routes(func(_ http.Handler) http.Handler {
		wrapped++
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTeapot)
		})
	})
	routes := []struct{ method, path string }{
		{http.MethodPost, "/captures/cap-1/start"},
		{http.MethodPost, "/captures/cap-1/stop"},
		{http.MethodGet, "/captures/cap-1/status"},
		{http.MethodGet, "/captures/cap-1/file"},
		{http.MethodDelete, "/captures/cap-1"},
		{http.MethodGet, "/v1/targets/server-uid/captures/cap-1/uids/capture-uid/file"},
		{http.MethodDelete, "/v1/targets/server-uid/captures/cap-1/uids/capture-uid/file"},
	}
	if wrapped != len(routes) {
		t.Fatalf("middleware applied to %d handlers, want %d", wrapped, len(routes))
	}
	for _, route := range routes {
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), route.method, route.path, nil))
		if rr.Code != http.StatusTeapot {
			t.Fatalf("%s %s bypassed the middleware: %d", route.method, route.path, rr.Code)
		}
	}

	// /healthz must stay unauthenticated.
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("healthz = %d, want 200 (unauthenticated)", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// Start
// ---------------------------------------------------------------------------

func TestHandleStart_InvalidBody(t *testing.T) {
	srv, _ := newTestServer(t)
	rr := doStart(t, srv, "cap-test", "invalid json")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestHandleStart_InvalidID(t *testing.T) {
	srv, _ := newTestServer(t)
	for _, id := range []string{"", ".", "..", "a/b", `a\b`, "cap test", strings.Repeat("a", 65)} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/captures/x/start", strings.NewReader(startBody("tcp port 1", 5, 100)))
		req.SetPathValue("id", id)
		rr := httptest.NewRecorder()
		srv.HandleStart(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("id %q: status = %d, want 400", id, rr.Code)
		}
	}
}

func TestHandleStart_LimitsOutOfRange(t *testing.T) {
	srv, _ := newTestServer(t)

	cases := []struct {
		name     string
		duration int64
		size     int64
	}{
		{"zero duration", 0, 1000000},
		{"negative duration", -1, 1000000},
		{"duration beyond the sidecar cap", maxDurationSecondsLimit + 1, 1000000},
		{"duration that overflows int64 nanoseconds", 1 << 62, 1000000},
		{"zero size", 300, 0},
		{"negative size", 300, -1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := doStart(t, srv, "cap-test", startBody("tcp port 8080", tc.duration, tc.size))
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rr.Code)
			}
		})
	}
}

func TestHandleStart_InvalidFilterRejected(t *testing.T) {
	srv, factory := newTestServer(t)
	rr := doStart(t, srv, "cap-test", startBody("tcp port notaport", 300, 1000000))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
	if factory.count() != 0 {
		t.Fatal("a packet source was opened for an invalid filter")
	}
}

// TestHandleStart_EmptyFilterRejected pins FR-003's boundary: the sidecar
// cannot construct the default advertised-port filter (it does not know the
// ports), and "no filter" must never be allowed to mean "capture the whole pod
// network", which would put the agent's and API's traffic into a downloadable
// file.
func TestHandleStart_EmptyFilterRejected(t *testing.T) {
	srv, factory := newTestServer(t)
	rr := doStart(t, srv, "cap-test", startBody("", 300, 1000000))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "filter is required") {
		t.Fatalf("body = %q", rr.Body.String())
	}
	if factory.count() != 0 {
		t.Fatal("a packet source was opened without a filter")
	}
}

// TestHandleStart_SourceOpenFailure is the regression test for a capture that
// answered 200 while its AF_PACKET socket had failed to open: the user then
// got a valid, empty PCAPNG indistinguishable from "the filter matched
// nothing". The contract requires a clear error instead.
func TestHandleStart_SourceOpenFailure(t *testing.T) {
	srv, factory := newTestServer(t)
	factory.openErr = errors.New("open AF_PACKET socket on eth0: operation not permitted")

	rr := doStart(t, srv, "cap-test", startBody("tcp port 8080", 300, 1000000))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "operation not permitted") {
		t.Fatalf("body = %q; want the underlying error", rr.Body.String())
	}

	srv.mu.Lock()
	current := srv.currentCapture
	srv.mu.Unlock()
	if current != nil {
		t.Fatal("a failed start left a capture registered")
	}

	// The socket is opened before the file, so a failed start leaves no
	// orphan PCAPNG behind.
	if _, err := os.Stat(srv.captureFilePath("cap-test")); !os.IsNotExist(err) {
		t.Fatalf("stat capture file: %v; want it to be absent", err)
	}
}

func TestHandleStart_Success(t *testing.T) {
	srv, factory := newTestServer(t)
	rr := doStart(t, srv, "cap-test", startBody("tcp port 8080", 300, 1000000))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q", rr.Code, rr.Body.String())
	}

	var resp startResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != statusRunning || resp.CaptureID != "cap-test" {
		t.Fatalf("resp = %+v", resp)
	}
	if resp.BytesWritten != 0 || resp.PacketsWritten != 0 {
		t.Fatalf("counters = %d/%d, want 0/0", resp.BytesWritten, resp.PacketsWritten)
	}
	if factory.count() != 1 {
		t.Fatalf("opened %d packet sources, want 1", factory.count())
	}
}

func TestHandleStart_ConcurrentRejected(t *testing.T) {
	srv, factory := newTestServer(t)

	if rr := doStart(t, srv, "cap-first", startBody("tcp port 8080", 300, 1000000)); rr.Code != http.StatusOK {
		t.Fatalf("first start = %d", rr.Code)
	}
	rr := doStart(t, srv, "cap-second", startBody("tcp port 8080", 300, 1000000))
	if rr.Code != http.StatusConflict {
		t.Fatalf("second start = %d, want 409", rr.Code)
	}
	// F-192 regression: the 409 must name the capture that is actually
	// running (cap-first), not the rejected request's id (cap-second) -
	// otherwise the caller sees a capture that never started described as
	// "in progress".
	if body := rr.Body.String(); !strings.Contains(body, "cap-first") || strings.Contains(body, "cap-second") {
		t.Fatalf("409 body = %q; want it to name the running capture cap-first, not the rejected cap-second", body)
	}
	if factory.count() != 1 {
		t.Fatalf("opened %d packet sources; the rejected start must not open one", factory.count())
	}

	srv.mu.Lock()
	current := srv.currentCapture
	srv.mu.Unlock()
	if current == nil || current.id != "cap-first" {
		t.Fatal("the first capture was displaced")
	}
}

// ---------------------------------------------------------------------------
// Packet path
// ---------------------------------------------------------------------------

// TestCapture_WritesPacketsAndReadableFile drives the whole packet path:
// start, feed synthetic frames, stop, then verify the counters and re-read the
// finalized file with gopacket's PCAPNG reader.
func TestCapture_WritesPacketsAndReadableFile(t *testing.T) {
	srv, factory := newTestServer(t)

	if rr := doStart(t, srv, "cap-pkt", startBody("tcp port 8080", 300, 10000000)); rr.Code != http.StatusOK {
		t.Fatalf("start = %d, body %q", rr.Code, rr.Body.String())
	}

	frames := [][]byte{frame(0x11), frame(0x22), frame(0x33)}
	factory.last(t).feed(frames...)
	waitForPackets(t, srv, "cap-pkt", int64(len(frames)))

	resp := decodeStop(t, doStop(t, srv, "cap-pkt", `{"reason":"user_requested"}`))
	if resp.Status != statusCompleted {
		t.Fatalf("status = %q, want completed (message %q)", resp.Status, resp.Message)
	}
	if resp.PacketsWritten != int64(len(frames)) {
		t.Fatalf("packetsWritten = %d, want %d", resp.PacketsWritten, len(frames))
	}
	// At least the captured payload; the writer may additionally account for
	// per-packet PCAPNG block overhead.
	if want := int64(len(frames) * len(frames[0])); resp.BytesWritten < want {
		t.Fatalf("bytesWritten = %d, want at least %d", resp.BytesWritten, want)
	}
	if resp.StoppingReason != reasonUserRequested {
		t.Fatalf("stoppingReason = %q", resp.StoppingReason)
	}

	got := readPCAPNG(t, srv.captureFilePath("cap-pkt"))
	if len(got) != len(frames) {
		t.Fatalf("PCAPNG holds %d packets, want %d", len(got), len(frames))
	}
	for i, want := range frames {
		if string(got[i]) != string(want) {
			t.Fatalf("packet %d differs from what was captured", i)
		}
	}
}

// TestCapture_SizeLimitStopsImmediately pins the fix for a size-limited
// capture that used to stay "running" until the next packet arrived - on a
// quiet link, potentially until the duration timer fired.
func TestCapture_SizeLimitStopsImmediately(t *testing.T) {
	srv, factory := newTestServer(t)

	if rr := doStart(t, srv, "cap-size", startBody("tcp port 8080", 300, 100)); rr.Code != http.StatusOK {
		t.Fatalf("start = %d, body %q", rr.Code, rr.Body.String())
	}
	factory.last(t).feed(frame(0x01), frame(0x02))

	status := waitForTerminal(t, srv, "cap-size")
	if status.Status != statusCompleted {
		t.Fatalf("status = %q, want completed", status.Status)
	}
	if status.StoppingReason != "max_size_reached" {
		t.Fatalf("stoppingReason = %q, want max_size_reached", status.StoppingReason)
	}
	// Exactly how many of the two 64-byte frames fit under a 100-byte cap
	// depends on whether the writer accounts for PCAPNG block overhead, so pin
	// the invariant that matters: it stopped early, and the file is readable and
	// holds exactly what the counters claim.
	if status.PacketsWritten < 1 || status.PacketsWritten > 2 {
		t.Fatalf("packetsWritten = %d, want 1 or 2", status.PacketsWritten)
	}
	if got := int64(len(readPCAPNG(t, srv.captureFilePath("cap-size")))); got != status.PacketsWritten {
		t.Fatalf("PCAPNG holds %d packets but the status reports %d", got, status.PacketsWritten)
	}
}

// TestCapture_MidCaptureReadFailureIsReportedFailed pins that a capture whose
// packet source dies mid-run is reported failed, not completed: the file holds
// real packets but is truncated, and the operator must see the cause.
func TestCapture_MidCaptureReadFailureIsReportedFailed(t *testing.T) {
	srv, factory := newTestServer(t)

	if rr := doStart(t, srv, "cap-err", startBody("tcp port 8080", 300, 10000000)); rr.Code != http.StatusOK {
		t.Fatalf("start = %d", rr.Code)
	}
	src := factory.last(t)
	src.feed(frame(0x77))
	waitForPackets(t, srv, "cap-err", 1)
	src.failWith(errors.New("network is down"))

	status := waitForTerminal(t, srv, "cap-err")
	if status.Status != statusFailed {
		t.Fatalf("status = %q, want failed", status.Status)
	}
	if status.StoppingReason != reasonError {
		t.Fatalf("stoppingReason = %q, want %q", status.StoppingReason, reasonError)
	}
	if !strings.Contains(status.Message, "network is down") {
		t.Fatalf("message = %q; want the underlying cause", status.Message)
	}
	if status.PacketsWritten != 1 {
		t.Fatalf("packetsWritten = %d, want the packet captured before the failure", status.PacketsWritten)
	}
	if len(readPCAPNG(t, srv.captureFilePath("cap-err"))) != 1 {
		t.Fatal("the truncated capture is not a readable PCAPNG")
	}
}

// ---------------------------------------------------------------------------
// Stop
// ---------------------------------------------------------------------------

func TestHandleStop_NotFound(t *testing.T) {
	srv, _ := newTestServer(t)
	rr := doStop(t, srv, "nonexistent", `{"reason":"user_requested"}`)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestHandleStop_ChunkedBodyIsDecoded(t *testing.T) {
	srv, _ := newTestServer(t)
	if rr := doStart(t, srv, "cap-chunk", startBody("tcp port 8080", 300, 1000000)); rr.Code != http.StatusOK {
		t.Fatalf("start = %d", rr.Code)
	}

	// A client that assigns Body after building the request leaves
	// ContentLength at 0, and net/http then sends the body chunked, which the
	// server sees as ContentLength == -1. The reason must still be decoded.
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/captures/cap-chunk/stop", strings.NewReader(`{"reason":"pod_restarting"}`))
	req.SetPathValue("id", "cap-chunk")
	req.ContentLength = -1
	rr := httptest.NewRecorder()
	srv.HandleStop(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q", rr.Code, rr.Body.String())
	}
	if got := decodeStop(t, rr).StoppingReason; got != "pod_restarting" {
		t.Fatalf("stoppingReason = %q, want pod_restarting", got)
	}
}

func TestHandleStop_EmptyBodyDefaultsToUserRequested(t *testing.T) {
	srv, _ := newTestServer(t)
	if rr := doStart(t, srv, "cap-nobody", startBody("tcp port 8080", 300, 1000000)); rr.Code != http.StatusOK {
		t.Fatalf("start = %d", rr.Code)
	}
	rr := doStop(t, srv, "cap-nobody", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q", rr.Code, rr.Body.String())
	}
	if got := decodeStop(t, rr).StoppingReason; got != reasonUserRequested {
		t.Fatalf("stoppingReason = %q, want %q", got, reasonUserRequested)
	}
}

// TestHandleStop_ReplaysTerminalStateOfAFinishedCapture pins the fix for the
// bug where a finished capture was discarded: a stop that lost the race with
// the duration timer used to 404, destroying the counters and the reason.
func TestHandleStop_ReplaysTerminalStateOfAFinishedCapture(t *testing.T) {
	srv, factory := newTestServer(t)
	if rr := doStart(t, srv, "cap-twice", startBody("tcp port 8080", 300, 10000000)); rr.Code != http.StatusOK {
		t.Fatalf("start = %d", rr.Code)
	}
	factory.last(t).feed(frame(0x42))
	waitForPackets(t, srv, "cap-twice", 1)

	first := decodeStop(t, doStop(t, srv, "cap-twice", `{"reason":"user_requested"}`))

	rr := doStop(t, srv, "cap-twice", `{"reason":"user_requested"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("second stop = %d, want 200 with the stored result", rr.Code)
	}
	second := decodeStop(t, rr)
	if second.PacketsWritten != first.PacketsWritten || second.StoppingReason != first.StoppingReason {
		t.Fatalf("replayed %+v, want %+v", second, first)
	}
	if second.PacketsWritten != 1 {
		t.Fatalf("packetsWritten = %d, want 1", second.PacketsWritten)
	}
}

// ---------------------------------------------------------------------------
// Status
// ---------------------------------------------------------------------------

func TestHandleStatus_NotFound(t *testing.T) {
	srv, _ := newTestServer(t)
	if rr := doStatus(t, srv, "nonexistent"); rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestHandleStatus_Running(t *testing.T) {
	srv, _ := newTestServer(t)
	if rr := doStart(t, srv, "cap-test", startBody("tcp port 8080", 300, 1000000)); rr.Code != http.StatusOK {
		t.Fatalf("start = %d", rr.Code)
	}

	rr := doStatus(t, srv, "cap-test")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var resp statusResponseRunning
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != statusRunning || resp.CaptureID != "cap-test" {
		t.Fatalf("resp = %+v", resp)
	}
	if resp.CompletedAt != nil || resp.StoppingReason != nil {
		t.Fatalf("running response must null out completedAt/stoppingReason: %+v", resp)
	}
	if resp.EstimatedTimeRemainingSeconds <= 0 || resp.EstimatedTimeRemainingSeconds > 300 {
		t.Fatalf("estimatedTimeRemainingSeconds = %d", resp.EstimatedTimeRemainingSeconds)
	}
	// estimatedBytesRemaining must be maxSizeBytes minus the writer's real
	// on-disk bytesWritten (which, even before any packet is written, already
	// includes the PCAPNG section-header and interface-description blocks —
	// see capture.Writer.BytesWritten). Assert that relationship via the
	// response's own bytesWritten rather than a byte count copied from a test
	// run, which breaks every time block/header overhead changes.
	if resp.BytesWritten <= 0 {
		t.Fatalf("bytesWritten = %d, want > 0 (PCAPNG header blocks are written up front)", resp.BytesWritten)
	}
	if want := int64(1000000) - resp.BytesWritten; resp.EstimatedBytesRemaining != want {
		t.Fatalf("estimatedBytesRemaining = %d, want %d (maxSizeBytes - bytesWritten=%d)", resp.EstimatedBytesRemaining, want, resp.BytesWritten)
	}
	if resp.EstimatedBytesRemaining <= 0 || resp.EstimatedBytesRemaining >= 1000000 {
		t.Fatalf("estimatedBytesRemaining = %d, want in (0, 1000000)", resp.EstimatedBytesRemaining)
	}
}

// TestHandleStatus_Completed pins the contract's documented completed-status
// response, which used to be unreachable: finishing a capture discarded it, so
// every poll after completion returned 404 and the operator failed the
// NetworkCapture with "sidecar unreachable".
func TestHandleStatus_Completed(t *testing.T) {
	srv, factory := newTestServer(t)
	if rr := doStart(t, srv, "cap-test", startBody("tcp port 8080", 300, 10000000)); rr.Code != http.StatusOK {
		t.Fatalf("start = %d", rr.Code)
	}
	factory.last(t).feed(frame(0x55), frame(0x56))
	waitForPackets(t, srv, "cap-test", 2)

	if rr := doStop(t, srv, "cap-test", `{"reason":"user_requested"}`); rr.Code != http.StatusOK {
		t.Fatalf("stop = %d", rr.Code)
	}

	rr := doStatus(t, srv, "cap-test")
	if rr.Code != http.StatusOK {
		t.Fatalf("status of a completed capture = %d, want 200", rr.Code)
	}
	resp := decodeCompleted(t, rr)
	if resp.Status != statusCompleted {
		t.Fatalf("status = %q, want completed", resp.Status)
	}
	if resp.StoppingReason != reasonUserRequested {
		t.Fatalf("stoppingReason = %q", resp.StoppingReason)
	}
	if resp.PacketsWritten != 2 {
		t.Fatalf("packetsWritten = %d, want 2", resp.PacketsWritten)
	}
	if resp.CompletedAt == "" || resp.File != srv.captureFilePath("cap-test") {
		t.Fatalf("resp = %+v", resp)
	}
}

// TestDurationTimeout pins that the hard duration cap finalizes the capture
// and reports it as a normal completion with the right reason, rather than
// dropping it.
func TestDurationTimeout(t *testing.T) {
	srv, factory := newTestServer(t)
	if rr := doStart(t, srv, "cap-timeout", startBody("tcp port 8080", 1, 10000000)); rr.Code != http.StatusOK {
		t.Fatalf("start = %d", rr.Code)
	}
	factory.last(t).feed(frame(0x09))
	waitForPackets(t, srv, "cap-timeout", 1)

	resp := waitForTerminal(t, srv, "cap-timeout")
	if resp.Status != statusCompleted {
		t.Fatalf("status = %q, want completed (message %q)", resp.Status, resp.Message)
	}
	if resp.StoppingReason != reasonMaxDuration {
		t.Fatalf("stoppingReason = %q, want %q", resp.StoppingReason, reasonMaxDuration)
	}
	if resp.PacketsWritten != 1 {
		t.Fatalf("packetsWritten = %d, want 1", resp.PacketsWritten)
	}
	if len(readPCAPNG(t, srv.captureFilePath("cap-timeout"))) != 1 {
		t.Fatal("the duration-limited capture did not finalize a readable file")
	}

	srv.mu.Lock()
	current := srv.currentCapture
	srv.mu.Unlock()
	if current != nil {
		t.Fatal("the capture was not auto-stopped by the duration timer")
	}
}

// TestStatusPollsRaceWithFinish exercises the status path concurrently with a
// capture finishing. It is the -race regression test for reading a capture's
// mutable fields outside its mutex.
func TestStatusPollsRaceWithFinish(t *testing.T) {
	srv, _ := newTestServer(t)
	if rr := doStart(t, srv, "cap-race", startBody("tcp port 8080", 300, 1000000)); rr.Code != http.StatusOK {
		t.Fatalf("start = %d", rr.Code)
	}

	stopPolling := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stopPolling:
					return
				default:
				}
				req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/captures/cap-race/status", nil)
				req.SetPathValue("id", "cap-race")
				srv.HandleStatus(httptest.NewRecorder(), req)
			}
		}()
	}

	time.Sleep(20 * time.Millisecond)
	if rr := doStop(t, srv, "cap-race", `{"reason":"user_requested"}`); rr.Code != http.StatusOK {
		t.Fatalf("stop = %d", rr.Code)
	}
	close(stopPolling)
	wg.Wait()
}

// ---------------------------------------------------------------------------
// Download
// ---------------------------------------------------------------------------

func TestHandleDownload_NotFound(t *testing.T) {
	srv, _ := newTestServer(t)
	if rr := doDownload(t, srv, "nonexistent", ""); rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

// TestHandleDownload_GoneOnlyForAKnownCapture pins that 410 means "this
// sidecar ran this capture and its file is gone", not "any open error".
func TestHandleDownload_GoneOnlyForAKnownCapture(t *testing.T) {
	srv, _ := newTestServer(t)
	if rr := doStart(t, srv, "cap-gone", startBody("tcp port 8080", 300, 1000000)); rr.Code != http.StatusOK {
		t.Fatalf("start = %d", rr.Code)
	}
	if rr := doStop(t, srv, "cap-gone", ""); rr.Code != http.StatusOK {
		t.Fatalf("stop = %d", rr.Code)
	}
	if err := os.Remove(srv.captureFilePath("cap-gone")); err != nil {
		t.Fatalf("remove capture file: %v", err)
	}

	if rr := doDownload(t, srv, "cap-gone", ""); rr.Code != http.StatusGone {
		t.Fatalf("status = %d, want 410", rr.Code)
	}
	if rr := doDownload(t, srv, "cap-unknown", ""); rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for an unknown capture", rr.Code)
	}
}

func TestHandleDownload_StillRunning(t *testing.T) {
	srv, _ := newTestServer(t)
	if rr := doStart(t, srv, "cap-test", startBody("tcp port 8080", 300, 1000000)); rr.Code != http.StatusOK {
		t.Fatalf("start = %d", rr.Code)
	}
	if rr := doDownload(t, srv, "cap-test", ""); rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rr.Code)
	}
}

func TestHandleDownload_Success(t *testing.T) {
	srv, _ := newTestServer(t)
	payload := []byte("PCAPNG test data")
	if err := os.WriteFile(srv.captureFilePath("cap-test"), payload, 0o600); err != nil {
		t.Fatalf("write capture file: %v", err)
	}

	rr := doDownload(t, srv, "cap-test", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/vnd.tcpdump.pcap" {
		t.Fatalf("Content-Type = %q", ct)
	}
	if cd := rr.Header().Get("Content-Disposition"); cd != `attachment; filename="capture-cap-test.pcapng"` {
		t.Fatalf("Content-Disposition = %q", cd)
	}
	if rr.Header().Get("Accept-Ranges") != "bytes" {
		t.Fatalf("Accept-Ranges = %q", rr.Header().Get("Accept-Ranges"))
	}
	if rr.Body.String() != string(payload) {
		t.Fatalf("body = %q", rr.Body.String())
	}
}

// TestHandleDownload_HonoursRange pins the fix for advertising Accept-Ranges
// while ignoring Range: a resuming client used to silently receive the file
// from byte 0 and corrupt its download.
func TestHandleDownload_HonoursRange(t *testing.T) {
	srv, _ := newTestServer(t)
	payload := []byte("0123456789")
	if err := os.WriteFile(srv.captureFilePath("cap-range"), payload, 0o600); err != nil {
		t.Fatalf("write capture file: %v", err)
	}

	rr := doDownload(t, srv, "cap-range", "bytes=4-7")
	if rr.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", rr.Code)
	}
	if rr.Body.String() != "4567" {
		t.Fatalf("body = %q, want %q", rr.Body.String(), "4567")
	}
	if got := rr.Header().Get("Content-Range"); got != "bytes 4-7/10" {
		t.Fatalf("Content-Range = %q", got)
	}

	rr = doDownload(t, srv, "cap-range", "bytes=99-200")
	if rr.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("status = %d, want 416", rr.Code)
	}
}

func TestHandleDownload_StreamsLargeFile(t *testing.T) {
	srv, _ := newTestServer(t)
	payload := make([]byte, 1024*1024)
	for i := range payload {
		payload[i] = byte(i % 256)
	}
	if err := os.WriteFile(srv.captureFilePath("cap-large"), payload, 0o600); err != nil {
		t.Fatalf("write capture file: %v", err)
	}

	rr := doDownload(t, srv, "cap-large", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if rr.Body.Len() != len(payload) {
		t.Fatalf("body = %d bytes, want %d", rr.Body.Len(), len(payload))
	}
}

// ---------------------------------------------------------------------------
// Delete
// ---------------------------------------------------------------------------

func TestHandleDelete_InvalidID(t *testing.T) {
	srv, _ := newTestServer(t)
	for _, id := range []string{"", ".", "..", "a/b", `a\b`, "cap test", strings.Repeat("a", 65)} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/captures/x", nil)
		req.SetPathValue("id", id)
		rr := httptest.NewRecorder()
		srv.HandleDelete(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("id %q: status = %d, want 400", id, rr.Code)
		}
	}
}

func TestHandleDelete_Success(t *testing.T) {
	srv, _ := newTestServer(t)
	filePath := srv.captureFilePath("cap-test")
	if err := os.WriteFile(filePath, []byte("test data"), 0o600); err != nil {
		t.Fatalf("write capture file: %v", err)
	}

	if _, err := os.Stat(filePath); err != nil {
		t.Fatalf("stat before delete: %v", err)
	}

	rr := doDelete(t, srv, "cap-test")
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rr.Code)
	}

	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Fatalf("stat after delete: %v; want ErrNotExist", err)
	}
}

func TestHandleDelete_Idempotent(t *testing.T) {
	srv, _ := newTestServer(t)
	filePath := srv.captureFilePath("cap-test")
	if err := os.WriteFile(filePath, []byte("test data"), 0o600); err != nil {
		t.Fatalf("write capture file: %v", err)
	}

	// First delete should succeed.
	rr := doDelete(t, srv, "cap-test")
	if rr.Code != http.StatusNoContent {
		t.Fatalf("first delete status = %d, want 204", rr.Code)
	}

	// Second delete (file already gone) should also succeed.
	rr = doDelete(t, srv, "cap-test")
	if rr.Code != http.StatusNoContent {
		t.Fatalf("second delete status = %d, want 204", rr.Code)
	}
}

func TestHandleDelete_RefusesRunningCapture(t *testing.T) {
	srv, _ := newTestServer(t)
	if rr := doStart(t, srv, "cap-test", startBody("tcp port 8080", 300, 1000000)); rr.Code != http.StatusOK {
		t.Fatalf("start = %d", rr.Code)
	}

	rr := doDelete(t, srv, "cap-test")
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rr.Code)
	}

	// File should still exist.
	if _, err := os.Stat(srv.captureFilePath("cap-test")); err != nil {
		t.Fatalf("stat capture file: %v; the running capture should protect it", err)
	}
}

func TestHandleDelete_ViaRouterMux(t *testing.T) {
	srv, _ := newTestServer(t)
	mux := srv.Routes(nil)
	filePath := srv.captureFilePath("cap-route-delete")
	if err := os.WriteFile(filePath, []byte("test data"), 0o600); err != nil {
		t.Fatalf("write capture file: %v", err)
	}

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/captures/cap-route-delete", nil))
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete via mux = %d, want 204", rr.Code)
	}

	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Fatalf("file still exists after delete via mux")
	}
}

// ---------------------------------------------------------------------------
// Lifecycle
// ---------------------------------------------------------------------------

// TestCaptureGoroutinesDoNotLeak actually counts goroutines, which the test
// that previously carried this name did not.
func TestCaptureGoroutinesDoNotLeak(t *testing.T) {
	srv, _ := newTestServer(t)
	before := runtime.NumGoroutine()

	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("cap-leak-%d", i)
		if rr := doStart(t, srv, id, startBody("tcp port 8080", 300, 1000000)); rr.Code != http.StatusOK {
			t.Fatalf("iteration %d: start = %d", i, rr.Code)
		}
		if rr := doStop(t, srv, id, `{"reason":"cleanup"}`); rr.Code != http.StatusOK {
			t.Fatalf("iteration %d: stop = %d", i, rr.Code)
		}
	}

	srv.mu.Lock()
	current := srv.currentCapture
	srv.mu.Unlock()
	if current != nil {
		t.Fatal("a capture is still registered after all stops")
	}

	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > before+2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if after := runtime.NumGoroutine(); after > before+2 {
		t.Fatalf("goroutines: %d before, %d after; the capture goroutines leaked", before, after)
	}
}

// TestStaleDurationTimerCannotKillASuccessor pins that a timer left over from
// a finished capture never terminates a later capture that reuses its id.
func TestStaleDurationTimerCannotKillASuccessor(t *testing.T) {
	srv, _ := newTestServer(t)
	if rr := doStart(t, srv, "cap-reuse", startBody("tcp port 8080", 1, 1000000)); rr.Code != http.StatusOK {
		t.Fatalf("first start = %d", rr.Code)
	}

	srv.mu.Lock()
	first := srv.currentCapture
	srv.mu.Unlock()
	if first == nil {
		t.Fatal("no capture registered")
	}

	if rr := doStop(t, srv, "cap-reuse", `{"reason":"user_requested"}`); rr.Code != http.StatusOK {
		t.Fatalf("stop = %d", rr.Code)
	}
	if rr := doStart(t, srv, "cap-reuse", startBody("tcp port 8080", 300, 1000000)); rr.Code != http.StatusOK {
		t.Fatalf("second start = %d", rr.Code)
	}

	// Fire the first capture's timer callback by hand: it must be a no-op.
	srv.finish("", first, reasonMaxDuration, nil)

	srv.mu.Lock()
	current := srv.currentCapture
	srv.mu.Unlock()
	if current == nil {
		t.Fatal("the stale timer terminated the successor capture")
	}

	rr := doStatus(t, srv, "cap-reuse")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"status":"running"`) {
		t.Fatalf("successor status = %d %q", rr.Code, rr.Body.String())
	}
}

// TestCaptureENOSPCDuringWritePacketReportsDiskFull pins the fix for
// ENOSPC from WritePacket being reported as a generic "error" instead of
// "disk_full". This happens when bufio flush inside WritePacket fails,
// and is distinguished from other write errors by checking both the
// error chain and the writer's LimitReason.
func TestCaptureENOSPCDuringWritePacketReportsDiskFull(t *testing.T) {
	srv, factory := newTestServer(t)
	if rr := doStart(t, srv, "cap-enospc", startBody("tcp port 8080", 300, 1000000)); rr.Code != http.StatusOK {
		t.Fatalf("start = %d", rr.Code)
	}

	// Feed one packet to establish the capture is running.
	factory.last(t).feed(frame(0x11))
	waitForPackets(t, srv, "cap-enospc", 1)

	// Replace the writer with a failing one that returns ENOSPC on WritePacket.
	srv.mu.Lock()
	state := srv.currentCapture
	srv.mu.Unlock()

	if state == nil {
		t.Fatal("no capture is running")
	}

	oldWriter := state.writer

	// Inject a writer that fails on the next WritePacket call.
	// We'll do this by swapping the writer with one that wraps the original
	// but fails on the next call.
	state.mu.Lock()
	state.writer = &failingWriter{inner: oldWriter, failWith: syscall.ENOSPC}
	state.mu.Unlock()

	// Feed another packet that will trigger the ENOSPC error.
	factory.last(t).feed(frame(0x22))

	// Wait for the capture to reach a terminal state.
	resp := waitForTerminal(t, srv, "cap-enospc")

	// Verify the failure was reported as disk_full, not error.
	if resp.Status != statusFailed {
		t.Fatalf("status = %q, want failed", resp.Status)
	}
	if resp.StoppingReason != reasonDiskFull {
		t.Fatalf("stoppingReason = %q, want %q", resp.StoppingReason, reasonDiskFull)
	}
	if resp.PacketsWritten < 1 {
		t.Fatalf("packetsWritten = %d, want >= 1 (the error occurred during write)", resp.PacketsWritten)
	}
}

// failingWriter wraps a capture.PacketWriter and fails on the next WritePacket call.
type failingWriter struct {
	inner     capture.PacketWriter
	failWith  error
	failCount int
	failAfter int
}

func (fw *failingWriter) WritePacket(pkt *capture.RawPacket) error {
	fw.failCount++
	if fw.failAfter == 0 || fw.failCount > fw.failAfter {
		if fw.failWith != nil {
			return fmt.Errorf("disk full during capture: %w", fw.failWith)
		}
	}
	return fw.inner.WritePacket(pkt)
}

func (fw *failingWriter) Close() error {
	return fw.inner.Close()
}

func (fw *failingWriter) BytesWritten() int64 {
	return fw.inner.BytesWritten()
}

func (fw *failingWriter) PacketsWritten() int64 {
	return fw.inner.PacketsWritten()
}

func (fw *failingWriter) IsLimitReached() bool {
	return fw.inner.IsLimitReached()
}

func (fw *failingWriter) LimitReason() string {
	return fw.inner.LimitReason()
}

// ---------------------------------------------------------------------------
// F-187: volume budget (retained + new capture vs. the emptyDir SizeLimit)
// ---------------------------------------------------------------------------

// newTestServerWithBudget is newTestServer with a non-zero volume budget, for
// the tests below that exercise HandleStart's F-187 admission check.
func newTestServerWithBudget(t *testing.T, budgetBytes int64) (*Server, *sourceFactory) {
	t.Helper()
	srv := NewServer(context.Background(), t.TempDir(), budgetBytes)
	factory := &sourceFactory{}
	srv.newSource = factory.open
	t.Cleanup(func() {
		srv.mu.Lock()
		state := srv.currentCapture
		srv.mu.Unlock()
		if state != nil {
			srv.finish("", state, reasonUserRequested, nil)
			<-state.done
		}
	})
	return srv, factory
}

// writeRetainedFile creates a retained capture's PCAPNG file directly on
// disk (simulating a capture that finished in an earlier pod lifetime and
// is still waiting out its retention window) reporting exactly n bytes via
// os.Stat. The file is created sparse (via Truncate rather than writing n
// actual bytes) so large fixtures used to exercise volume-budget refusals
// don't allocate or physically write gigabytes of real disk/memory in CI.
func writeRetainedFile(t *testing.T, srv *Server, id string, n int64) {
	t.Helper()
	f, err := os.Create(srv.captureFilePath(id))
	if err != nil {
		t.Fatalf("create retained fixture %s: %v", id, err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Fatalf("close retained fixture %s: %v", id, err)
		}
	}()
	if err := f.Truncate(n); err != nil {
		t.Fatalf("truncate retained fixture %s to %d bytes: %v", id, n, err)
	}
}

// TestHandleStart_VolumeBudget_RefusesWhenRetainedPlusNewExceedsBudget
// covers the core F-187 regression: retained files that the old check never
// looked at must count against the same budget as the new capture's own
// ceiling.
func TestHandleStart_VolumeBudget_RefusesWhenRetainedPlusNewExceedsBudget(t *testing.T) {
	const budget = 1000
	srv, factory := newTestServerWithBudget(t, budget)

	writeRetainedFile(t, srv, "cap-old", 700)

	rr := doStart(t, srv, "cap-new", startBody("tcp port 8080", 300, 400)) // 700+400 > 1000
	if rr.Code != http.StatusInsufficientStorage {
		t.Fatalf("status = %d, body %q; want 507", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "budget") {
		t.Fatalf("body = %q; want it to mention the volume budget", rr.Body.String())
	}
	if factory.count() != 0 {
		t.Fatalf("opened %d packet sources; a refused start must not open one", factory.count())
	}
	srv.mu.Lock()
	current := srv.currentCapture
	srv.mu.Unlock()
	if current != nil {
		t.Fatal("a refused start left a capture registered")
	}
}

// TestHandleStart_VolumeBudget_AllowsWhenItFits is the mirror case: the same
// retained file plus a new capture that fits under the budget must still be
// admitted.
func TestHandleStart_VolumeBudget_AllowsWhenItFits(t *testing.T) {
	const budget = 1000
	srv, _ := newTestServerWithBudget(t, budget)

	writeRetainedFile(t, srv, "cap-old", 700)

	rr := doStart(t, srv, "cap-new", startBody("tcp port 8080", 300, 300)) // 700+300 == 1000, fits
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q; want 200", rr.Code, rr.Body.String())
	}
}

// TestHandleStart_VolumeBudget_ExpiredFilesNotCounted covers the "expired
// files don't count" half of F-187: a capture file already deleted (expiry,
// in this system, is deletion - see retainedCaptureBytes' doc comment) must
// not be counted against the budget just because it once existed.
func TestHandleStart_VolumeBudget_ExpiredFilesNotCounted(t *testing.T) {
	const budget = 1000
	srv, _ := newTestServerWithBudget(t, budget)

	writeRetainedFile(t, srv, "cap-old", 700)
	writeRetainedFile(t, srv, "cap-expired", 900)
	// Simulate the operator's expiry reconciler deleting the retention-expired
	// file (expireCapture -> DeleteCaptureFile -> HandleDelete) before this
	// start request arrives.
	if err := os.Remove(srv.captureFilePath("cap-expired")); err != nil {
		t.Fatalf("remove expired fixture: %v", err)
	}

	// Without the deleted file counted, 700(retained)+300(new) fits exactly;
	// counting it (700+900+300) would refuse the start.
	rr := doStart(t, srv, "cap-new", startBody("tcp port 8080", 300, 300))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q; want 200 (expired file must not count)", rr.Code, rr.Body.String())
	}
}

// TestHandleStart_VolumeBudget_RefusesOnOversizedRequestWithoutOverflow
// covers the overflow regression in the admission check: a maxSizeBytes at
// math.MaxInt64 (what an unclamped NetworkCapture CR applied directly -
// bypassing the API tier's DefaultMaxSizeBytes clamp - can carry through to
// this handler as nc.Spec.MaxSize.Value()) added to any positive retained
// total wraps a signed int64 negative, which would make the naive
// `retained+req.MaxSizeBytes > s.budgetBytes` comparison spuriously false
// and admit a start that should be refused. The check must compare without
// adding.
func TestHandleStart_VolumeBudget_RefusesOnOversizedRequestWithoutOverflow(t *testing.T) {
	const budget = 1000
	srv, factory := newTestServerWithBudget(t, budget)

	writeRetainedFile(t, srv, "cap-old", 700)

	rr := doStart(t, srv, "cap-new", startBody("tcp port 8080", 300, math.MaxInt64))
	if rr.Code != http.StatusInsufficientStorage {
		t.Fatalf("status = %d, body %q; want 507 (must not overflow into admission)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "budget") {
		t.Fatalf("body = %q; want it to mention the volume budget", rr.Body.String())
	}
	if factory.count() != 0 {
		t.Fatalf("opened %d packet sources; a refused start must not open one", factory.count())
	}
}

// TestHandleStart_VolumeBudget_ZeroDisablesCheck documents that a zero
// budget (the value every other HandleStart test uses via newTestServer)
// disables the check entirely, regardless of what's on disk.
func TestHandleStart_VolumeBudget_ZeroDisablesCheck(t *testing.T) {
	srv, _ := newTestServer(t)

	writeRetainedFile(t, srv, "cap-old", 10*1024*1024*1024) // absurdly large; would refuse if checked

	rr := doStart(t, srv, "cap-new", startBody("tcp port 8080", 300, 1000000))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q; want 200 (budget check disabled)", rr.Code, rr.Body.String())
	}
}
