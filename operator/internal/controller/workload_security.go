package controller

import (
	"fmt"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

const agentTokenVolumeName = "agent-api-token"
const workloadSecurityLabel = "gameplane.local/workload-security"

// Images must declare their compatible numeric identity; guessing from an
// image name or trusting an unknown image USER only moves failure to kubelet.
func validateRestrictedTemplate(tmpl *gameplanev1alpha1.GameTemplate) error {
	sec := tmpl.Spec.Security
	if sec == nil || sec.RunAsUser == nil || *sec.RunAsUser <= 0 || sec.FSGroup == nil || *sec.FSGroup <= 0 || (sec.RunAsGroup != nil && *sec.RunAsGroup <= 0) {
		return fmt.Errorf("restricted workload requires template security.runAsUser and security.fsGroup greater than zero, and a positive security.runAsGroup when specified; use an image compatible with that identity")
	}
	return nil
}

func restrictedContainerSecurity(tmpl *gameplanev1alpha1.GameTemplate) *corev1.SecurityContext {
	group := tmpl.Spec.Security.RunAsGroup
	if group == nil {
		group = tmpl.Spec.Security.FSGroup
	}
	return &corev1.SecurityContext{
		RunAsUser: tmpl.Spec.Security.RunAsUser, RunAsGroup: group,
		RunAsNonRoot: ptrTo(true), AllowPrivilegeEscalation: ptrTo(false),
		Capabilities:   &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
	}
}

// All profiles restrict the mounted API credentials to the agent. Shared PID
// remains intentional for metrics: this is mount isolation, not a hard trust
// boundary against a process that can inspect another process through /proc.
func applyWorkloadSecurity(pod *corev1.PodSpec, tmpl *gameplanev1alpha1.GameTemplate, restricted bool, expirationSeconds int64) {
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
			c.SecurityContext = restrictedContainerSecurity(tmpl)
		}
	}
	if restricted {
		pod.SecurityContext.RunAsNonRoot = ptrTo(true)
		pod.SecurityContext.SeccompProfile = &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}
		for i := range pod.InitContainers {
			pod.InitContainers[i].SecurityContext = restrictedContainerSecurity(tmpl)
		}
	}
}
