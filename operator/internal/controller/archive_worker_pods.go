package controller

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// Archive worker Pods (spec 041) run the agent image's `archive-worker`
// subcommand against a backup archive. They are built here as plain
// objects; the reconcilers that own BackupExport and Restore decide when
// to create them.

const (
	// archiveWorkerPort is the TLS port the worker serves on in export and
	// receive modes.
	archiveWorkerPort = 9443

	// archiveWorkerLabel carries the worker mode ("export", "receive" or
	// "copy") on Pods and Services. On the TLS Secret it carries "tls".
	archiveWorkerLabel = "gameplane.local/archive-worker"

	// archiveWorkerUIDLabel carries the owning BackupExport or Restore UID.
	archiveWorkerUIDLabel = "gameplane.local/archive-worker-uid"
)

// archiveWorkerParams describes one archive worker Pod.
type archiveWorkerParams struct {
	Name, Namespace string // Pod, Service and TLS Secret share Name
	Mode            string // "export", "receive" or "copy"
	UID             string // BackupExport or Restore UID, passed as --uid

	AgentImage           string
	AgentImagePullPolicy corev1.PullPolicy
	ResticImage          string // empty -> resticImageOrDefault("")
	Resources            corev1.ResourceRequirements
	PodSecurityContext   *corev1.PodSecurityContext
	Env                  []corev1.EnvVar
	Volumes              []corev1.Volume
	VolumeMounts         []corev1.VolumeMount
	Affinity             *corev1.Affinity

	NeedsRestic bool // copy restic into /tools via an init container
	Serve       bool // export and receive serve TLS; copy does not
}

// workerDNSNames returns the DNS SANs for a worker's TLS certificate: the
// bare Service name and its namespace-qualified forms.
func workerDNSNames(name, namespace string) []string {
	return []string{
		name,
		name + "." + namespace,
		name + "." + namespace + ".svc",
		name + "." + namespace + ".svc.cluster.local",
	}
}

// buildArchiveWorkerTLSSecret stages the worker's server cert, key and CA
// bundle in the layout the worker reads from /tls.
func buildArchiveWorkerTLSSecret(name, namespace string, certPEM, keyPEM, caPEM []byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{archiveWorkerLabel: "tls"},
		},
		Type: corev1.SecretTypeTLS,
		Data: map[string][]byte{
			corev1.TLSCertKey:       certPEM,
			corev1.TLSPrivateKeyKey: keyPEM,
			"ca.crt":                caPEM,
		},
	}
}

// archiveWorkerContainerSecurityContext is the hardened security context
// shared by the worker and the restic-copy init container.
func archiveWorkerContainerSecurityContext() *corev1.SecurityContext {
	return &corev1.SecurityContext{
		RunAsNonRoot:             ptrTo(true),
		RunAsUser:                ptrTo(int64(65532)),
		ReadOnlyRootFilesystem:   ptrTo(true),
		AllowPrivilegeEscalation: ptrTo(false),
		Capabilities: &corev1.Capabilities{
			Drop: []corev1.Capability{"ALL"},
		},
	}
}

// buildArchiveWorkerPod builds the worker Pod for p.
func buildArchiveWorkerPod(p archiveWorkerParams) *corev1.Pod {
	labels := map[string]string{
		archiveWorkerLabel:    p.Mode,
		archiveWorkerUIDLabel: p.UID,
	}

	volumes := []corev1.Volume{
		{
			Name:         "tmp",
			VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
		},
	}
	mounts := []corev1.VolumeMount{
		{Name: "tmp", MountPath: "/tmp"},
	}
	if p.NeedsRestic {
		volumes = append(volumes, corev1.Volume{
			Name:         "tools",
			VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
		})
		mounts = append(mounts, corev1.VolumeMount{Name: "tools", MountPath: "/tools"})
	}
	if p.Serve {
		volumes = append(volumes, corev1.Volume{
			Name: "tls",
			VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{
				SecretName:  p.Name,
				DefaultMode: ptrTo(int32(0o400)),
			}},
		})
		mounts = append(mounts, corev1.VolumeMount{Name: "tls", MountPath: "/tls", ReadOnly: true})
	}
	volumes = append(volumes, p.Volumes...)
	mounts = append(mounts, p.VolumeMounts...)

	args := []string{"archive-worker", p.Mode, "--uid", p.UID}
	var ports []corev1.ContainerPort
	var readiness *corev1.Probe
	if p.Serve {
		args = append(args,
			"--addr", ":9443",
			"--cert", "/tls/tls.crt",
			"--key", "/tls/tls.key",
			"--client-ca", "/tls/ca.crt",
		)
		ports = []corev1.ContainerPort{{
			Name:          "archive",
			ContainerPort: archiveWorkerPort,
			Protocol:      corev1.ProtocolTCP,
		}}
		readiness = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt(archiveWorkerPort)},
			},
			PeriodSeconds: 2,
		}
	}

	env := make([]corev1.EnvVar, 0, len(p.Env)+1)
	env = append(env, p.Env...)
	env = append(env, corev1.EnvVar{Name: "XDG_CACHE_HOME", Value: "/tmp/restic-cache"})

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      p.Name,
			Namespace: p.Namespace,
			Labels:    labels,
		},
		Spec: corev1.PodSpec{
			RestartPolicy:                corev1.RestartPolicyNever,
			AutomountServiceAccountToken: ptrTo(false),
			EnableServiceLinks:           ptrTo(false),
			SecurityContext:              p.PodSecurityContext,
			Affinity:                     p.Affinity,
			Volumes:                      volumes,
			Containers: []corev1.Container{{
				Name:                     "worker",
				Image:                    p.AgentImage,
				ImagePullPolicy:          p.AgentImagePullPolicy,
				Args:                     args,
				Env:                      env,
				Resources:                p.Resources,
				Ports:                    ports,
				ReadinessProbe:           readiness,
				VolumeMounts:             mounts,
				TerminationMessagePolicy: corev1.TerminationMessageReadFile,
				SecurityContext:          archiveWorkerContainerSecurityContext(),
			}},
		},
	}

	if p.NeedsRestic {
		pod.Spec.InitContainers = []corev1.Container{{
			Name:      "tools",
			Image:     resticImageOrDefault(p.ResticImage),
			Command:   []string{"/bin/cp", "/usr/bin/restic", "/tools/restic"},
			Resources: p.Resources,
			VolumeMounts: []corev1.VolumeMount{
				{Name: "tools", MountPath: "/tools"},
			},
			SecurityContext: archiveWorkerContainerSecurityContext(),
		}}
	}

	return pod
}

// buildArchiveWorkerService exposes a serving worker Pod on its TLS port,
// selected by the owning UID and mode labels.
func buildArchiveWorkerService(name, namespace, mode, uid string) *corev1.Service {
	labels := map[string]string{
		archiveWorkerLabel:    mode,
		archiveWorkerUIDLabel: uid,
	}
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: labels,
			Ports: []corev1.ServicePort{{
				Name:       "archive",
				Port:       archiveWorkerPort,
				TargetPort: intstr.FromInt(archiveWorkerPort),
				Protocol:   corev1.ProtocolTCP,
			}},
		},
	}
}
