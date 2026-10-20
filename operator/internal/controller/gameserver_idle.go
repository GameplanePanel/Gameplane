package controller

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/robfig/cron/v3"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

const (
	// IdleAsleepSinceAnnotation is the authoritative marker that the operator
	// has put this server to sleep for idleness, carrying the RFC3339 time it
	// happened. Its presence — not a status field — is what drives the
	// scale-down, for the same reason the restart token is an annotation:
	// status is a derived read model that a `kubectl replace` or a subresource
	// reset can drop, and losing the marker would silently wake every sleeping
	// server in the cluster.
	//
	// It is deliberately NOT spec.suspend. That field belongs to the user (the
	// API's :start/:stop verbs patch it), so overloading it would make an
	// automatic sleep indistinguishable from a deliberate stop.
	IdleAsleepSinceAnnotation = "gameplane.local/idle-asleep-since"

	// IdleWakeRequestedAnnotation carries the token of a wake request stamped
	// by the API's :wake verb; IdleWakeCompletedAnnotation echoes it back once
	// the operator has actually woken the server. Same request/ack shape as
	// the restart pair, and for the same reason: the request survives on the
	// object until honored, so it cannot be lost to a coalesced reconcile.
	IdleWakeRequestedAnnotation = "gameplane.local/idle-wake-requested"
	IdleWakeCompletedAnnotation = "gameplane.local/idle-wake-completed"

	// defaultIdleAfter matches the CRD default for spec.idle.afterMinutes and
	// applies when the field is unset.
	defaultIdleAfter = 30 * time.Minute

	// idleReevaluate bounds how long the controller waits before re-checking a
	// sleeping server against its wake windows when no window is parseable (or
	// none are configured but a wake could still arrive by annotation). The
	// GameServer watch already wakes us on an annotation change; this is only a
	// backstop.
	idleReevaluate = 5 * time.Minute
)

// idleState is what the idle policy wants for this reconcile.
type idleState int

const (
	// idleAwake means the server should be running as far as idle policy is
	// concerned — disabled, ineligible, still accumulating, or just woken.
	idleAwake idleState = iota
	// idleAsleep means the operator has parked this server; desiredReplicas
	// must drive it down through the same graceful path as spec.suspend.
	idleAsleep
)

// idleInputs is everything idleDecide needs, gathered from the object so the
// decision itself stays a pure function — no client, no clock, no I/O — and
// can be table-tested without envtest.
type idleInputs struct {
	spec *gameplanev1alpha1.IdleSpec
	// suspended is the user's own spec.suspend.
	suspended bool
	// phase is the phase observed on the *previous* reconcile. Idle time only
	// accrues from Running, so a server that is starting, stopping, or failed
	// never sleeps out from under the operator mid-transition.
	phase gameplanev1alpha1.GameServerPhase
	// hbFresh reports whether the agent heartbeat is inside its freshness
	// window. A stale heartbeat freezes the idle clock rather than reading as
	// empty: a dead agent must never look like an empty server.
	hbFresh bool
	// playersOnline is nil when unknown (the game exposes no player source, or
	// the query failed). Unknown is NOT zero — see AgentStatus.PlayersOnline.
	playersOnline *int32
	// busy reports another operator-owned lifecycle op in flight (restart or
	// wipe). Sleeping into one of those would fight it for the replica count.
	busy bool

	asleepSince  *time.Time
	emptySince   *time.Time
	lastWakeTime *time.Time
	// wakePending is a wake request that has not been acked yet.
	wakePending bool

	now time.Time
}

// idleOutcome is the decision plus everything the caller needs to persist it.
type idleOutcome struct {
	state idleState
	// sleep/wake are the transitions to write as annotations this pass.
	sleep bool
	wake  bool
	// status is the read model to fold into the single status patch that
	// reconcileStatus issues; nil when spec.idle is not configured.
	status *gameplanev1alpha1.IdleStatus
	// requeue is when to look again (0 = no idle-driven requeue).
	requeue time.Duration
	// err reports a malformed wake window, surfaced as a condition rather
	// than failing the whole reconcile — an unparseable schedule must not
	// wedge a server, it must just never fire.
	err error
}

// idleDecide is the whole idle policy, as a pure function.
//
// Order matters. An explicit user action always beats the automatic policy:
// disabling idle, suspending by hand, or asking for a wake each take the
// server out of the operator's hands before any emptiness accounting happens.
func idleDecide(in idleInputs) idleOutcome {
	// Idle disabled (or never configured): if we had parked this server, let
	// it back up. Turning the feature off must not strand a sleeping server.
	// A nil status clears status.idle, so a disabled server carries no stale
	// "asleep" read model.
	if in.spec == nil || !in.spec.Enabled {
		return idleOutcome{state: idleAwake, wake: in.asleepSince != nil, status: nil}
	}

	st := &gameplanev1alpha1.IdleStatus{}
	if in.lastWakeTime != nil {
		st.LastWakeTime = &metav1.Time{Time: *in.lastWakeTime}
	}

	// Validate the wake-window schedule up front, whenever idle is enabled,
	// regardless of sleep state (F-057). A malformed schedule must be visible
	// on the IdleScheduleInvalid condition on an awake server too, not only
	// while asleep, and must not disappear the moment the server wakes.
	scheduleErr := validateWakeWindows(in.spec.WakeWindows)

	// The user's own stop wins outright, and clears any sleep marker so that
	// resuming by hand doesn't come back to a server still flagged asleep.
	if in.suspended {
		st.Reason = "stopped by user"
		return idleOutcome{state: idleAwake, wake: in.asleepSince != nil, status: st, err: scheduleErr}
	}

	// An explicit wake request outranks the wake windows and the idle clock:
	// honor it, ack it, and hand back a full fresh idle period so the server
	// someone just woke doesn't immediately sleep again.
	if in.wakePending {
		st.LastWakeTime = &metav1.Time{Time: in.now}
		st.Reason = "woken by request"
		return idleOutcome{state: idleAwake, wake: true, status: st, err: scheduleErr}
	}

	if in.asleepSince != nil {
		st.Asleep = true
		st.AsleepSince = &metav1.Time{Time: *in.asleepSince}

		if scheduleErr != nil {
			st.Reason = fmt.Sprintf("asleep; wake window invalid: %v", scheduleErr)
			return idleOutcome{state: idleAsleep, status: st, requeue: idleReevaluate, err: scheduleErr}
		}
		due, next, err := wakeWindowDue(in.spec.WakeWindows, *in.asleepSince, in.now)
		if err != nil {
			// Already validated above; defensive only.
			st.Reason = fmt.Sprintf("asleep; wake window invalid: %v", err)
			return idleOutcome{state: idleAsleep, status: st, requeue: idleReevaluate, err: err}
		}
		if due {
			st.Asleep, st.AsleepSince = false, nil
			st.LastWakeTime = &metav1.Time{Time: in.now}
			st.Reason = "woken by wake window"
			return idleOutcome{state: idleAwake, wake: true, status: st}
		}
		st.Reason = "asleep (no players)"
		return idleOutcome{state: idleAsleep, status: st, requeue: untilNext(next, in.now)}
	}

	// Awake: decide whether idle time is accruing.
	if reason, ok := idleEligible(in); !ok {
		st.Reason = reason
		return idleOutcome{state: idleAwake, status: st, err: scheduleErr} // clock cleared (EmptySince stays nil)
	}

	since := in.now
	if in.emptySince != nil {
		since = *in.emptySince
	}
	st.EmptySince = &metav1.Time{Time: since}

	after := defaultIdleAfter
	if in.spec.AfterMinutes != nil {
		after = time.Duration(*in.spec.AfterMinutes) * time.Minute
	}
	if remaining := after - in.now.Sub(since); remaining > 0 {
		st.Reason = fmt.Sprintf("empty, sleeping in %s", remaining.Round(time.Second))
		return idleOutcome{state: idleAwake, status: st, requeue: remaining, err: scheduleErr}
	}

	st.Asleep = true
	st.AsleepSince = &metav1.Time{Time: in.now}
	st.EmptySince = nil
	st.Reason = "asleep (no players)"
	return idleOutcome{state: idleAsleep, sleep: true, status: st, err: scheduleErr}
}

// idleEligible reports whether the server is currently accruing idle time,
// and if not, why — the "why" is surfaced on status so a server that never
// sleeps explains itself instead of looking broken.
func idleEligible(in idleInputs) (string, bool) {
	switch {
	case in.busy:
		return "another lifecycle operation is in flight", false
	case in.phase != gameplanev1alpha1.GameServerPhaseRunning:
		return fmt.Sprintf("server is %s, not Running", nonEmptyPhase(in.phase)), false
	case !in.hbFresh:
		// Explicitly not "empty": without a live agent we have no idea who is
		// connected, and guessing zero would sleep a populated server.
		return "agent heartbeat is stale; player count unknown", false
	case in.playersOnline == nil:
		return "this game reports no player count", false
	case *in.playersOnline > 0:
		return fmt.Sprintf("%d player(s) online", *in.playersOnline), false
	}
	return "", true
}

func nonEmptyPhase(p gameplanev1alpha1.GameServerPhase) gameplanev1alpha1.GameServerPhase {
	if p == "" {
		return "Pending"
	}
	return p
}

// validateWakeWindows reports whether every entry in windows parses as a
// standard cron schedule, without evaluating any of them against a clock.
// Called every idleDecide pass so a malformed schedule surfaces regardless of
// whether the server happens to be asleep right now (F-057).
func validateWakeWindows(windows []string) error {
	for _, w := range windows {
		if _, err := cron.ParseStandard(w); err != nil {
			return fmt.Errorf("parse wake window %q: %w", w, err)
		}
	}
	return nil
}

// wakeWindowDue reports whether any wake window would have fired between
// asleepSince and now, and the soonest upcoming fire time otherwise.
//
// Anchoring on asleepSince rather than a separate "last checked" stamp is what
// keeps this stateless: the anchor is cleared the instant the server wakes, so
// a window can fire at most once per sleep and there is no bookkeeping to drift.
func wakeWindowDue(windows []string, asleepSince, now time.Time) (bool, time.Time, error) {
	var soonest time.Time
	for _, w := range windows {
		sched, err := cron.ParseStandard(w)
		if err != nil {
			return false, time.Time{}, fmt.Errorf("parse wake window %q: %w", w, err)
		}
		next := sched.Next(asleepSince)
		if !next.After(now) {
			return true, time.Time{}, nil
		}
		if soonest.IsZero() || next.Before(soonest) {
			soonest = next
		}
	}
	return false, soonest, nil
}

// untilNext converts an absolute next-fire time into a requeue delay, falling
// back to the backstop when there is no upcoming window.
func untilNext(next, now time.Time) time.Duration {
	if next.IsZero() {
		return idleReevaluate
	}
	if d := next.Sub(now); d > 0 {
		return d
	}
	return time.Second
}

// reconcileIdle evaluates the idle policy and persists the transitions it
// decides. It runs before desiredReplicas so the replica count computed this
// pass already reflects a sleep or a wake.
//
// Only annotations are written here. The status read model is returned for
// reconcileStatus to fold into its single status patch, so the agent's
// concurrent status.agent heartbeat has exactly one writer to race, not two.
// reconcileIdle runs the idle policy and returns, in order: the resulting
// state, the status read model, the requeue interval, an unparseable
// wake-window error (F-057 — surfaced by the caller as a status condition;
// never wedges the reconcile), and a fatal error from writing the sleep/wake
// annotation.
func (r *GameServerReconciler) reconcileIdle(
	ctx context.Context, gs *gameplanev1alpha1.GameServer,
) (idleState, *gameplanev1alpha1.IdleStatus, time.Duration, error, error) {
	in := idleInputs{
		spec:      gs.Spec.Idle,
		suspended: gs.Spec.Suspend,
		phase:     gs.Status.Phase,
		hbFresh:   heartbeatFresh(gs),
		busy: tokenPending(gs, RestartRequestedAnnotation, RestartCompletedAnnotation) ||
			tokenPending(gs, WipeRequestedAnnotation, WipeCompletedAnnotation),
		wakePending: tokenPending(gs, IdleWakeRequestedAnnotation, IdleWakeCompletedAnnotation),
		asleepSince: parseAnnotationTime(gs, IdleAsleepSinceAnnotation),
		now:         time.Now().UTC(),
	}
	if gs.Status.Agent != nil {
		in.playersOnline = gs.Status.Agent.PlayersOnline
	}
	if gs.Status.Idle != nil {
		if gs.Status.Idle.EmptySince != nil {
			t := gs.Status.Idle.EmptySince.Time
			in.emptySince = &t
		}
		if gs.Status.Idle.LastWakeTime != nil {
			t := gs.Status.Idle.LastWakeTime.Time
			in.lastWakeTime = &t
		}
	}

	out := idleDecide(in)
	if out.sleep || out.wake {
		if err := r.applyIdleTransition(ctx, gs, out, in.now); err != nil {
			return idleAwake, nil, 0, nil, err
		}
	}
	return out.state, out.status, out.requeue, out.err, nil
}

// applyIdleTransition writes the sleep marker or clears it (acking any wake
// request in the same patch, so a token can never be honored without being
// acked and re-run forever).
func (r *GameServerReconciler) applyIdleTransition(
	ctx context.Context, gs *gameplanev1alpha1.GameServer, out idleOutcome, now time.Time,
) error {
	base := gs.DeepCopy()
	if gs.Annotations == nil {
		gs.Annotations = map[string]string{}
	}
	switch {
	case out.sleep:
		gs.Annotations[IdleAsleepSinceAnnotation] = now.Format(time.RFC3339)
	case out.wake:
		delete(gs.Annotations, IdleAsleepSinceAnnotation)
		// Drop the soft-stop grace clock too: a wake that lands mid-drain must
		// not leave a stale stamp that instantly expires the *next* sleep's
		// grace period and hard-kills the game.
		delete(gs.Annotations, stopRequestedAtAnnotation)
		if tok := gs.Annotations[IdleWakeRequestedAnnotation]; tok != "" {
			gs.Annotations[IdleWakeCompletedAnnotation] = tok
		}
	}
	if err := r.Patch(ctx, gs, client.MergeFrom(base)); err != nil {
		return fmt.Errorf("patch idle annotations on %s/%s: %w", gs.Namespace, gs.Name, err)
	}
	return nil
}

// tokenPending reports whether a request token is set and not yet acked.
func tokenPending(gs *gameplanev1alpha1.GameServer, requested, completed string) bool {
	req := gs.Annotations[requested]
	return req != "" && req != gs.Annotations[completed]
}

// parseAnnotationTime reads an RFC3339 annotation, returning nil when absent
// or unparseable. An unparseable sleep marker reads as "not asleep", which
// fails safe: the server comes up rather than staying down on bad data.
func parseAnnotationTime(gs *gameplanev1alpha1.GameServer, key string) *time.Time {
	raw, ok := gs.Annotations[key]
	if !ok {
		return nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil
	}
	return &t
}
