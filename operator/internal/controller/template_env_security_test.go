package controller

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// tmplEnvSecretRef returns an env var sourced from a Secret key.
func tmplEnvSecretRef(name, secret string) corev1.EnvVar {
	return corev1.EnvVar{
		Name: name,
		ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: secret},
			Key:                  "password",
		}},
	}
}

// tmplEnvConfigMapRef returns an env var sourced from a ConfigMap key.
func tmplEnvConfigMapRef(name, cm string) corev1.EnvVar {
	return corev1.EnvVar{
		Name: name,
		ValueFrom: &corev1.EnvVarSource{ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: cm},
			Key:                  "motd",
		}},
	}
}

// TestValidateTemplateEnvSourcesSecurity verifies that Secret and ConfigMap
// references from the GameTemplate or GameVersion env must be owned by the
// GameServer, and that refusals name the template.
func TestValidateTemplateEnvSourcesSecurity(t *testing.T) {
	s := testScheme(t)
	ownedBy := func(uid string) []metav1.OwnerReference {
		return []metav1.OwnerReference{{
			APIVersion: "gameplane.local/v1alpha1",
			Kind:       "GameServer",
			Name:       "alpha",
			UID:        types.UID(uid),
		}}
	}
	objs := []corev1.Secret{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "alpha-rcon", Namespace: "games", OwnerReferences: ownedBy("alpha-uid")},
			Data:       map[string][]byte{"password": []byte("own")},
		},
		{
			// Owned by a different GameServer that happens to share the name prefix.
			ObjectMeta: metav1.ObjectMeta{Name: "beta-rcon", Namespace: "games", OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "gameplane.local/v1alpha1",
				Kind:       "GameServer",
				Name:       "beta",
				UID:        "beta-uid",
			}}},
			Data: map[string][]byte{"password": []byte("beta")},
		},
		{
			// Backup destination style secret with no owner at all.
			ObjectMeta: metav1.ObjectMeta{Name: "restic-repo-backups", Namespace: "games"},
			Data:       map[string][]byte{"password": []byte("restic")},
		},
	}
	cms := []corev1.ConfigMap{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "alpha-config", Namespace: "games", OwnerReferences: ownedBy("alpha-uid")},
			Data:       map[string]string{"motd": "hi"},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "shared-config", Namespace: "games"},
			Data:       map[string]string{"motd": "shared"},
		},
	}

	var clientObjs []client.Object
	for i := range objs {
		clientObjs = append(clientObjs, &objs[i])
	}
	for i := range cms {
		clientObjs = append(clientObjs, &cms[i])
	}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(clientObjs...).Build()
	r := &GameServerReconciler{Client: cl, Scheme: s}

	gs := probeSecGameServer()

	tmplWith := func(env ...corev1.EnvVar) *gameplanev1alpha1.GameTemplate {
		tmpl := probeSecTemplate(nil)
		tmpl.Spec.Env = env
		return tmpl
	}
	verWith := func(env ...corev1.EnvVar) *gameplanev1alpha1.GameVersion {
		return &gameplanev1alpha1.GameVersion{ID: "1.21.4-paper", DisplayName: "1.21.4 (Paper)", Env: env}
	}

	cases := []struct {
		name    string
		tmpl    *gameplanev1alpha1.GameTemplate
		ver     *gameplanev1alpha1.GameVersion
		wantErr string // substring; empty means no error expected
	}{
		{
			name:    "template secret owned by another server is refused",
			tmpl:    tmplWith(tmplEnvSecretRef("RCON", "beta-rcon")),
			wantErr: "not owned by GameServer",
		},
		{
			name:    "template configmap not owned by server is refused",
			tmpl:    tmplWith(tmplEnvConfigMapRef("MOTD", "shared-config")),
			wantErr: "not owned by GameServer",
		},
		{
			name:    "template secret that is a backup destination is refused",
			tmpl:    tmplWith(tmplEnvSecretRef("RESTIC", "restic-repo-backups")),
			wantErr: "not owned by GameServer",
		},
		{
			name:    "version secret owned by another server is refused",
			tmpl:    tmplWith(),
			ver:     verWith(tmplEnvSecretRef("RCON", "beta-rcon")),
			wantErr: "not owned by GameServer",
		},
		{
			name:    "version configmap not owned by server is refused",
			tmpl:    tmplWith(),
			ver:     verWith(tmplEnvConfigMapRef("MOTD", "shared-config")),
			wantErr: "not owned by GameServer",
		},
		{
			name:    "missing referenced secret is refused",
			tmpl:    tmplWith(tmplEnvSecretRef("RCON", "does-not-exist")),
			wantErr: "does-not-exist",
		},
		{
			name: "server-owned secret and configmap pass",
			tmpl: tmplWith(
				tmplEnvSecretRef("RCON", "alpha-rcon"),
				tmplEnvConfigMapRef("MOTD", "alpha-config"),
			),
			ver: verWith(tmplEnvSecretRef("RCON_VERSION", "alpha-rcon")),
		},
		{
			name: "plain env values pass",
			tmpl: tmplWith(corev1.EnvVar{Name: "EULA", Value: "true"}),
			ver:  verWith(corev1.EnvVar{Name: "TYPE", Value: "PAPER"}),
		},
		{
			name: "no env at all passes",
			tmpl: tmplWith(),
		},
		{
			name: "empty version env passes",
			tmpl: tmplWith(),
			ver:  verWith(),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := r.validateTemplateEnvSources(context.Background(), gs, tc.tmpl, tc.ver)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.HasPrefix(err.Error(), "template minecraft env: ") {
				t.Errorf("error %q not wrapped with template name", err.Error())
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}
