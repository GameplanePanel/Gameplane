package preview

import (
	"fmt"
	"math"
	"strings"
)

// Module default ownership policy names, as the template contract spells them.
const (
	ownershipEveryStart  = "EveryStart"
	ownershipOnlyWrong   = "OnlyWhenWrong"
	ownershipSourceTmpl  = "template"
	ownershipSourceBuilt = "built-in default"
)

// RiskyPreview is one template-marked risky setting with its reason.
type RiskyPreview struct {
	Setting string `json:"setting"`
	Reason  string `json:"reason,omitempty"`
}

// SecurityPreview is the resolved spec.security block of a template: the
// module defaults that a server inherits unless its owner overrides them.
type SecurityPreview struct {
	RunAsUser  *int64 `json:"runAsUser,omitempty"`
	RunAsGroup *int64 `json:"runAsGroup,omitempty"`
	FSGroup    *int64 `json:"fsGroup,omitempty"`
	// VolumeOwnershipCheck is the resolved policy. It is empty when the
	// template has no fsGroup and sets no policy, because nothing applies.
	VolumeOwnershipCheck string `json:"volumeOwnershipCheck,omitempty"`
	// OwnershipCheckSource says where VolumeOwnershipCheck came from.
	OwnershipCheckSource string `json:"ownershipCheckSource,omitempty"`
	// OwnershipCheckNoEffect is true when a policy is set without an fsGroup.
	OwnershipCheckNoEffect bool           `json:"ownershipCheckNoEffect,omitempty"`
	Risky                  []RiskyPreview `json:"risky,omitempty"`
}

// securityRiskOrder is the fixed order in which risky settings are listed.
var securityRiskOrder = []string{"runAsUser", "runAsGroup", "fsGroup", "volumeOwnershipCheck"}

// toInt64Ptr converts a decoded YAML number to *int64. It returns nil for a
// missing value or a non-integral one; the validator reports those.
func toInt64Ptr(v any) *int64 {
	var out int64
	switch n := v.(type) {
	case int:
		out = int64(n)
	case int64:
		out = n
	case uint64:
		if n > math.MaxInt64 {
			return nil
		}
		out = int64(n)
	case float64:
		if n != float64(int64(n)) {
			return nil
		}
		out = int64(n)
	default:
		return nil
	}
	return &out
}

// buildSecurityPreview resolves a decoded spec.security mapping.
func buildSecurityPreview(sec map[string]any) *SecurityPreview {
	out := &SecurityPreview{
		RunAsUser:  toInt64Ptr(sec["runAsUser"]),
		RunAsGroup: toInt64Ptr(sec["runAsGroup"]),
		FSGroup:    toInt64Ptr(sec["fsGroup"]),
	}

	explicit, _ := sec["volumeOwnershipCheck"].(string)
	switch {
	case out.FSGroup != nil:
		// The policy only acts on a filesystem group. Omitted means OnlyWhenWrong.
		out.VolumeOwnershipCheck = ownershipOnlyWrong
		out.OwnershipCheckSource = ownershipSourceBuilt
		if explicit != "" {
			out.VolumeOwnershipCheck = explicit
			out.OwnershipCheckSource = ownershipSourceTmpl
		}
	case explicit != "":
		out.VolumeOwnershipCheck = explicit
		out.OwnershipCheckSource = ownershipSourceTmpl
		out.OwnershipCheckNoEffect = true
	}

	risky, _ := sec["risky"].(map[string]any)
	for _, name := range securityRiskOrder {
		member, marked := risky[name]
		if !marked {
			continue
		}
		reason := ""
		if m, ok := member.(map[string]any); ok {
			reason, _ = m["reason"].(string)
		}
		out.Risky = append(out.Risky, RiskyPreview{Setting: name, Reason: reason})
	}

	return out
}

// formatSecurityHuman renders the resolved security block for the terminal.
func formatSecurityHuman(s *SecurityPreview) string {
	var sb strings.Builder
	sb.WriteString("Security Context:\n")
	if s == nil {
		sb.WriteString("  image default (template sets no security block)\n")
		return sb.String()
	}

	fmt.Fprintf(&sb, "  %-22s %s\n", "runAsUser:", uidText(s.RunAsUser))
	fmt.Fprintf(&sb, "  %-22s %s\n", "runAsGroup:", uidText(s.RunAsGroup))
	fmt.Fprintf(&sb, "  %-22s %s\n", "fsGroup:", uidText(s.FSGroup))

	switch {
	case s.VolumeOwnershipCheck == "":
		fmt.Fprintf(&sb, "  %-22s %s\n", "volumeOwnershipCheck:", "not applicable (no fsGroup)")
	case s.OwnershipCheckNoEffect:
		fmt.Fprintf(&sb, "  %-22s %s (no effect: fsGroup is not set)\n", "volumeOwnershipCheck:", s.VolumeOwnershipCheck)
	default:
		fmt.Fprintf(&sb, "  %-22s %s (%s)\n", "volumeOwnershipCheck:", s.VolumeOwnershipCheck, s.OwnershipCheckSource)
	}

	if len(s.Risky) == 0 {
		fmt.Fprintf(&sb, "  %-22s %s\n", "risky settings:", "none")
		return sb.String()
	}
	sb.WriteString("  risky settings:\n")
	for _, r := range s.Risky {
		reason := r.Reason
		if reason == "" {
			reason = "no reason given"
		}
		fmt.Fprintf(&sb, "    - %s: %s\n", r.Setting, reason)
	}
	return sb.String()
}

// uidText shows a template value, or the image default when none is set.
func uidText(v *int64) string {
	if v == nil {
		return "image default"
	}
	return fmt.Sprintf("%d (module default)", *v)
}
