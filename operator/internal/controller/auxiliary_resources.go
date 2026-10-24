package controller

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/validation"
)

// ParseAuxiliaryResources parses an install-level resource override. An absent
// flag returns nil (use defaults); an explicit object, including {}, replaces
// defaults wholesale. Reject typos before the operator starts creating pods.
func ParseAuxiliaryResources(value string) (*corev1.ResourceRequirements, error) {
	if value == "" {
		return nil, nil
	}
	var configured *corev1.ResourceRequirements
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&configured); err != nil {
		return nil, fmt.Errorf("decode resources: %w", err)
	}
	if configured == nil {
		return nil, fmt.Errorf("resources must be a JSON object; use {} to clear defaults")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("resources must contain exactly one JSON object")
	}
	if len(configured.Claims) != 0 {
		return nil, fmt.Errorf("resource claims are not supported for auxiliary containers")
	}
	for _, values := range []corev1.ResourceList{configured.Requests, configured.Limits} {
		for name, quantity := range values {
			if len(validation.IsQualifiedName(string(name))) != 0 || quantity.Sign() < 0 {
				return nil, fmt.Errorf("invalid resource %q or negative quantity", name)
			}
		}
	}
	for name, request := range configured.Requests {
		if limit, exists := configured.Limits[name]; exists && request.Cmp(limit) > 0 {
			return nil, fmt.Errorf("request for %s exceeds its limit", name)
		}
	}
	return configured, nil
}

func auxiliaryResources(configured *corev1.ResourceRequirements, cpu, memory string) corev1.ResourceRequirements {
	if configured != nil {
		return *configured.DeepCopy()
	}
	return corev1.ResourceRequirements{Requests: corev1.ResourceList{
		corev1.ResourceCPU: resource.MustParse(cpu), corev1.ResourceMemory: resource.MustParse(memory),
	}}
}

func optionalResources(values []*corev1.ResourceRequirements) *corev1.ResourceRequirements {
	if len(values) == 0 {
		return nil
	}
	return values[0]
}
