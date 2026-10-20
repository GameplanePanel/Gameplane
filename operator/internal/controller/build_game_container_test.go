package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// TestBuildGameContainer_ProbeOverride proves per-server probes win over
// the template one probe at a time, and unset per-server probes fall back
// to the template.
func TestBuildGameContainer_ProbeOverride(t *testing.T) {
	probe := func(delay int32) *corev1.Probe {
		return &corev1.Probe{InitialDelaySeconds: delay}
	}

	tmpl := &gameplanev1alpha1.GameTemplate{}
	tmpl.Name = "minecraft"
	tmpl.Spec.Game = "minecraft"
	tmpl.Spec.Probes = &gameplanev1alpha1.GameProbesSpec{
		Readiness: probe(1),
		Liveness:  probe(2),
		Startup:   probe(3),
	}

	gs := &gameplanev1alpha1.GameServer{}
	gs.Name = "alpha"
	// Override readiness + startup only; liveness should stay the template's.
	gs.Spec.Probes = &gameplanev1alpha1.GameProbesSpec{
		Readiness: probe(10),
		Startup:   probe(30),
	}

	c := buildGameContainer(gs, tmpl, "busybox:1.36", nil, &materializedConfig{})

	if c.ReadinessProbe == nil || c.ReadinessProbe.InitialDelaySeconds != 10 {
		t.Errorf("ReadinessProbe = %+v, want per-server (delay 10)", c.ReadinessProbe)
	}
	if c.LivenessProbe == nil || c.LivenessProbe.InitialDelaySeconds != 2 {
		t.Errorf("LivenessProbe = %+v, want template (delay 2)", c.LivenessProbe)
	}
	if c.StartupProbe == nil || c.StartupProbe.InitialDelaySeconds != 30 {
		t.Errorf("StartupProbe = %+v, want per-server (delay 30)", c.StartupProbe)
	}
}

// TestBuildGameContainer_NoServerProbes keeps the template probes when the
// server overrides none.
func TestBuildGameContainer_NoServerProbes(t *testing.T) {
	tmpl := &gameplanev1alpha1.GameTemplate{}
	tmpl.Name = "minecraft"
	tmpl.Spec.Game = "minecraft"
	tmpl.Spec.Probes = &gameplanev1alpha1.GameProbesSpec{
		Liveness: &corev1.Probe{InitialDelaySeconds: 7},
	}
	gs := &gameplanev1alpha1.GameServer{}
	gs.Name = "alpha"

	c := buildGameContainer(gs, tmpl, "busybox:1.36", nil, &materializedConfig{})
	if c.LivenessProbe == nil || c.LivenessProbe.InitialDelaySeconds != 7 {
		t.Errorf("LivenessProbe = %+v, want template (delay 7)", c.LivenessProbe)
	}
}

// TestBuildGameContainer_SecurityContext_Unset guards the byte-identical-
// when-omitted contract: a template with no Security block must render a
// nil container SecurityContext, not an empty &corev1.SecurityContext{} —
// the latter would still change the rendered pod spec (and roll every
// existing game StatefulSet on upgrade) even though nothing was requested.
func TestBuildGameContainer_SecurityContext_Unset(t *testing.T) {
	tmpl := &gameplanev1alpha1.GameTemplate{}
	tmpl.Name = "minecraft"
	tmpl.Spec.Game = "minecraft"
	gs := &gameplanev1alpha1.GameServer{}
	gs.Name = "alpha"

	c := buildGameContainer(gs, tmpl, "busybox:1.36", nil, &materializedConfig{})
	if c.SecurityContext != nil {
		t.Errorf("SecurityContext = %+v, want nil when template sets no security block", c.SecurityContext)
	}
}

// TestBuildGameContainer_SecurityContext_RunAsUserGroup proves the ARK
// case: a template declaring security.runAsUser/runAsGroup projects both
// onto the GAME container's SecurityContext (and nothing else — no
// implicit readOnlyRootFilesystem/capabilities/seccomp that could break a
// third-party image that doesn't expect them).
func TestBuildGameContainer_SecurityContext_RunAsUserGroup(t *testing.T) {
	uid := int64(25000)
	gid := int64(25000)
	tmpl := &gameplanev1alpha1.GameTemplate{}
	tmpl.Name = "ark-survival-ascended"
	tmpl.Spec.Game = "ark-survival-ascended"
	tmpl.Spec.Security = &gameplanev1alpha1.GameSecuritySpec{
		RunAsUser:  &uid,
		RunAsGroup: &gid,
	}
	gs := &gameplanev1alpha1.GameServer{}
	gs.Name = "ark"

	c := buildGameContainer(gs, tmpl, "mschnitzer/asa-linux-server:latest", nil, &materializedConfig{})
	sc := c.SecurityContext
	if sc == nil {
		t.Fatal("SecurityContext nil, want RunAsUser/RunAsGroup set")
	}
	if sc.RunAsUser == nil || *sc.RunAsUser != uid {
		t.Errorf("RunAsUser = %v, want %d", sc.RunAsUser, uid)
	}
	if sc.RunAsGroup == nil || *sc.RunAsGroup != gid {
		t.Errorf("RunAsGroup = %v, want %d", sc.RunAsGroup, gid)
	}
	if sc.ReadOnlyRootFilesystem != nil || sc.Capabilities != nil || sc.SeccompProfile != nil {
		t.Errorf("unexpected extra SecurityContext fields set: %+v", sc)
	}
}

// TestBuildGameContainer_HostportOnlyBindsAdvertisedPorts proves expose:
// Hostport only binds advertised ports on the node, mirroring the Advertise
// filter already applied by svcPortsFromTemplate/networkPolicyPortsFromTemplate.
// A non-advertised admin port (RCON, query) must never get a HostPort — that
// would bind it on the node IP even though it's not meant to be reachable
// off-pod.
func TestBuildGameContainer_HostportOnlyBindsAdvertisedPorts(t *testing.T) {
	tmpl := &gameplanev1alpha1.GameTemplate{}
	tmpl.Name = "minecraft"
	tmpl.Spec.Game = "minecraft"
	tmpl.Spec.Ports = []gameplanev1alpha1.GamePort{
		{Name: "game", ContainerPort: 25565, Protocol: corev1.ProtocolTCP, Advertise: true},
		{Name: "rcon", ContainerPort: 25575, Protocol: corev1.ProtocolTCP, Advertise: false},
	}

	gs := &gameplanev1alpha1.GameServer{}
	gs.Name = "alpha"
	gs.Spec.Networking = gameplanev1alpha1.GameServerNetworking{Expose: "Hostport"}

	c := buildGameContainer(gs, tmpl, "busybox:1.36", nil, &materializedConfig{})

	var game, rcon *corev1.ContainerPort
	for i := range c.Ports {
		switch c.Ports[i].Name {
		case "game":
			game = &c.Ports[i]
		case "rcon":
			rcon = &c.Ports[i]
		}
	}
	if game == nil {
		t.Fatal("game port missing from rendered container")
	}
	if game.HostPort != game.ContainerPort {
		t.Errorf("advertised port HostPort = %d, want %d (ContainerPort)", game.HostPort, game.ContainerPort)
	}
	if rcon == nil {
		t.Fatal("rcon port missing from rendered container")
	}
	if rcon.HostPort != 0 {
		t.Errorf("non-advertised port HostPort = %d, want 0 (unset)", rcon.HostPort)
	}
}

// TestGamePodSecurityContext proves fsGroup lands at the pod level (it's a
// PodSecurityContext-only field, unlike runAsUser/runAsGroup which are
// per-container) and that an unset security block renders no pod
// SecurityContext at all.
func TestGamePodSecurityContext(t *testing.T) {
	t.Run("unset renders nil", func(t *testing.T) {
		tmpl := &gameplanev1alpha1.GameTemplate{}
		if got := gamePodSecurityContext(tmpl); got != nil {
			t.Errorf("gamePodSecurityContext = %+v, want nil", got)
		}
	})

	t.Run("fsGroup projects onto the pod", func(t *testing.T) {
		gid := int64(25000)
		tmpl := &gameplanev1alpha1.GameTemplate{}
		tmpl.Spec.Security = &gameplanev1alpha1.GameSecuritySpec{FSGroup: &gid}
		got := gamePodSecurityContext(tmpl)
		if got == nil || got.FSGroup == nil || *got.FSGroup != gid {
			t.Errorf("gamePodSecurityContext = %+v, want FSGroup=%d", got, gid)
		}
	})
}
