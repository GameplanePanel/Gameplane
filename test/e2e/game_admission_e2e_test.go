//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// Seed an administrator-owned namespace before it joins operator.gameNamespaces.
// Its unprivileged test account lets the upgrade exercise PATCH of a legacy Secret.
func prepareAdmissionFixture(t *testing.T, ns string) {
	t.Helper()
	ctx := t.Context()
	core := envInstance.K8s.CoreV1()
	if _, err := core.Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		if err := core.Namespaces().Delete(cleanup, ns, metav1.DeleteOptions{}); err != nil {
			t.Errorf("delete admission fixture: %v", err)
		}
	})
	if _, err := core.ServiceAccounts(ns).Create(ctx, &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "legacy-fixture"}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "legacy-token", Annotations: map[string]string{corev1.ServiceAccountNameKey: "legacy-fixture"}},
		Type:       corev1.SecretTypeServiceAccountToken,
	}
	if _, err := core.Secrets(ns).Create(ctx, secret, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
}

func checkGameAdmission(t *testing.T, extraNS string, game *corev1.Pod) {
	t.Helper()
	ctx := t.Context()
	core := envInstance.K8s.CoreV1()
	dryRun := metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}}
	api := gameplaneServiceAccountClient(t, "gameplane-api")
	if _, err := api.CoreV1().Secrets("gameplane-system").Get(ctx, "gameplane-agent-ca", metav1.GetOptions{}); !apierrors.IsForbidden(err) {
		t.Fatalf("API reading agent signer: %v", err)
	}
	checkModuleConfigMapIsolation(t, api)
	for _, ns := range []string{extraNS, game.Namespace, "gameplane-system", credentialsNS} {
		secrets := core.Secrets(ns)
		if ns == credentialsNS || ns == game.Namespace {
			secrets = api.CoreV1().Secrets(ns)
		}
		secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{GenerateName: "admission-"}}
		if _, err := secrets.Create(ctx, secret, dryRun); err != nil {
			t.Fatalf("ordinary Secret in %s: %v", ns, err)
		}
		secret.Type = corev1.SecretTypeServiceAccountToken
		secret.Annotations = map[string]string{corev1.ServiceAccountNameKey: "default"}
		_, err := secrets.Create(ctx, secret, dryRun)
		if !apierrors.IsForbidden(err) || !strings.Contains(err.Error(), "bound-tokens.") {
			t.Fatalf("token admission in %s: %v", ns, err)
		}
	}
	_, err := core.Secrets(extraNS).Patch(ctx, "legacy-token", types.MergePatchType,
		[]byte(`{"metadata":{"labels":{"exercise":"update"}}}`), metav1.PatchOptions{DryRun: dryRun.DryRun})
	if !apierrors.IsForbidden(err) || !strings.Contains(err.Error(), "bound-tokens.") {
		t.Fatalf("patching existing token: %v", err)
	}

	// This Pod would pass Restricted, but the namespaced guard must deny even
	// a compliant workload while the external namespace lacks its PSA labels.
	probe := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "admission-"},
		Spec: corev1.PodSpec{
			SecurityContext: game.Spec.SecurityContext,
			Containers:      []corev1.Container{{Name: "probe", Image: "busybox:1.36", SecurityContext: game.Spec.Containers[0].SecurityContext}},
		},
	}
	if _, err := core.Pods(extraNS).Create(ctx, probe, dryRun); !apierrors.IsForbidden(err) || !strings.Contains(err.Error(), "restricted-games.") {
		t.Fatalf("unlabelled managed namespace admitted a Pod or returned the wrong error: %v", err)
	}
	labels := []byte(`{"metadata":{"labels":{"pod-security.kubernetes.io/enforce":"restricted","pod-security.kubernetes.io/enforce-version":"v1.34"}}}`)
	if _, err := core.Namespaces().Patch(ctx, extraNS, types.MergePatchType, labels, metav1.PatchOptions{}); err != nil {
		t.Fatal(err)
	}
	envInstance.Eventually(t, time.Minute, func() (bool, string) {
		_, err := core.Pods(extraNS).Create(ctx, probe, dryRun)
		return err == nil, fmt.Sprintf("admitting the Restricted Pod: %v", err)
	})
	probe.Spec.Volumes = []corev1.Volume{{Name: "node", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/dev/null"}}}}
	// Let the Job controller attempt creation with its own identity. The node
	// selector keeps the probe unschedulable even if admission regresses.
	probe.Spec.NodeSelector = map[string]string{"gameplane.local/e2e-unschedulable": "true"}
	probe.Spec.RestartPolicy = corev1.RestartPolicyNever
	operator := gameplaneServiceAccountClient(t, "gameplane-operator")
	job, err := operator.BatchV1().Jobs(extraNS).Create(ctx, &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "hostpath-probe"},
		Spec:       batchv1.JobSpec{Template: corev1.PodTemplateSpec{Spec: probe.Spec}},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("operator creating the probe Job: %v", err)
	}
	envInstance.Eventually(t, time.Minute, func() (bool, string) {
		events, err := core.Events(extraNS).List(ctx, metav1.ListOptions{FieldSelector: "involvedObject.uid=" + string(job.UID)})
		if err != nil {
			return false, err.Error()
		}
		for _, event := range events.Items {
			if event.Reason == "FailedCreate" && strings.Contains(event.Message, "PodSecurity") && strings.Contains(event.Message, "hostPath") {
				return true, ""
			}
		}
		return false, "waiting for PodSecurity to reject the Job controller's hostPath Pod"
	})
}

func checkModuleConfigMapIsolation(t *testing.T, api kubernetes.Interface) {
	t.Helper()
	ctx := t.Context()
	release := api.CoreV1().ConfigMaps("gameplane-system")
	if _, err := release.Get(ctx, "gameplane-crd-manifests", metav1.GetOptions{}); !apierrors.IsForbidden(err) {
		t.Fatalf("API getting a release ConfigMap: %v", err)
	}
	if _, err := release.List(ctx, metav1.ListOptions{Limit: 1}); !apierrors.IsForbidden(err) {
		t.Fatalf("API listing release ConfigMaps: %v", err)
	}
	upload := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
		Name:   "module-upload-admission-probe",
		Labels: map[string]string{"gameplane.local/module-upload": "true"},
	}}
	dryRun := metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}}
	if _, err := release.Create(ctx, upload, dryRun); !apierrors.IsForbidden(err) {
		t.Fatalf("API creating a labelled upload in the release namespace: %v", err)
	}
	if _, err := api.CoreV1().ConfigMaps(credentialsNS).Create(ctx, upload, dryRun); err != nil {
		t.Fatalf("API creating a module upload in %s: %v", credentialsNS, err)
	}
}

func gameplaneServiceAccountClient(t *testing.T, name string) kubernetes.Interface {
	t.Helper()
	cfg := rest.CopyConfig(envInstance.Cfg)
	cfg.Impersonate = rest.ImpersonationConfig{UserName: "system:serviceaccount:gameplane-system:" + name,
		Groups: []string{"system:authenticated", "system:serviceaccounts", "system:serviceaccounts:gameplane-system"}}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return client
}
