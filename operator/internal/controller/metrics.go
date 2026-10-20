package controller

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// allGameServerPhases is the full set of phases a GameServer can report. Each
// fleet collector emits a sample for every known phase on each scrape (0 when
// none are in that phase) so dashboards and alerts see a continuous series
// instead of a label that blinks in and out of existence as objects change
// state.
var allGameServerPhases = []gameplanev1alpha1.GameServerPhase{
	gameplanev1alpha1.GameServerPhasePending,
	gameplanev1alpha1.GameServerPhaseStarting,
	gameplanev1alpha1.GameServerPhaseRunning,
	gameplanev1alpha1.GameServerPhaseStopping,
	gameplanev1alpha1.GameServerPhaseStopped,
	gameplanev1alpha1.GameServerPhaseSuspended,
	gameplanev1alpha1.GameServerPhaseFailed,
}

// allBackupPhases is the full set of phases a Backup can report.
var allBackupPhases = []gameplanev1alpha1.BackupPhase{
	gameplanev1alpha1.BackupPhasePending,
	gameplanev1alpha1.BackupPhaseRunning,
	gameplanev1alpha1.BackupPhaseSucceeded,
	gameplanev1alpha1.BackupPhaseFailed,
}

var gameServersDesc = prometheus.NewDesc(
	"gameplane_gameservers",
	"Number of GameServers currently in each lifecycle phase, as observed by the operator.",
	[]string{"phase"}, nil,
)

var backupsDesc = prometheus.NewDesc(
	"gameplane_backups",
	"Number of Backups currently in each phase, as observed by the operator.",
	[]string{"phase"}, nil,
)

// idleStates are the two states an idle-enabled GameServer can be in. Servers
// with idle disabled are counted in neither, so asleep/(asleep+awake) is the
// share of opted-in servers currently parked.
var idleStates = []string{"asleep", "awake"}

var gameServersIdleDesc = prometheus.NewDesc(
	"gameplane_gameservers_idle",
	"Number of idle-enabled GameServers currently asleep vs awake, as observed by the operator. "+
		"Servers with spec.idle.enabled false are not counted.",
	[]string{"state"}, nil,
)

// phaseStrings converts a slice of string-kind phase constants to their bare
// string label values.
func phaseStrings[T ~string](in []T) []string {
	out := make([]string, len(in))
	for i, p := range in {
		out[i] = string(p)
	}
	return out
}

// phaseCollector reports how many objects of one kind sit in each phase by
// listing them from the operator's shared informer cache at scrape time.
// Reading the cache is cheap and, unlike mutating a GaugeVec inside reconcile,
// sidesteps the stale-label and reset races you get when several reconciles run
// concurrently — the emitted series always reflects exactly what the cache
// holds right now.
type phaseCollector struct {
	desc        *prometheus.Desc
	logName     string
	knownPhases []string
	// emptyPhase is the phase an object with no phase set yet (just created,
	// not reconciled) is attributed to, so the total still matches the fleet.
	emptyPhase string
	// phases lists the live objects and returns each one's phase label.
	phases func(ctx context.Context) ([]string, error)
}

func (c *phaseCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.desc
}

func (c *phaseCollector) Collect(ch chan<- prometheus.Metric) {
	got, err := c.phases(context.Background())
	if err != nil {
		// A scrape that lands mid-cache-sync (or a transient cache error) must
		// not fail the whole /metrics response; skip this collector's samples
		// and let the next scrape pick them up.
		ctrllog.Log.WithName("metrics").V(1).Info(c.logName+": list failed", "error", err)
		return
	}

	counts := make(map[string]int, len(c.knownPhases))
	for _, phase := range c.knownPhases {
		counts[phase] = 0
	}
	for _, phase := range got {
		if phase == "" {
			phase = c.emptyPhase
		}
		// Defensive: an unexpected phase value (none should exist) is ignored
		// rather than emitted as an unknown series.
		if _, known := counts[phase]; known {
			counts[phase]++
		}
	}

	for _, phase := range c.knownPhases {
		ch <- prometheus.MustNewConstMetric(
			c.desc, prometheus.GaugeValue, float64(counts[phase]), phase,
		)
	}
}

// NewGameServerCollector builds the GameServer fleet-metrics collector. Register
// it with the controller-runtime metrics registry so it is served on the
// operator's existing /metrics endpoint:
//
//	metrics.Registry.MustRegister(controller.NewGameServerCollector(mgr.GetClient()))
func NewGameServerCollector(reader client.Reader) prometheus.Collector {
	return &phaseCollector{
		desc:        gameServersDesc,
		logName:     "gameserver fleet metric",
		knownPhases: phaseStrings(allGameServerPhases),
		emptyPhase:  string(gameplanev1alpha1.GameServerPhasePending),
		phases: func(ctx context.Context) ([]string, error) {
			var list gameplanev1alpha1.GameServerList
			if err := reader.List(ctx, &list); err != nil {
				return nil, err
			}
			out := make([]string, len(list.Items))
			for i := range list.Items {
				out[i] = string(list.Items[i].Status.Phase)
			}
			return out, nil
		},
	}
}

// NewGameServerIdleCollector builds the idle-sleep fleet collector: how many
// opted-in GameServers are currently parked. It reuses phaseCollector because
// the shape is identical — count live objects into a fixed label set, emitting
// every label on each scrape so the series never blinks out of existence.
//
// This is the metric that answers "is idle sleep actually saving me anything",
// which is the whole reason the feature exists.
func NewGameServerIdleCollector(reader client.Reader) prometheus.Collector {
	return &phaseCollector{
		desc:        gameServersIdleDesc,
		logName:     "gameserver idle metric",
		knownPhases: idleStates,
		// Unreachable: the mapper below never returns "". phaseCollector
		// attributes an empty label here, and "awake" is the honest default.
		emptyPhase: "awake",
		phases: func(ctx context.Context) ([]string, error) {
			var list gameplanev1alpha1.GameServerList
			if err := reader.List(ctx, &list); err != nil {
				return nil, err
			}
			out := make([]string, 0, len(list.Items))
			for i := range list.Items {
				gs := &list.Items[i]
				if gs.Spec.Idle == nil || !gs.Spec.Idle.Enabled {
					continue // not opted in — belongs to neither series
				}
				if gs.Status.Idle != nil && gs.Status.Idle.Asleep {
					out = append(out, "asleep")
				} else {
					out = append(out, "awake")
				}
			}
			return out, nil
		},
	}
}

// NewBackupCollector builds the Backup fleet-metrics collector. Register it the
// same way as NewGameServerCollector. A non-zero Failed series is the signal an
// operator cares about: a backup that silently failed is a data-loss risk.
func NewBackupCollector(reader client.Reader) prometheus.Collector {
	return &phaseCollector{
		desc:        backupsDesc,
		logName:     "backup fleet metric",
		knownPhases: phaseStrings(allBackupPhases),
		emptyPhase:  string(gameplanev1alpha1.BackupPhasePending),
		phases: func(ctx context.Context) ([]string, error) {
			var list gameplanev1alpha1.BackupList
			if err := reader.List(ctx, &list); err != nil {
				return nil, err
			}
			out := make([]string, len(list.Items))
			for i := range list.Items {
				out[i] = string(list.Items[i].Status.Phase)
			}
			return out, nil
		},
	}
}
