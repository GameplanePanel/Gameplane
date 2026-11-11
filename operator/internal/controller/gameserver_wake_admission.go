package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

const wakeStartsName = "gameplane-wake-starts"

// reserveWakeStart atomically reserves this sleep episode before its wake token
// is acknowledged. Both writes survive operator restart. Losing a ConfigMap or
// GameServer resourceVersion race retries the entire decision.
func (r *GameServerReconciler) reserveWakeStart(ctx context.Context, gs *gameplanev1alpha1.GameServer) (bool, error) {
	key := client.ObjectKey{Namespace: r.WakeStartNamespace, Name: wakeStartsName}
	var starts corev1.ConfigMap
	err := r.APIReader.Get(ctx, key, &starts)
	if apierrors.IsNotFound(err) {
		var operator appsv1.Deployment
		if err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: key.Namespace, Name: "gameplane-operator"}, &operator); err != nil {
			return false, fmt.Errorf("get owner for wake reservations: %w", err)
		}
		owner := metav1.NewControllerRef(&operator, appsv1.SchemeGroupVersion.WithKind("Deployment"))
		// The ledger can be garbage-collected after the Deployment is gone.
		// Blocking deletion would require permission to edit its finalizers.
		owner.BlockOwnerDeletion = nil
		starts.ObjectMeta = metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace, OwnerReferences: []metav1.OwnerReference{*owner}}
		if err := r.Create(ctx, &starts); err != nil {
			if apierrors.IsAlreadyExists(err) {
				return false, nil
			}
			return false, fmt.Errorf("create wake reservations: %w", err)
		}
	} else if err != nil {
		return false, fmt.Errorf("get wake reservations: %w", err)
	}
	if !starts.DeletionTimestamp.IsZero() {
		return false, nil
	}
	ref := metav1.GetControllerOf(&starts)
	if ref == nil || ref.APIVersion != appsv1.SchemeGroupVersion.String() || ref.Kind != "Deployment" || ref.Name != "gameplane-operator" {
		return false, fmt.Errorf("refusing foreign wake reservation ConfigMap %s", key)
	}
	uid := string(gs.UID)
	episode := gs.Namespace + "/" + gs.Name + "@" + gs.Annotations[IdleAsleepSinceAnnotation]
	if starts.Data[uid] == episode {
		return true, nil
	}
	if starts.Data == nil {
		starts.Data = map[string]string{}
	}
	// Ignore objects that have completed, stopped, slept again or been
	// replaced. A Pending/slow start retains its reservation regardless of age.
	if len(starts.Data) >= r.WakeStartLimit {
		for owner, saved := range starts.Data {
			inUse, err := r.wakeStartInUse(ctx, owner, saved)
			if err != nil {
				return false, err
			}
			if !inUse {
				delete(starts.Data, owner)
			}
		}
	}
	if len(starts.Data) >= r.WakeStartLimit {
		return false, nil
	}
	starts.Data[uid] = episode
	if err := r.Update(ctx, &starts); err != nil {
		return false, fmt.Errorf("reserve fleet wake capacity: %w", err)
	}
	return true, nil
}

func (r *GameServerReconciler) wakeStartInUse(ctx context.Context, uid, episode string) (bool, error) {
	target, since, ok := strings.Cut(episode, "@")
	namespace, name, named := strings.Cut(target, "/")
	if !ok || !named || namespace == "" || name == "" || since == "" {
		return false, fmt.Errorf("invalid wake reservation for UID %s in %s/%s", uid, r.WakeStartNamespace, wakeStartsName)
	}
	key := client.ObjectKey{Namespace: namespace, Name: name}
	var owner gameplanev1alpha1.GameServer
	if err := r.APIReader.Get(ctx, key, &owner); err != nil {
		return false, client.IgnoreNotFound(err)
	}
	if string(owner.UID) != uid {
		return false, nil
	}
	if asleep := owner.Annotations[IdleAsleepSinceAnnotation]; asleep != "" {
		// Between the reservation and the wake patch, the episode itself owns
		// the slot. A concurrent takeover of the lifecycle changes the GS
		// resourceVersion, fencing any outstanding wake patch.
		_, held, err := lifecycleOwner(&owner)
		if err != nil || held || asleep != since || owner.Spec.Suspend || !owner.DeletionTimestamp.IsZero() || owner.Spec.Idle == nil || !owner.Spec.Idle.Enabled {
			return false, err
		}
		if tokenPending(&owner, IdleWakeRequestedAnnotation, IdleWakeCompletedAnnotation) {
			return true, nil
		}
		t, err := time.Parse(time.RFC3339, since)
		if err != nil {
			return false, err
		}
		// A withdrawn request cannot strand a slot. A due prewarm window
		// still owns it; an invalidated schedule cannot start the game.
		due, _, windowErr := wakeWindowDue(owner.Spec.Idle.WakeWindows, t, time.Now())
		return due && windowErr == nil, nil
	}
	var ss appsv1.StatefulSet
	if err := r.APIReader.Get(ctx, key, &ss); err != nil {
		if apierrors.IsNotFound(err) {
			return !owner.Spec.Suspend && owner.DeletionTimestamp.IsZero() && owner.Status.Phase != gameplanev1alpha1.GameServerPhaseFailed, nil
		}
		return false, err
	}
	if !metav1.IsControlledBy(&ss, &owner) {
		return false, fmt.Errorf("StatefulSet %s does not belong to wake reservation %s", key, uid)
	}
	if ss.Status.ObservedGeneration < ss.Generation {
		return true, nil
	}
	if owner.Spec.Suspend || !owner.DeletionTimestamp.IsZero() {
		return ss.Status.Replicas > 0, nil
	}
	return ss.Spec.Replicas == nil || *ss.Spec.Replicas == 0 || ss.Status.ReadyReplicas == 0, nil
}
