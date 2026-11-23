package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// s3Events counts audit-event S3 deliveries by outcome. Like the webhook
// counter, a "dropped" or "failed" delta is operationally important — it means
// the external audit mirror has a gap — so it's surfaced at /metrics.
var s3Events = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "gameplane_audit_s3_events_total",
	Help: "Audit-event S3 deliveries by result (sent, failed, dropped).",
}, []string{"result"})

const (
	s3Buffer         = 1024
	s3FlushCountSize = 100
	s3FlushByteSize  = 1024 * 1024 // 1 MiB
	s3FlushInterval  = 5 * time.Second
)

// S3Sink mirrors each audit event to an S3-compatible endpoint by batching
// events as NDJSON lines into objects. Delivery is best-effort and fully
// decoupled from the request path: events are handed to a bounded buffer and
// shipped by a single background worker, so a slow or unreachable endpoint
// never blocks — or fails — an audited request. The database stays the source
// of truth; this is the same "mirror, don't gate" contract as the webhook sink,
// just batched into S3 objects rather than individual POSTs.
//
// Buffered events not yet flushed are lost on crash (same model as the webhook
// sink).
type S3Sink struct {
	endpoint   string
	bucket     string
	prefix     string
	client     *minio.Client
	ch         chan Event
	seqCounter *atomic.Uint64
}

// S3Config holds the configuration needed to create an S3Sink.
type S3Config struct {
	Endpoint  string
	Bucket    string
	Prefix    string
	Region    string
	Insecure  bool
	AccessKey string
	SecretKey string
}

// NewS3Sink returns a sink that batches audit events as NDJSON to an S3-compatible
// endpoint. The endpoint is not validated at startup — validation happens on first
// flush. Call Start to launch the delivery worker.
func NewS3Sink(cfg S3Config) (*S3Sink, error) {
	region := cfg.Region
	if region == "" {
		region = "us-east-1"
	}
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: !cfg.Insecure,
		Region: region,
	})
	if err != nil {
		return nil, fmt.Errorf("s3 client init: %w", err)
	}
	return &S3Sink{
		endpoint:   cfg.Endpoint,
		bucket:     cfg.Bucket,
		prefix:     cfg.Prefix,
		client:     client,
		ch:         make(chan Event, s3Buffer),
		seqCounter: &atomic.Uint64{},
	}, nil
}

// Enqueue hands an event to the worker without ever blocking. When the buffer
// is full (a stalled endpoint backing up), the event is dropped and counted —
// a dropped mirror leaves a hole in the external trail, so it must be visible.
func (s *S3Sink) Enqueue(e Event) {
	select {
	case s.ch <- e:
	default:
		s3Events.WithLabelValues("dropped").Inc()
	}
}

// Start runs the delivery worker until ctx is cancelled, then best-effort
// drains whatever is already buffered within a short deadline. It blocks; run
// it in a goroutine.
func (s *S3Sink) Start(ctx context.Context) {
	ticker := time.NewTicker(s3FlushInterval)
	defer ticker.Stop()

	var buffer []Event
	var bufferBytes int64
	var scratch bytes.Buffer // reused to size each event via its exact NDJSON encoding, not a raw-length estimate

	flush := func() {
		if len(buffer) == 0 {
			return
		}
		s.pushBatch(ctx, buffer)
		buffer = buffer[:0]
		bufferBytes = 0
	}

	for {
		select {
		case <-ctx.Done():
			s.drain(ctx, buffer)
			return
		case e := <-s.ch:
			buffer = append(buffer, e)
			// Size the event exactly as it will be uploaded: encoding/json's
			// default SetEscapeHTML(true) can expand a single byte (e.g. '<')
			// up to 6x, so a raw len()-based estimate (even with a fudge
			// factor) can undercount badly. Encoding each event the same way
			// encodeNDJSON does and summing the results is exact, because
			// NDJSON is just independently-encoded lines concatenated —
			// there's no cross-event interaction to approximate.
			scratch.Reset()
			encodeEvent(&scratch, e)
			bufferBytes += int64(scratch.Len())
			if len(buffer) >= s3FlushCountSize || bufferBytes >= s3FlushByteSize {
				flush()
			}
		case <-ticker.C:
			if len(buffer) > 0 {
				flush()
			}
		}
	}
}

// drain ships already-buffered events on shutdown, bounded by a short deadline.
func (s *S3Sink) drain(ctx context.Context, buffer []Event) {
	deadline := time.After(2 * time.Second)
	for {
		select {
		case e := <-s.ch:
			buffer = append(buffer, e)
			if len(buffer) >= s3FlushCountSize {
				s.pushBatch(ctx, buffer)
				buffer = buffer[:0]
			}
		case <-deadline:
			if len(buffer) > 0 {
				s.pushBatch(ctx, buffer)
			}
			return
		default:
			if len(buffer) > 0 {
				s.pushBatch(ctx, buffer)
			}
			return
		}
	}
}

// pushBatch ships a batch of events. It retries 3 times (immediate, +2s, +8s)
// on transient errors; on final failure or success, counts each event accordingly.
func (s *S3Sink) pushBatch(parentCtx context.Context, events []Event) {
	if len(events) == 0 {
		return
	}

	body := s.encodeNDJSON(events)
	key := s.objectKey()

	// Retry logic: immediate, +2s, +8s.
	delays := []time.Duration{0, 2 * time.Second, 8 * time.Second}
	var lastErr error

	for attempt, delay := range delays {
		if attempt > 0 {
			time.Sleep(delay)
		}

		ctx, cancel := context.WithTimeout(context.WithoutCancel(parentCtx), 10*time.Second)
		_, err := s.client.PutObject(ctx, s.bucket, key,
			bytes.NewReader(body), int64(len(body)),
			minio.PutObjectOptions{
				ContentType: "application/x-ndjson",
				// Our batches are single-part PUTs bounded well under the
				// multipart threshold, so there's no streaming benefit here —
				// but minio-go's default behavior for a known-size PUT against
				// a non-TLS endpoint is still to sign the payload with
				// STREAMING-AWS4-HMAC-SHA256-PAYLOAD and wrap the body in
				// aws-chunked framing on the wire (see api.go's newRequest:
				// the streaming branch is keyed on "!c.secure", independent of
				// any MD5 option). Several self-hosted S3-compatible stores —
				// and this package's own tests, which talk plain HTTP to an
				// httptest server — don't expect that framing and choke on
				// it. DisableContentSha256 forces the plain, non-chunked V4
				// signer (X-Amz-Content-Sha256: UNSIGNED-PAYLOAD) so the
				// wire body is exactly the NDJSON bytes above.
				DisableContentSha256: true,
			})
		cancel()

		if err == nil {
			s3Events.WithLabelValues("sent").Add(float64(len(events)))
			return
		}

		lastErr = err
		// Check if it's a permanent error (4xx). Stop retrying immediately.
		if respErr := minio.ToErrorResponse(err); respErr.StatusCode >= 400 && respErr.StatusCode < 500 {
			s3Events.WithLabelValues("failed").Add(float64(len(events)))
			slog.Warn("audit s3 batch failed (permanent)", "err", lastErr, "endpoint", s.endpoint,
				"bucket", s.bucket, "key", key, "events", len(events), "status", respErr.StatusCode)
			return
		}
		// Retry on transient errors (network, timeout, 5xx) except on the last attempt.
		if attempt == len(delays)-1 {
			break
		}
	}

	// Final failure: count all events as failed.
	s3Events.WithLabelValues("failed").Add(float64(len(events)))
	slog.Warn("audit s3 batch failed", "err", lastErr, "endpoint", s.endpoint,
		"bucket", s.bucket, "key", key, "events", len(events))
}

// encodeEvent JSON-encodes a single event as one NDJSON line (the database id
// is excluded, same payload as the webhook sink) and appends it to buf,
// including the trailing newline json.Encoder.Encode always writes. It is the
// sole place that defines the wire encoding, so both the upload body
// (encodeNDJSON) and the buffered-byte size tracked in Start stay in sync —
// summing encodeEvent's output per event is exact, not an estimate, since
// NDJSON lines are encoded independently with no cross-event interaction.
func encodeEvent(buf *bytes.Buffer, e Event) {
	p := map[string]any{
		"ts":     e.TS,
		"actor":  e.Actor,
		"method": e.Method,
		"path":   e.Path,
		"status": e.Status,
	}
	if e.Target != "" {
		p["target"] = e.Target
	}
	if e.IP != "" {
		p["ip"] = e.IP
	}
	if e.Reason != "" {
		p["reason"] = e.Reason
	}
	if e.Source != "" {
		p["source"] = e.Source
	}
	_ = json.NewEncoder(buf).Encode(p)
}

// encodeNDJSON encodes events as NDJSON (one JSON object per line),
// excluding the database id (same payload as the webhook sink).
func (s *S3Sink) encodeNDJSON(events []Event) []byte {
	var buf bytes.Buffer
	for _, e := range events {
		encodeEvent(&buf, e)
	}
	return buf.Bytes()
}

// objectKey returns the S3 object key in format:
// <prefix>/<YYYY>/<MM>/<DD>/<HHMMSS.nano>-<seq>.ndjson
// If prefix is empty, it omits the leading component cleanly.
func (s *S3Sink) objectKey() string {
	now := time.Now().UTC()
	seq := s.seqCounter.Add(1)
	nanos := now.Nanosecond()
	micro := nanos / 1000 // nanoseconds to microseconds for readable timestamp

	key := fmt.Sprintf("%04d/%02d/%02d/%02d%02d%02d.%06d-%d.ndjson",
		now.Year(), now.Month(), now.Day(),
		now.Hour(), now.Minute(), now.Second(), micro, seq)

	if s.prefix == "" {
		return key
	}
	return s.prefix + "/" + key
}
