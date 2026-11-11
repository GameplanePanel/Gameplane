package controller

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/validation"

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
	if decoder.Decode(new(any)) != io.EOF {
		return nil, fmt.Errorf("expected a single game workload policy object")
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
