package controller

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func captureFilterTemplate(name string, ports ...gameplanev1alpha1.GamePort) *gameplanev1alpha1.GameTemplate {
	return &gameplanev1alpha1.GameTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       gameplanev1alpha1.GameTemplateSpec{Ports: ports},
	}
}

// TestDefaultCaptureFilter_CoversOnlyAdvertisedPortsAndProtocols checks
// that the default capture filter names each advertised port with its
// protocol and leaves out ports that are not advertised.
func TestDefaultCaptureFilter_CoversOnlyAdvertisedPortsAndProtocols(t *testing.T) {
	cases := []struct {
		name  string
		ports []gameplanev1alpha1.GamePort
		want  string
	}{
		{
			name: "single tcp port",
			ports: []gameplanev1alpha1.GamePort{
				{Name: "game", ContainerPort: 25565, Protocol: corev1.ProtocolTCP, Advertise: true},
			},
			want: "tcp port 25565",
		},
		{
			name: "mixed protocols, unadvertised rcon left out",
			ports: []gameplanev1alpha1.GamePort{
				{Name: "game", ContainerPort: 2456, Protocol: corev1.ProtocolUDP, Advertise: true},
				{Name: "query", ContainerPort: 2457, Protocol: corev1.ProtocolUDP, Advertise: true},
				{Name: "rcon", ContainerPort: 25575, Protocol: corev1.ProtocolTCP, Advertise: false},
				{Name: "web", ContainerPort: 8080, Protocol: corev1.ProtocolTCP, Advertise: true},
			},
			want: "udp port 2456 or udp port 2457 or tcp port 8080",
		},
		{
			name: "same port on both protocols, duplicate dropped, empty protocol is tcp",
			ports: []gameplanev1alpha1.GamePort{
				{Name: "game", ContainerPort: 7777, Advertise: true},
				{Name: "game-udp", ContainerPort: 7777, Protocol: corev1.ProtocolUDP, Advertise: true},
				{Name: "game-again", ContainerPort: 7777, Protocol: corev1.ProtocolTCP, Advertise: true},
			},
			want: "tcp port 7777 or udp port 7777",
		},
		{
			name: "nothing advertised",
			ports: []gameplanev1alpha1.GamePort{
				{Name: "rcon", ContainerPort: 25575, Protocol: corev1.ProtocolTCP, Advertise: false},
			},
			want: "",
		},
		{
			name:  "no ports",
			ports: nil,
			want:  "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := defaultCaptureFilter(captureFilterTemplate("tmpl", c.ports...))
			if got != c.want {
				t.Errorf("defaultCaptureFilter = %q, want %q", got, c.want)
			}
		})
	}
}

// TestCaptureFilter_ExplicitFilterWins checks that a capture's own filter
// is sent unchanged, without reading the template.
func TestCaptureFilter_ExplicitFilterWins(t *testing.T) {
	s := testScheme(t)
	cl := fake.NewClientBuilder().WithScheme(s).Build()
	r := &NetworkCaptureReconciler{Client: cl, Scheme: s}
	explicit := "udp port 9999"
	nc := &gameplanev1alpha1.NetworkCapture{Spec: gameplanev1alpha1.NetworkCaptureSpec{Filter: &explicit}}
	gs := &gameplanev1alpha1.GameServer{Spec: gameplanev1alpha1.GameServerSpec{
		TemplateRef: gameplanev1alpha1.GameTemplateRef{Name: "missing-template"},
	}}

	got, msg, err := r.captureFilter(context.Background(), nc, gs)
	if err != nil || msg != "" {
		t.Fatalf("captureFilter: msg=%q err=%v", msg, err)
	}
	if got == nil || *got != explicit {
		t.Fatalf("filter = %v, want %q", got, explicit)
	}
}

// TestCaptureFilter_DefaultsToTemplatePorts checks that a capture with no
// filter, or an empty one, gets the filter built from its template.
func TestCaptureFilter_DefaultsToTemplatePorts(t *testing.T) {
	s := testScheme(t)
	tmpl := captureFilterTemplate("mc",
		gameplanev1alpha1.GamePort{Name: "game", ContainerPort: 25565, Protocol: corev1.ProtocolTCP, Advertise: true},
		gameplanev1alpha1.GamePort{Name: "rcon", ContainerPort: 25575, Protocol: corev1.ProtocolTCP, Advertise: false},
	)
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(tmpl).Build()
	r := &NetworkCaptureReconciler{Client: cl, Scheme: s}
	gs := &gameplanev1alpha1.GameServer{Spec: gameplanev1alpha1.GameServerSpec{
		TemplateRef: gameplanev1alpha1.GameTemplateRef{Name: "mc"},
	}}
	empty := ""
	for name, filter := range map[string]*string{"nil filter": nil, "empty filter": &empty} {
		nc := &gameplanev1alpha1.NetworkCapture{Spec: gameplanev1alpha1.NetworkCaptureSpec{Filter: filter}}
		got, msg, err := r.captureFilter(context.Background(), nc, gs)
		if err != nil || msg != "" {
			t.Fatalf("%s: captureFilter: msg=%q err=%v", name, msg, err)
		}
		if got == nil || *got != "tcp port 25565" {
			t.Errorf("%s: filter = %v, want \"tcp port 25565\"", name, got)
		}
	}
}

// TestCaptureFilter_FailsWithoutAnAdvertisedPort checks that a capture
// with no filter fails with a message, rather than sending an empty
// filter, when the template advertises no port or is missing.
func TestCaptureFilter_FailsWithoutAnAdvertisedPort(t *testing.T) {
	s := testScheme(t)
	tmpl := captureFilterTemplate("quiet",
		gameplanev1alpha1.GamePort{Name: "rcon", ContainerPort: 25575, Protocol: corev1.ProtocolTCP, Advertise: false},
	)
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(tmpl).Build()
	r := &NetworkCaptureReconciler{Client: cl, Scheme: s}
	nc := &gameplanev1alpha1.NetworkCapture{}

	cases := map[string]struct{ template, want string }{
		"no advertised port": {template: "quiet", want: "advertises no ports"},
		"missing template":   {template: "absent", want: "not found"},
	}
	for name, c := range cases {
		gs := &gameplanev1alpha1.GameServer{Spec: gameplanev1alpha1.GameServerSpec{
			TemplateRef: gameplanev1alpha1.GameTemplateRef{Name: c.template},
		}}
		got, msg, err := r.captureFilter(context.Background(), nc, gs)
		if err != nil {
			t.Fatalf("%s: captureFilter: %v", name, err)
		}
		if got != nil {
			t.Errorf("%s: filter = %q, want none", name, *got)
		}
		if !strings.Contains(msg, c.want) {
			t.Errorf("%s: message = %q, want it to contain %q", name, msg, c.want)
		}
	}
}
