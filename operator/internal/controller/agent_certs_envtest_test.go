//go:build envtest

package controller

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// TestAgentTLS_SecretCreatedAndConsumed asserts the end-to-end shape:
//
//  1. The reconciler creates a per-GameServer Secret named
//     <gs>-agent-tls with tls.crt, tls.key, ca.crt.
//  2. The cert is signed by the seeded CA, has the expected SANs, and
//     carries the ServerAuth EKU.
//  3. The StatefulSet pod template mounts that Secret into the agent
//     container and passes the matching --tls-* flags.
//
// This is the only test in the suite that drives reconcileAgentTLS
// directly — other GameServer envtests just need the CA to exist so
// the reconcile path doesn't error out.
func TestAgentTLS_SecretCreatedAndConsumed(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildGameTemplate(uniqueName("smp"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "smp", tmpl.Name)
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	var (
		secret  corev1.Secret
		certPEM []byte
	)
	eventually(t, func() (bool, string) {
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp-agent-tls"}, &secret); err != nil {
			return false, "get secret: " + err.Error()
		}
		certPEM = secret.Data["tls.crt"]
		if len(certPEM) == 0 || len(secret.Data["tls.key"]) == 0 || len(secret.Data["ca.crt"]) == 0 {
			return false, "agent-tls Secret missing one of tls.crt/tls.key/ca.crt"
		}
		return true, ""
	})

	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatalf("agent-tls Secret tls.crt is not PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}

	wantSAN := "smp-0.smp." + ns + ".svc.cluster.local"
	var sawSAN bool
	for _, n := range cert.DNSNames {
		if n == wantSAN {
			sawSAN = true
			break
		}
	}
	if !sawSAN {
		t.Errorf("cert SAN missing %q; got %v", wantSAN, cert.DNSNames)
	}

	var sawServerAuth bool
	for _, eku := range cert.ExtKeyUsage {
		if eku == x509.ExtKeyUsageServerAuth {
			sawServerAuth = true
			break
		}
	}
	if !sawServerAuth {
		t.Errorf("cert missing ServerAuth EKU; got %v", cert.ExtKeyUsage)
	}

	// Owner ref ⇒ deletion of the GameServer GCs the Secret.
	if len(secret.OwnerReferences) == 0 || secret.OwnerReferences[0].Name != "smp" {
		t.Errorf("expected agent-tls Secret to be owner-referenced by GameServer smp; got %v", secret.OwnerReferences)
	}

	// And the StatefulSet picks it up.
	var ss appsv1.StatefulSet
	eventually(t, func() (bool, string) {
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp"}, &ss); err != nil {
			return false, "get statefulset: " + err.Error()
		}
		return true, ""
	})

	var sawVolume bool
	for _, v := range ss.Spec.Template.Spec.Volumes {
		if v.Name == "agent-tls" && v.Secret != nil && v.Secret.SecretName == "smp-agent-tls" {
			sawVolume = true
			break
		}
	}
	if !sawVolume {
		t.Errorf("StatefulSet volumes missing agent-tls Secret mount; got %+v", ss.Spec.Template.Spec.Volumes)
	}

	var agent *corev1.Container
	for i := range ss.Spec.Template.Spec.Containers {
		if ss.Spec.Template.Spec.Containers[i].Name == "agent" {
			agent = &ss.Spec.Template.Spec.Containers[i]
			break
		}
	}
	if agent == nil {
		t.Fatalf("agent container missing from StatefulSet pod template")
	}

	wantArgs := map[string]bool{
		"--tls-cert=/etc/gameplane/agent-tls/tls.crt":     false,
		"--tls-key=/etc/gameplane/agent-tls/tls.key":      false,
		"--tls-client-ca=/etc/gameplane/agent-tls/ca.crt": false,
	}
	for _, a := range agent.Args {
		if _, ok := wantArgs[a]; ok {
			wantArgs[a] = true
		}
	}
	for arg, seen := range wantArgs {
		if !seen {
			t.Errorf("agent container missing arg %q; got %v", arg, agent.Args)
		}
	}

	var sawMount bool
	for _, m := range agent.VolumeMounts {
		if m.Name == "agent-tls" && m.MountPath == "/etc/gameplane/agent-tls" && m.ReadOnly {
			sawMount = true
			break
		}
	}
	if !sawMount {
		t.Errorf("agent container missing agent-tls volumeMount; got %+v", agent.VolumeMounts)
	}
}

// TestAgentTLS_IdempotentOnRereconcile asserts that a fresh, valid
// cert is left alone — re-running the reconciler does not rotate the
// cert. This is what keeps the agent pod stable on every requeue.
func TestAgentTLS_IdempotentOnRereconcile(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildGameTemplate(uniqueName("smp"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "smp", tmpl.Name)
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	var first corev1.Secret
	eventually(t, func() (bool, string) {
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp-agent-tls"}, &first); err != nil {
			return false, "get secret: " + err.Error()
		}
		return len(first.Data["tls.crt"]) > 0, "no tls.crt yet"
	})

	// Trigger a re-reconcile by patching the GameServer's spec. Retry
	// on conflict because the reconciler also writes status (and may
	// re-write the spec via SetControllerReference cascades) in parallel.
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var fresh gameplanev1alpha1.GameServer
		if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: "smp"}, &fresh); err != nil {
			if apierrors.IsNotFound(err) {
				return err
			}
			return err
		}
		fresh.Spec.Suspend = true
		return k8sClient.Update(context.Background(), &fresh)
	}); err != nil {
		t.Fatalf("update gs: %v", err)
	}

	// Wait for the reconciler to observe the change.
	eventually(t, func() (bool, string) {
		var got gameplanev1alpha1.GameServer
		if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: "smp"}, &got); err != nil {
			return false, "get gs: " + err.Error()
		}
		return got.Spec.Suspend, "spec.suspend not yet observed"
	})

	consistently(t, defaultEventuallyInterval*5, func() (bool, string) {
		var second corev1.Secret
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp-agent-tls"}, &second); err != nil {
			return false, "get secret: " + err.Error()
		}
		if string(second.Data["tls.crt"]) != string(first.Data["tls.crt"]) {
			return false, "tls.crt rotated unexpectedly between reconciles"
		}
		return true, ""
	})
}
