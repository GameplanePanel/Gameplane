package controller

import (
	"testing"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// TestEffectiveConsoleMode tests precedence rules for template console modes.
func TestEffectiveConsoleMode(t *testing.T) {
	cases := []struct {
		name string
		spec gameplanev1alpha1.GameTemplateSpec
		want string
	}{
		{
			name: "explicit pty wins over rcon spec",
			spec: gameplanev1alpha1.GameTemplateSpec{
				ConsoleMode: "pty",
				RCON:        &gameplanev1alpha1.RCONSpec{Protocol: "source"},
			},
			want: "pty",
		},
		{
			name: "explicit none wins over rcon spec",
			spec: gameplanev1alpha1.GameTemplateSpec{
				ConsoleMode: "none",
				RCON:        &gameplanev1alpha1.RCONSpec{Protocol: "source"},
			},
			want: "none",
		},
		{
			name: "explicit rcon",
			spec: gameplanev1alpha1.GameTemplateSpec{ConsoleMode: "rcon"},
			want: "rcon",
		},
		{
			name: "default with rcon source → rcon",
			spec: gameplanev1alpha1.GameTemplateSpec{
				RCON: &gameplanev1alpha1.RCONSpec{Protocol: "source"},
			},
			want: "rcon",
		},
		{
			name: "default with rcon cli → pty",
			spec: gameplanev1alpha1.GameTemplateSpec{
				RCON: &gameplanev1alpha1.RCONSpec{Protocol: "cli"},
			},
			want: "pty",
		},
		{
			name: "default with rcon protocol=none → none",
			spec: gameplanev1alpha1.GameTemplateSpec{
				RCON: &gameplanev1alpha1.RCONSpec{Protocol: "none"},
			},
			want: "none",
		},
		{
			name: "default with no rcon at all → none",
			spec: gameplanev1alpha1.GameTemplateSpec{},
			want: "none",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := EffectiveConsoleMode(&gameplanev1alpha1.GameTemplate{Spec: tc.spec})
			if got != tc.want {
				t.Errorf("got %q want %q", got, tc.want)
			}
		})
	}

	if got := EffectiveConsoleMode(nil); got != "none" {
		t.Errorf("nil template: got %q want \"none\"", got)
	}
}

// ownerBoolPtr returns a pointer to a bool value, used in Kubernetes OwnerReference tests.
func ownerBoolPtr(b bool) *bool {
	return &b
}
