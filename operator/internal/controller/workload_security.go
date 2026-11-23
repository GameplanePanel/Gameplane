package controller

import (
	"errors"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

const agentTokenVolumeName = "agent-api-token"
const workloadSecurityLabel = "gameplane.local/workload-security"

// restrictedIdentityMessage is the failure reported for a restricted server
// whose effective identity is not usable (OD-5 A: validation uses the effective
// values, so an override is refused by the same rule as a template default).
const restrictedIdentityMessage = "restricted workload requires template security.runAsUser and security.fsGroup greater than zero, and a positive security.runAsGroup when specified; use an image compatible with that identity"

// Images must declare their compatible numeric identity; guessing from an
// image name or trusting an unknown image USER only moves failure to kubelet.
func validateRestrictedTemplate(tmpl *gameplanev1alpha1.GameTemplate) error {
	return validateRestrictedSecurity(resolveGameSecurity(tmpl, nil))
}

// validateRestrictedSecurity applies the restricted identity rule to the
// effective values of one server. An absent or "use image default" run-as user
// and filesystem group are refused, because restricted mode needs both. An
// absent group is allowed and falls back to the filesystem group.
func validateRestrictedSecurity(sec resolvedGameSecurity) error {
	if sec.RunAsUser.Value == nil || *sec.RunAsUser.Value <= 0 ||
		sec.FSGroup.Value == nil || *sec.FSGroup.Value <= 0 ||
		(sec.RunAsGroup.Value != nil && *sec.RunAsGroup.Value <= 0) {
		return errors.New(restrictedIdentityMessage)
	}
	return nil
}

func restrictedContainerSecurity(sec resolvedGameSecurity) *corev1.SecurityContext {
	group := sec.RunAsGroup.Value
	if group == nil {
		group = sec.FSGroup.Value
	}
	return &corev1.SecurityContext{
		RunAsUser: sec.RunAsUser.Value, RunAsGroup: group,
		RunAsNonRoot: ptrTo(true), AllowPrivilegeEscalation: ptrTo(false),
		Capabilities:   &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
	}
}

// All profiles restrict the mounted API credentials to the agent. Shared PID
// remains intentional for metrics: this is mount isolation, not a hard trust
// boundary against a process that can inspect another process through /proc.
func applyWorkloadSecurity(pod *corev1.PodSpec, tmpl *gameplanev1alpha1.GameTemplate, restricted bool, expirationSeconds int64) {
	applyWorkloadSecurityResolved(pod, resolveGameSecurity(tmpl, nil), restricted, expirationSeconds)
}

// applyWorkloadSecurityResolved is applyWorkloadSecurity with the effective
// values of one server. Restricted mode takes its identity from sec, so a
// server override reaches the game and init containers (OD-5 A).
func applyWorkloadSecurityResolved(pod *corev1.PodSpec, sec resolvedGameSecurity, restricted bool, expirationSeconds int64) {
	if expirationSeconds < 600 || expirationSeconds > 3600 {
		expirationSeconds = 3600
	}
	pod.AutomountServiceAccountToken = ptrTo(false)
	pod.Volumes = append(pod.Volumes, corev1.Volume{Name: agentTokenVolumeName, VolumeSource: corev1.VolumeSource{Projected: &corev1.ProjectedVolumeSource{
		DefaultMode: ptrTo(int32(0o644)), Sources: []corev1.VolumeProjection{
			{ServiceAccountToken: &corev1.ServiceAccountTokenProjection{Path: "token", ExpirationSeconds: &expirationSeconds}},
			{ConfigMap: &corev1.ConfigMapProjection{LocalObjectReference: corev1.LocalObjectReference{Name: "kube-root-ca.crt"}, Items: []corev1.KeyToPath{{Key: "ca.crt", Path: "ca.crt"}}}},
			{DownwardAPI: &corev1.DownwardAPIProjection{Items: []corev1.DownwardAPIVolumeFile{{Path: "namespace", FieldRef: &corev1.ObjectFieldSelector{APIVersion: "v1", FieldPath: "metadata.namespace"}}}}},
		},
	}}})
	for i := range pod.Containers {
		c := &pod.Containers[i]
		if c.Name == "agent" {
			c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{Name: agentTokenVolumeName, MountPath: "/var/run/secrets/kubernetes.io/serviceaccount", ReadOnly: true})
		}
		if restricted && c.Name == "game" {
			c.SecurityContext = restrictedContainerSecurity(sec)
		}
	}
	if restricted {
		pod.SecurityContext.RunAsNonRoot = ptrTo(true)
		pod.SecurityContext.SeccompProfile = &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}
		for i := range pod.InitContainers {
			pod.InitContainers[i].SecurityContext = restrictedContainerSecurity(sec)
		}
	}
}
