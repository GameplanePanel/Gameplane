package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/netguard"
)

// EventTest is the synthetic type sent by the test endpoint. It is not in
// AllEvents: sinks can't filter on it and the watcher never emits it.
const EventTest EventType = "test"

// ErrUnknownSink reports a test-send against a sink name that isn't in the
// persisted notifications config.
var ErrUnknownSink = errors.New("unknown notification sink")

// ErrSinkNotConfigured reports a sink that has no configRef Secret yet, so
// there is nothing to dial.
var ErrSinkNotConfigured = errors.New("sink has no configRef secret")

// deliveries counts notification deliveries by sink kind and outcome. A
// growing "failed" or "dropped" delta means admins are missing alerts they
// configured, so it's surfaced at /metrics like the audit webhook counter.
var deliveries = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "gameplane_notify_deliveries_total",
	Help: "Notification deliveries by sink kind and result (sent, failed, dropped, skipped_no_secret).",
}, []string{"kind", "result"})

// queueSize bounds how many undelivered events the notifier holds. Events
// are cluster-level transitions (a few per hour on a busy install), so a
// healthy set of sinks never approaches this; the bound exists so stalled
// endpoints can't grow memory without limit.
const queueSize = 256

// Event is one notifiable occurrence. It is also the exact JSON payload
// shipped to generic webhook sinks, so field renames are a breaking change
// for webhook consumers.
type Event struct {
	Cluster   string    `json:"cluster,omitempty"`
	Type      EventType `json:"type"`
	TS        string    `json:"ts"`
	Kind      string    `json:"kind,omitempty"` // CRD kind: GameServer | Backup | Restore
	Name      string    `json:"name,omitempty"`
	Namespace string    `json:"namespace,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	Message   string    `json:"message,omitempty"`
	Instance  string    `json:"instance,omitempty"`
	Test      bool      `json:"test,omitempty"`
}

// Notifier watches CRD status transitions and delivers matching events to
// the admin-configured sinks. Delivery is best-effort and fully decoupled
// from observation: events land in a bounded buffer and a single background
// worker ships them, so a slow endpoint never blocks the informer callbacks
// (the same "mirror, don't gate" contract as the audit webhook sink).
type Notifier struct {
	store     *db.Store
	k         *kube.Client
	controlNS string
	client    *http.Client
	ch        chan Event
}

// New returns a Notifier reading sink config from store and sink Secrets
// from controlNS. Call Run to start watching and delivering.
func New(store *db.Store, k *kube.Client, controlNS string) *Notifier {
	return &Notifier{
		store:     store,
		k:         k,
		controlNS: controlNS,
		// IsAllowed, not IsPublic: sinks are admin-tier config, the same
		// trust class as ModuleSources, and homelab installs legitimately
		// point them at LAN/in-cluster receivers. Link-local (cloud
		// metadata), unspecified, multicast, and NAT64/6to4 stay blocked
		// at dial time.
		client: netguard.HTTPClient(10*time.Second, netguard.IsAllowed),
		ch:     make(chan Event, queueSize),
	}
}

// Enqueue hands an event to the delivery worker without ever blocking.
// When the buffer is full the event is dropped and counted — a dropped
// alert must at least be visible in metrics.
func (n *Notifier) Enqueue(e Event) {
	select {
	case n.ch <- e:
	default:
		deliveries.WithLabelValues("queue", "dropped").Inc()
		slog.Warn("notification dropped: queue full", "type", e.Type, "namespace", e.Namespace, "name", e.Name)
	}
}

// Run watches CRD status transitions and processes the delivery queue until
// ctx is cancelled. It blocks; run it in a goroutine. Events still buffered
// at shutdown are dropped — notifications mirror state that the dashboard
// and kubectl also show, so exit is never held up for a retrying endpoint.
func (n *Notifier) Run(ctx context.Context) {
	n.runWatchers(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case e := <-n.ch:
			n.fanOut(ctx, e)
		}
	}
}

// fanOut delivers e to every enabled sink whose event filter matches. Sinks
// are re-read from config per event so admin edits apply immediately.
func (n *Notifier) fanOut(ctx context.Context, e Event) {
	loadCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	sinks, err := n.loadSinks(loadCtx)
	if err == nil {
		e.Instance = n.instanceName(loadCtx)
	}
	cancel()
	if err != nil {
		slog.Warn("notification fan-out: loading sinks failed", "err", err)
		return
	}
	for _, s := range sinks {
		if !sinkMatches(s, e.Type) {
			continue
		}
		if s.ConfigRef == "" {
			deliveries.WithLabelValues(s.Kind, "skipped_no_secret").Inc()
			slog.Warn("notification sink has no secret configured; skipping", "sink", s.Name, "type", e.Type)
			continue
		}
		secCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		secret, err := n.sinkSecret(secCtx, s.ConfigRef)
		cancel()
		if err != nil {
			deliveries.WithLabelValues(s.Kind, "failed").Inc()
			slog.Warn("notification sink secret unavailable", "sink", s.Name, "err", err)
			continue
		}
		n.deliverWithRetry(ctx, s, secret, e)
	}
}

// DeliverTest sends a synthetic event to the named persisted sink,
// synchronously and without retry, so the caller can report the real
// outcome. It returns ErrUnknownSink or ErrSinkNotConfigured for the two
// misconfiguration cases the handler distinguishes.
func (n *Notifier) DeliverTest(ctx context.Context, sinkName string) error {
	sinks, err := n.loadSinks(ctx)
	if err != nil {
		return fmt.Errorf("load sinks: %w", err)
	}
	for _, s := range sinks {
		if s.Name != sinkName {
			continue
		}
		if s.ConfigRef == "" {
			return fmt.Errorf("sink %q: %w", sinkName, ErrSinkNotConfigured)
		}
		secret, err := n.sinkSecret(ctx, s.ConfigRef)
		if err != nil {
			return err
		}
		e := Event{
			Type:     EventTest,
			TS:       time.Now().UTC().Format(time.RFC3339),
			Instance: n.instanceName(ctx),
			Test:     true,
		}
		if err := n.deliver(ctx, s, secret, e); err != nil {
			deliveries.WithLabelValues(s.Kind, "failed").Inc()
			return fmt.Errorf("deliver test to %q: %w", sinkName, err)
		}
		deliveries.WithLabelValues(s.Kind, "sent").Inc()
		return nil
	}
	return fmt.Errorf("%w: %q", ErrUnknownSink, sinkName)
}

// instanceName reads general.instanceName so notifications from different
// Gameplane installs are tellable apart. Best-effort: an unreadable config
// just yields the "Gameplane" fallback at format time.
func (n *Notifier) instanceName(ctx context.Context) string {
	raw, ok, err := n.store.ConfigValue(ctx, "general")
	if err != nil || !ok {
		return ""
	}
	var c struct {
		InstanceName string `json:"instanceName"`
	}
	if json.Unmarshal([]byte(raw), &c) != nil {
		return ""
	}
	return c.InstanceName
}
