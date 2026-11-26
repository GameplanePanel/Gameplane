package archive

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// gapRandomSource is a source whose archive holds size incompressible bytes,
// large enough that the exporter's first response hold is exceeded.
func gapRandomSource(t *testing.T, size int) []byte {
	t.Helper()
	rng := rand.New(rand.NewSource(11))
	body := make([]byte, size)
	_, _ = rng.Read(body)
	return buildSource(t, []srcEntry{{
		hdr:  tar.Header{Name: "data/random.bin", Typeflag: tar.TypeReg, Mode: 0o644},
		body: string(body),
	}})
}

// gapFlakyWriter accepts okWrites body writes and then refuses the rest, as a
// client that drops mid-download would.
type gapFlakyWriter struct {
	*httptest.ResponseRecorder
	okWrites int
}

func (w *gapFlakyWriter) Write(p []byte) (int, error) {
	if w.okWrites <= 0 {
		return 0, errors.New("client gone")
	}
	w.okWrites--
	return w.ResponseRecorder.Write(p)
}

func TestRunDumpFailsWithCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out, errOut bytes.Buffer
	err := runDump(ctx, "snap1", &out, &errOut)
	if err == nil || !strings.Contains(err.Error(), "restic dump") {
		t.Fatalf("error = %v, want a restic dump failure", err)
	}
}

func TestExporterClientGoneBeforeResponseStartsIs502(t *testing.T) {
	setRunDump(t, fakeDump(gapRandomSource(t, 200<<10), "", nil))

	x := newTestExporter("tar.gz", nil)
	w := brokenWriter{httptest.NewRecorder()}
	x.ServeHTTP(w, exportRequest())

	if w.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502 when the first held bytes cannot be sent", w.Code)
	}
	if res := x.result(); !strings.Contains(res.Error, "client gone") {
		t.Errorf("result error = %q, want the client failure", res.Error)
	}
}

func TestExporterClientGoneMidStreamAbortsConnection(t *testing.T) {
	setRunDump(t, fakeDump(gapRandomSource(t, 200<<10), "", nil))

	aborted := false
	x := newTestExporter("tar.gz", func() { aborted = true })
	w := &gapFlakyWriter{ResponseRecorder: httptest.NewRecorder(), okWrites: 1}
	func() {
		defer func() {
			r := recover()
			if err, ok := r.(error); !ok || !errors.Is(err, http.ErrAbortHandler) {
				t.Errorf("recovered %v, want http.ErrAbortHandler", r)
			}
		}()
		x.ServeHTTP(w, exportRequest())
		t.Error("ServeHTTP returned; want a panic with http.ErrAbortHandler")
	}()
	if !aborted {
		t.Error("abort was not called")
	}
	if res := x.result(); !strings.Contains(res.Error, "client gone") {
		t.Errorf("result error = %q, want the client failure", res.Error)
	}
}
