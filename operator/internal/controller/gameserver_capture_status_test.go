package controller

import (
	"context"
	"fmt"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	gameplanev1alpha1 "github.com/ValgulNecron/gameplane/operator/api/v1alpha1"
)

func TestCaptureStatusPatchPreservesConcurrentIdleClock(t *testing.T) {
	for _, captureWasReady := range []bool{false, true} {
		t.Run(fmt.Sprintf("captureWasReady=%t", captureWasReady), func(t *testing.T) {
			gs := wipeGameServer(false, "")
			gs.Spec.Idle = enabledIdle(5)
			now := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
			oldHeartbeat := metav1.NewTime(now.Add(-time.Second))
			recent := metav1.NewTime(now.Add(-time.Minute))
			backdated := metav1.NewTime(now.Add(-10 * time.Minute))
			players := int32(0)
			gs.Status.Phase = gameplanev1alpha1.GameServerPhaseRunning
			gs.Status.Agent = &gameplanev1alpha1.AgentStatus{LastHeartbeat: &oldHeartbeat, PlayersOnline: &players}
			gs.Status.Idle = &gameplanev1alpha1.IdleStatus{EmptySince: &recent}
			gs.Status.Capture = &gameplanev1alpha1.CaptureStatus{Ready: captureWasReady}
			ss := wipeStoppedSet(gs)
			one := int32(1)
			ss.Spec.Replicas = &one
			ss.Status.Replicas, ss.Status.ReadyReplicas = 1, 1
			injected := false
			c := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(gs, ss).WithStatusSubresource(gs, ss).
				WithInterceptorFuncs(interceptor.Funcs{
					SubResourcePatch: func(ctx context.Context, c client.Client, subresource string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
						if subresource == "status" && !injected {
							injected = true
							var current gameplanev1alpha1.GameServer
							if err := c.Get(ctx, client.ObjectKeyFromObject(gs), &current); err != nil {
								return err
							}
							// Model a clock backdate and fresh agent heartbeat arriving
							// after idle derivation but during the capture status write.
							current.Status.Idle.EmptySince = &backdated
							current.Status.Agent.LastHeartbeat = &now
							if err := c.Status().Update(ctx, &current); err != nil {
								return err
							}
						}
						return c.SubResource(subresource).Patch(ctx, obj, patch, opts...)
					},
				}).Build()
			r := &GameServerReconciler{Client: c, Scheme: c.Scheme()}
			idle, idleStatus, _, scheduleErr, err := r.reconcileIdle(t.Context(), gs)
			if err != nil || idle != idleAwake || idleStatus == nil || idleStatus.EmptySince == nil || !idleStatus.EmptySince.Equal(&recent) {
				t.Fatalf("initial idle derivation: state=%v err=%v", idle, err)
			}
			if err := r.reconcileCapture(t.Context(), gs); err != nil {
				t.Fatal(err)
			}
			if _, err := r.reconcileStatus(t.Context(), gs, idle, idleStatus, scheduleErr, tunnelPlan{}, &gameplanev1alpha1.GameTemplate{}, nil, ""); err != nil {
				t.Fatal(err)
			}
			current := wipeCurrent(t, c, gs)
			if !injected || current.Status.Idle == nil || current.Status.Idle.EmptySince == nil || !current.Status.Idle.EmptySince.Equal(&backdated) {
				t.Fatal("final status merge overwrote the concurrently backdated idle clock")
			}
			if current.Status.Agent == nil || current.Status.Agent.LastHeartbeat == nil || !current.Status.Agent.LastHeartbeat.Equal(&now) {
				t.Fatal("status merge overwrote the fresh agent heartbeat")
			}
			if current.Status.Capture == nil || current.Status.Capture.Ready {
				t.Fatal("disabled capture status was not persisted")
			}
			// The preserved backdate must drive the next sleep decision,
			// rather than silently resetting the server's idle interval.
			nextIdle, _, _, _, err := r.reconcileIdle(t.Context(), current)
			if err != nil || nextIdle != idleAsleep || current.Annotations[IdleAsleepSinceAnnotation] == "" {
				t.Fatalf("backdated clock did not produce sleep: state=%v err=%v", nextIdle, err)
			}
		})
	}
}
