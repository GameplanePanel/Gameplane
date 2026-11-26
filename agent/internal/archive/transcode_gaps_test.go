package archive

import (
	"archive/tar"
	"bytes"
	"encoding/base64"
	"errors"
	"math/rand"
	"testing"
	"time"
)

// gapCountingWriter records how many Write calls reach it.
type gapCountingWriter struct {
	buf   bytes.Buffer
	calls int
}

func (w *gapCountingWriter) Write(p []byte) (int, error) {
	w.calls++
	return w.buf.Write(p)
}

// gapFailFromWriter rejects the failAt-th Write call and every call after it.
type gapFailFromWriter struct {
	calls  int
	failAt int
}

func (w *gapFailFromWriter) Write(p []byte) (int, error) {
	w.calls++
	if w.calls >= w.failAt {
		return 0, errors.New("disk full")
	}
	return len(p), nil
}

// gapLargeSource is the standard source plus one large incompressible file in a
// kept volume, so the compressed output reaches the destination in many writes.
func gapLargeSource(t *testing.T) []byte {
	t.Helper()
	rng := rand.New(rand.NewSource(7))
	body := make([]byte, 96<<10)
	_, _ = rng.Read(body)
	ents := append(fullSource(), srcEntry{
		hdr:  tar.Header{Name: "volumes/world/big.bin", Typeflag: tar.TypeReg, Mode: 0o644},
		body: string(body),
	})
	return buildSource(t, ents)
}

// assertEveryWriteFails transcodes src with m and checks that a failure of each
// destination write, in turn, is reported as an error.
func assertEveryWriteFails(t *testing.T, format string, src []byte, m Manifest) {
	t.Helper()
	var ok gapCountingWriter
	if _, err := Transcode(&ok, bytes.NewReader(src), format, m); err != nil {
		t.Fatalf("Transcode: %v", err)
	}
	if ok.calls < 2 {
		t.Fatalf("destination received %d writes, want several", ok.calls)
	}
	for failAt := 1; failAt <= ok.calls; failAt++ {
		w := &gapFailFromWriter{failAt: failAt}
		if _, err := Transcode(w, bytes.NewReader(src), format, m); err == nil {
			t.Errorf("%s: write %d of %d failed, but Transcode returned no error", format, failAt, ok.calls)
		}
	}
}

func TestTranscodeFailsOnEveryDestinationWrite(t *testing.T) {
	src := gapLargeSource(t)
	for _, format := range []string{"tar.gz", "zip"} {
		t.Run(format, func(t *testing.T) {
			assertEveryWriteFails(t, format, src, testManifest())
		})
	}
}

func TestTranscodeFailsOnEveryWriteOfLargeManifest(t *testing.T) {
	// Incompressible manifest text large enough that writing the manifest
	// itself reaches the destination.
	rng := rand.New(rand.NewSource(3))
	raw := make([]byte, 12<<10)
	_, _ = rng.Read(raw)
	m := testManifest()
	m.Writer = base64.StdEncoding.EncodeToString(raw)
	src := buildSource(t, fullSource())
	for _, format := range []string{"tar.gz", "zip"} {
		t.Run(format, func(t *testing.T) {
			assertEveryWriteFails(t, format, src, m)
		})
	}
}

func TestTranscodeRejectsManifestThatCannotBeEncoded(t *testing.T) {
	m := testManifest()
	// A year past 9999 cannot be encoded as JSON, so the manifest entry fails.
	m.CapturedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, format := range []string{"tar.gz", "zip"} {
		var out bytes.Buffer
		if _, err := Transcode(&out, bytes.NewReader(nil), format, m); err == nil {
			t.Errorf("%s: Transcode accepted an unencodable manifest", format)
		}
	}
}
