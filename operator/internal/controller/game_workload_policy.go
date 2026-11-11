package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// GameWorkloadPolicy is configured by the cluster administrator. The pool
// label and NoSchedule taint use the same key/value pair.
type GameWorkloadPolicy struct {
	PoolLabel        string            `json:"poolLabel"`
	PoolValue        string            `json:"poolValue"`
	MinCPURequest    resource.Quantity `json:"minCPURequest"`
	MaxCPURequest    resource.Quantity `json:"maxCPURequest"`
	MinMemoryRequest resource.Quantity `json:"minMemoryRequest"`
	MaxMemoryLimit   resource.Quantity `json:"maxMemoryLimit"`
}

// A rejected rollout must retain the effective template for the running
// game's stop sequence. The rejected target is never rendered or executed.
type workloadPolicyRejection struct{ error }

// reconcileRejectedGameWorkload permits a suspended, owned server to drain while its requested configuration is invalid.
func (r *GameServerReconciler) reconcileRejectedGameWorkload(ctx context.Context, gs *gameplanev1alpha1.GameServer,
	effective *gameplanev1alpha1.GameTemplate, rejection error,
) (ctrl.Result, error) {
	var result ctrl.Result
	if gs.Spec.Suspend {
		key := client.ObjectKeyFromObject(gs)
		var live gameplanev1alpha1.GameServer
		if err := r.wipeReader().Get(ctx, key, &live); err != nil {
			return result, client.IgnoreNotFound(err)
		}
		if !live.Spec.Suspend || live.UID != gs.UID || live.Generation != gs.Generation ||
			live.Annotations[effectiveTemplateAnnotation] != gs.Annotations[effectiveTemplateAnnotation] {
			return ctrl.Result{RequeueAfter: reconcileRetryInterval}, nil
		}
		var ss appsv1.StatefulSet
		err := r.wipeReader().Get(ctx, key, &ss)
		if err != nil && !apierrors.IsNotFound(err) {
			return result, err
		}
		if err == nil && metav1.IsControlledBy(&ss, &live) && (ss.Spec.Replicas == nil || *ss.Spec.Replicas != 0) {
			if live.Annotations[effectiveTemplateAnnotation] == "" {
				// A legacy workload has no verified template pin. Use its own
				// Kubernetes termination grace instead of sending commands from
				// catalog content that may never have run on this server.
				effective = &gameplanev1alpha1.GameTemplate{}
			}
			op, held, err := lifecycleOwner(&live)
			if err != nil {
				return result, err
			}
			var replicas int32
			if held && op.Kind == "Rollout" && op.Baseline && len(op.Recovery) == 0 {
				// Baseline normally preserves the existing workload. An explicit
				// stop can still drain it while the new baseline is inadmissible.
				replicas, result.RequeueAfter, err = r.softStop(ctx, &live, effective)
			} else {
				replicas, result.RequeueAfter, err = r.desiredReplicas(ctx, &live, effective, idleAwake)
			}
			if err != nil {
				return requeueOnConflict(result, err)
			}
			if replicas == 0 {
				base := ss.DeepCopy()
				ss.Spec.Replicas = &replicas
				// Pin the original StatefulSet resourceVersion and preserve every
				// field of the existing Pod template, including its revision.
				if err := r.Patch(ctx, &ss, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})); err != nil {
					return requeueOnConflict(result, err)
				}
			}
		}
	}
	return result, r.setPhase(ctx, gs, rejection.Error())
}

// ParseGameWorkloadPolicy rejects incomplete or unrecognized configuration at startup.
func ParseGameWorkloadPolicy(raw string) (*GameWorkloadPolicy, error) {
	if raw == "" {
		return nil, nil
	}
	var policy GameWorkloadPolicy
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&policy); err != nil {
		return nil, fmt.Errorf("decode game workload policy: %w", err)
	}
	if err := decoder.Decode(new(any)); err == nil {
		return nil, fmt.Errorf("expected a single game workload policy object")
	} else if err != io.EOF {
		return nil, fmt.Errorf("decode trailing game workload policy data: %w", err)
	}
	if len(validation.IsQualifiedName(policy.PoolLabel)) != 0 || policy.PoolValue == "" || len(validation.IsValidLabelValue(policy.PoolValue)) != 0 {
		return nil, fmt.Errorf("poolLabel and poolValue must identify a valid, nonempty worker label")
	}
	if policy.MinCPURequest.Sign() <= 0 || policy.MaxCPURequest.Cmp(policy.MinCPURequest) < 0 ||
		policy.MinMemoryRequest.Sign() <= 0 || policy.MaxMemoryLimit.Cmp(policy.MinMemoryRequest) < 0 {
		return nil, fmt.Errorf("CPU and memory bounds require positive minimums and maximums at least as large")
	}
	return &policy, nil
}

// validate checks the merged game resources and placement before the controller can create or change a workload.
func (p *GameWorkloadPolicy) validate(gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate) error {
	res := effectiveResources(gs, tmpl)
	for name, request := range res.Requests {
		if limit, ok := res.Limits[name]; ok && request.Cmp(limit) > 0 {
			return fmt.Errorf("effective resources.requests.%s exceeds resources.limits.%s", name, name)
		}
	}
	if p == nil {
		return nil
	}
	if value, found := gs.Spec.NodeSelector[p.PoolLabel]; found && value != p.PoolValue {
		return fmt.Errorf("spec.nodeSelector conflicts with the required game worker pool %s=%s", p.PoolLabel, p.PoolValue)
	}
	for _, t := range gs.Spec.Tolerations {
		if t.Key != p.PoolLabel || t.Value != p.PoolValue || (t.Operator != corev1.TolerationOpEqual && t.Operator != "") ||
			t.Effect != corev1.TaintEffectNoSchedule || t.TolerationSeconds != nil {
			return fmt.Errorf("spec.tolerations may only tolerate %s=%s:NoSchedule", p.PoolLabel, p.PoolValue)
		}
	}
	cpu := res.Requests.Cpu()
	if cpu.Cmp(p.MinCPURequest) < 0 || cpu.Cmp(p.MaxCPURequest) > 0 {
		return fmt.Errorf("effective resources.requests.cpu must be between %s and %s", p.MinCPURequest.String(), p.MaxCPURequest.String())
	}
	request, limit := res.Requests.Memory(), res.Limits.Memory()
	if request.Cmp(p.MinMemoryRequest) < 0 || limit.IsZero() || limit.Cmp(p.MaxMemoryLimit) > 0 {
		return fmt.Errorf("effective resources.requests.memory must be at least %s, and resources.limits.memory must cover the request and stay within %s", p.MinMemoryRequest.String(), p.MaxMemoryLimit.String())
	}
	return nil
}

// placement combines the server's node selectors with the administrator's dedicated worker-pool taint and label.
func (p *GameWorkloadPolicy) placement(gs *gameplanev1alpha1.GameServer) (map[string]string, []corev1.Toleration) {
	if p == nil {
		return gs.Spec.NodeSelector, gs.Spec.Tolerations
	}
	selector := map[string]string{}
	maps.Copy(selector, gs.Spec.NodeSelector)
	selector[p.PoolLabel] = p.PoolValue
	return selector, []corev1.Toleration{{
		Key: p.PoolLabel, Operator: corev1.TolerationOpEqual, Value: p.PoolValue, Effect: corev1.TaintEffectNoSchedule,
	}}
}
