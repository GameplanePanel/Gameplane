package controller

import (
	"context"
	"encoding/json"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func revisionFixture(t *testing.T) (*GameServerReconciler, *gameplanev1alpha1.GameServer, *gameplanev1alpha1.GameTemplate) {
	t.Helper()
	s := testScheme(t)
	if err := batchv1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	gs := &gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{Name: "game", Namespace: "games", UID: "server-uid"}, Spec: gameplanev1alpha1.GameServerSpec{TemplateRef: gameplanev1alpha1.GameTemplateRef{Name: "template"}, TemplateRollout: &gameplanev1alpha1.TemplateRolloutSpec{Policy: "OnRequest"}}}
	tmpl := &gameplanev1alpha1.GameTemplate{ObjectMeta: metav1.ObjectMeta{Name: "template"}, Spec: gameplanev1alpha1.GameTemplateSpec{Image: "example.invalid/game:old", Env: []corev1.EnvVar{{Name: "REVISION", Value: "old"}}}}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(gs, tmpl).WithStatusSubresource(gs, &appsv1.StatefulSet{}, &gameplanev1alpha1.Backup{}).Build()
	r := &GameServerReconciler{Client: c, APIReader: c, Scheme: s}
	digest, err := r.saveTemplateSnapshot(t.Context(), gs, tmpl)
	if err != nil {
		t.Fatal(err)
	}
	gs.Annotations = map[string]string{effectiveTemplateAnnotation: digest}
	if err := c.Update(t.Context(), gs); err != nil {
		t.Fatal(err)
	}
	return r, gs, tmpl
}

func TestTemplateOnRequestRetainsCompleteContents(t *testing.T) {
	r, gs, installed := revisionFixture(t)
	old := gs.Annotations[effectiveTemplateAnnotation]
	installed.Spec.Image, installed.Spec.Env[0].Value = "example.invalid/game:new", "new"
	if err := r.Update(t.Context(), installed); err != nil {
		t.Fatal(err)
	}
	effective, _, err := r.reconcileTemplateRevision(t.Context(), gs)
	if err != nil {
		t.Fatal(err)
	}
	if effective.Spec.Image != "example.invalid/game:old" || effective.Spec.Env[0].Value != "old" {
		t.Fatalf("render escaped snapshot: %#v", effective.Spec)
	}
	if gs.Status.TemplateRevision.Effective != old || gs.Status.TemplateRevision.Desired == old {
		t.Fatalf("untruthful revisions: %#v", gs.Status.TemplateRevision)
	}
	if c := meta.FindStatusCondition(gs.Status.Conditions, "TemplateUpdatePending"); c == nil || c.Status != metav1.ConditionTrue {
		t.Fatalf("missing pending condition: %v", c)
	}
	// Recreating the reconciler and clearing status must not discard the pin.
	gs.Status = gameplanev1alpha1.GameServerStatus{}
	if err := r.Status().Update(t.Context(), gs); err != nil {
		t.Fatal(err)
	}
	r = &GameServerReconciler{Client: r.Client, APIReader: r.Client, Scheme: r.Scheme}
	effective, _, err = r.reconcileTemplateRevision(t.Context(), gs)
	if err != nil || effective.Spec.Image != "example.invalid/game:old" {
		t.Fatalf("restart lost snapshot: %v, %v", effective, err)
	}
}

func TestTemplateSnapshotRejectsMissingCorruptAndForeign(t *testing.T) {
	for _, mode := range []string{"missing", "corrupt", "foreign"} {
		t.Run(mode, func(t *testing.T) {
			r, gs, _ := revisionFixture(t)
			var cm corev1.ConfigMap
			key := client.ObjectKey{Namespace: gs.Namespace, Name: templateSnapshotName(gs, gs.Annotations[effectiveTemplateAnnotation])}
			if err := r.Get(t.Context(), key, &cm); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "missing":
				if err := r.Delete(t.Context(), &cm); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				cm.Data[templateSnapshotKey] = `{}`
				if err := r.Update(t.Context(), &cm); err != nil {
					t.Fatal(err)
				}
			case "foreign":
				cm.OwnerReferences[0].UID = "other-server"
				if err := r.Update(t.Context(), &cm); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := effectiveGameTemplate(t.Context(), r.Client, gs); err == nil {
				t.Fatal("invalid snapshot fell through to live template")
			}
		})
	}
}

func TestTemplateRequestMustMatchInstalledDigest(t *testing.T) {
	r, gs, installed := revisionFixture(t)
	gs.Spec.TemplateRollout.RequestedRevision = gs.Annotations[effectiveTemplateAnnotation]
	if err := r.Update(t.Context(), gs); err != nil {
		t.Fatal(err)
	}
	installed.Spec.Image = "example.invalid/game:new"
	if err := r.Update(t.Context(), installed); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.reconcileTemplateRevision(t.Context(), gs); err != nil {
		t.Fatal(err)
	}
	if _, held, _ := lifecycleOwner(gs); held {
		t.Fatal("old request admitted a newer template")
	}
}

func TestTemplateAdoptionWaitsForLifecycleOwner(t *testing.T) {
	for _, kind := range []string{"Backup", "Restore", "Wipe"} {
		t.Run(kind, func(t *testing.T) {
			r, gs, installed := revisionFixture(t)
			gs.Spec.TemplateRollout.Policy = "Automatic"
			op := lifecycleIdentity{Kind: kind, Name: "owner", UID: "owner-uid", TargetUID: gs.UID, Token: "request"}
			gs.Annotations[lifecycleOperationAnnotation] = lifecycleOperationJSON(lifecycleOperation{lifecycleIdentity: op})
			if err := r.Update(t.Context(), gs); err != nil {
				t.Fatal(err)
			}
			installed.Spec.Image = "example.invalid/game:new"
			if err := r.Update(t.Context(), installed); err != nil {
				t.Fatal(err)
			}
			effective, _, err := r.reconcileTemplateRevision(t.Context(), gs)
			if err != nil || effective.Spec.Image != "example.invalid/game:old" {
				t.Fatalf("protected operation adopted template: %v", err)
			}
			owner, _, _ := lifecycleOwner(gs)
			if owner.Kind != kind {
				t.Fatal("stole lifecycle owner")
			}
		})
	}
}

func TestRunningTemplateRequiresOwnedRunningGame(t *testing.T) {
	r, gs, _ := revisionFixture(t)
	digest := gs.Annotations[effectiveTemplateAnnotation]
	ss := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: gs.Name, Namespace: gs.Namespace, UID: "workload-uid", OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(gs, gameplanev1alpha1.GroupVersion.WithKind("GameServer"))}}}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: gs.Name + "-0", Namespace: gs.Namespace, Annotations: map[string]string{templateRevisionAnnotation: digest}, OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(ss, appsv1.SchemeGroupVersion.WithKind("StatefulSet"))}}}
	if err := r.Create(t.Context(), ss); err != nil {
		t.Fatal(err)
	}
	if err := r.Create(t.Context(), pod); err != nil {
		t.Fatal(err)
	}
	if got, err := observedTemplateRevision(t.Context(), r.Client, gs); err != nil || got != "" {
		t.Fatalf("pending Pod reported running: %q %v", got, err)
	}
	pod.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: "game", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}}
	if err := r.Status().Update(t.Context(), pod); err != nil {
		t.Fatal(err)
	}
	if got, err := observedTemplateRevision(t.Context(), r.Client, gs); err != nil || got != digest {
		t.Fatalf("running Pod not observed: %q %v", got, err)
	}
	pod.OwnerReferences[0].UID = types.UID("replaced-workload")
	if err := r.Update(t.Context(), pod); err != nil {
		t.Fatal(err)
	}
	if got, err := observedTemplateRevision(context.Background(), r.Client, gs); err != nil || got != "" {
		t.Fatalf("foreign Pod reported running: %q %v", got, err)
	}
}

func TestTemplateAdoptionDrainsAndKeepsAdmittedRevisionAcrossRestart(t *testing.T) {
	r, gs, installed := revisionFixture(t)
	old := gs.Annotations[effectiveTemplateAnnotation]
	zero, one := int32(0), int32(1)
	ss := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: gs.Name, Namespace: gs.Namespace, UID: "workload", OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(gs, gameplanev1alpha1.GroupVersion.WithKind("GameServer"))}}, Spec: appsv1.StatefulSetSpec{Replicas: &one}, Status: appsv1.StatefulSetStatus{Replicas: 1}}
	if err := r.Create(t.Context(), ss); err != nil {
		t.Fatal(err)
	}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: gs.Name + "-0", Namespace: gs.Namespace, UID: "old-pod", OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(ss, appsv1.SchemeGroupVersion.WithKind("StatefulSet"))}}}
	if err := r.Create(t.Context(), pod); err != nil {
		t.Fatal(err)
	}
	installed.Spec.Image = "example.invalid/game:admitted"
	if err := r.Update(t.Context(), installed); err != nil {
		t.Fatal(err)
	}
	_, target, _ := templateSnapshot(installed)
	gs.Spec.TemplateRollout.RequestedRevision = target
	if err := r.Update(t.Context(), gs); err != nil {
		t.Fatal(err)
	}
	if _, changed, err := r.reconcileTemplateRevision(t.Context(), gs); err != nil || !changed {
		t.Fatalf("admission: %v %v", changed, err)
	}
	owner, held, _ := lifecycleOwner(gs)
	if !held || owner.Kind != "Rollout" || owner.Token != target {
		t.Fatalf("wrong owner: %#v", owner)
	}
	// A new leader and a newer catalog must retain the admitted target.
	r = &GameServerReconciler{Client: r.Client, APIReader: r.Client, Scheme: r.Scheme}
	installed.Spec.Image = "example.invalid/game:later"
	if err := r.Update(t.Context(), installed); err != nil {
		t.Fatal(err)
	}
	effective, changed, err := r.reconcileTemplateRevision(t.Context(), gs)
	if err != nil || changed || effective.Spec.Image != "example.invalid/game:old" {
		t.Fatalf("adopted before drain: %v %v", changed, err)
	}
	ss.Spec.Replicas = &zero
	ss.Annotations = map[string]string{lifecycleOperationAnnotation: gs.Annotations[lifecycleOperationAnnotation]}
	if err := r.Update(t.Context(), ss); err != nil {
		t.Fatal(err)
	}
	ss.Status.Replicas = 0
	ss.Status.ObservedGeneration = ss.Generation
	if err := r.Status().Update(t.Context(), ss); err != nil {
		t.Fatal(err)
	}
	if _, changed, err := r.reconcileTemplateRevision(t.Context(), gs); err != nil || changed || gs.Annotations[effectiveTemplateAnnotation] != old {
		t.Fatalf("live old Pod escaped drain: %v %v", changed, err)
	}
	if err := r.Delete(t.Context(), pod); err != nil {
		t.Fatal(err)
	}
	if _, changed, err := r.reconcileTemplateRevision(t.Context(), gs); err != nil || !changed || gs.Annotations[effectiveTemplateAnnotation] != target {
		t.Fatalf("pin not committed: %v %v", changed, err)
	}
	effective, err = effectiveGameTemplate(t.Context(), r.Client, gs)
	if err != nil || effective.Spec.Image != "example.invalid/game:admitted" {
		t.Fatalf("catalog replaced admitted content: %v", err)
	}
	// Suspended adoption releases after observing the new stopped template.
	gs.Spec.Suspend = true
	if err := r.Update(t.Context(), gs); err != nil {
		t.Fatal(err)
	}
	ss.Spec.Template.Annotations = map[string]string{templateRevisionAnnotation: target}
	if err := r.Update(t.Context(), ss); err != nil {
		t.Fatal(err)
	}
	ss.Status.ObservedGeneration = ss.Generation
	if err := r.Status().Update(t.Context(), ss); err != nil {
		t.Fatal(err)
	}
	if _, changed, err := r.reconcileTemplateRevision(t.Context(), gs); err != nil || !changed {
		t.Fatalf("release: %v %v", changed, err)
	}
	if _, held, _ := lifecycleOwner(gs); held || !gs.Spec.Suspend {
		t.Fatal("owner retained or user suspend lost")
	}
	if _, _, err := r.reconcileTemplateRevision(t.Context(), gs); err != nil {
		t.Fatal(err)
	}
	if gs.Status.TemplateRevision.Running != "" || gs.Status.TemplateRevision.Desired == target {
		t.Fatalf("untruthful suspended/latest status: %#v", gs.Status.TemplateRevision)
	}
	var snapshots corev1.ConfigMapList
	if err := r.List(t.Context(), &snapshots); err != nil {
		t.Fatal(err)
	}
	if len(snapshots.Items) != 1 {
		t.Fatalf("obsolete snapshots retained: %d", len(snapshots.Items))
	}
}

func TestPinnedTemplateDrivesConfigInitStorageAndImage(t *testing.T) {
	r, gs, old := revisionFixture(t)
	old.Spec.Storage = gameplanev1alpha1.GameStorageSpec{MountPath: "/old-data", Size: resource.MustParse("1Gi"), Extra: []gameplanev1alpha1.ExtraVolumeSpec{{Name: "old", MountPath: "/old-extra", Size: resource.MustParse("1Gi")}}}
	old.Spec.ConfigFiles = []gameplanev1alpha1.ConfigFile{{Path: "revision.txt", Template: "old contents"}}
	revision, err := r.saveTemplateSnapshot(t.Context(), gs, old)
	if err != nil {
		t.Fatal(err)
	}
	gs.Annotations[effectiveTemplateAnnotation] = revision
	if err := r.Update(t.Context(), gs); err != nil {
		t.Fatal(err)
	}
	latest := old.DeepCopy()
	latest.Spec.Image, latest.Spec.Storage.MountPath = "example.invalid/game:new", "/new-data"
	latest.Spec.Storage.Extra[0].Name, latest.Spec.Storage.Extra[0].MountPath = "new", "/new-extra"
	latest.Spec.ConfigFiles[0].Template = "new contents"
	if err := r.Update(t.Context(), latest); err != nil {
		t.Fatal(err)
	}
	effective, _, err := r.reconcileTemplateRevision(t.Context(), gs)
	if err != nil {
		t.Fatal(err)
	}
	mc, err := materializeConfig(gs, effective)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.reconcilePVC(t.Context(), gs, effective); err != nil {
		t.Fatal(err)
	}
	if err := r.reconcileExtraPVCs(t.Context(), gs, effective); err != nil {
		t.Fatal(err)
	}
	if err := r.reconcileFilesSecret(t.Context(), gs, mc); err != nil {
		t.Fatal(err)
	}
	if err := r.reconcileStatefulSet(t.Context(), gs, effective, nil, mc, 1); err != nil {
		t.Fatal(err)
	}
	var ss appsv1.StatefulSet
	if err := r.Get(t.Context(), client.ObjectKeyFromObject(gs), &ss); err != nil {
		t.Fatal(err)
	}
	if ss.Spec.Template.Spec.Containers[0].Image != old.Spec.Image {
		t.Fatal("image used latest")
	}
	for _, container := range append(ss.Spec.Template.Spec.Containers, ss.Spec.Template.Spec.InitContainers...) {
		for _, mount := range container.VolumeMounts {
			if mount.MountPath == "/new-data" || mount.MountPath == "/new-extra" {
				t.Fatalf("mount escaped snapshot: %#v", mount)
			}
		}
	}
	if len(ss.Spec.Template.Spec.InitContainers) == 0 {
		t.Fatal("pinned config-init absent")
	}
	var files corev1.Secret
	if err := r.Get(t.Context(), client.ObjectKey{Namespace: gs.Namespace, Name: gs.Name + "-files"}, &files); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, value := range files.Data {
		if string(value) == "old contents" {
			found = true
		}
	}
	if !found {
		t.Fatalf("files used latest: %#v", files.Data)
	}
	var pvcs corev1.PersistentVolumeClaimList
	if err := r.List(t.Context(), &pvcs); err != nil {
		t.Fatal(err)
	}
	if len(pvcs.Items) != 2 {
		t.Fatalf("wrong effective PVC set: %d", len(pvcs.Items))
	}
	for _, pvc := range pvcs.Items {
		if pvc.Name == gs.Name+"-extra-new" {
			t.Fatal("pending revision provisioned new extra storage")
		}
	}
}

func TestSnapshotRestorePinsEffectiveSourceBeforeStartup(t *testing.T) {
	r, source, installed := revisionFixture(t)
	rs := &gameplanev1alpha1.Restore{ObjectMeta: metav1.ObjectMeta{Name: "restore", Namespace: source.Namespace, UID: "restore-uid"}}
	if err := r.Create(t.Context(), rs); err != nil {
		t.Fatal(err)
	}
	target := source.DeepCopy()
	target.Name, target.UID, target.ResourceVersion = "restored", "target-uid", ""
	target.Annotations = map[string]string{}
	restorer := &RestoreReconciler{Client: r.Client, APIReader: r.Client, Scheme: r.Scheme}
	if err := restorer.planRestoredTemplate(t.Context(), rs, source, target); err != nil {
		t.Fatal(err)
	}
	if err := r.Create(t.Context(), target); err != nil {
		t.Fatal(err)
	}
	installed.Spec.Image = "example.invalid/game:new"
	if err := r.Update(t.Context(), installed); err != nil {
		t.Fatal(err)
	}
	if _, wait, err := r.reconcileTemplateRevision(t.Context(), target); err != nil || !wait {
		t.Fatalf("target bypassed snapshot copy: wait=%v err=%v", wait, err)
	}
	if err := restorer.copyRestoredTemplate(t.Context(), rs, target); err != nil {
		t.Fatal(err)
	}
	effective, err := effectiveGameTemplate(t.Context(), r.Client, target)
	if err != nil || effective.Spec.Image != "example.invalid/game:old" {
		t.Fatalf("restore adopted latest content: %v", err)
	}
}

type revisionStopper struct{ calls int }

func (s *revisionStopper) Stop(context.Context, string, string) error { s.calls++; return nil }
func (*revisionStopper) Enabled() bool                                { return true }

func TestTemplateRolloutUsesOldGracefulStopAndBlocksBackup(t *testing.T) {
	r, gs, old := revisionFixture(t)
	old.Spec.RCON = &gameplanev1alpha1.RCONSpec{Protocol: "source"}
	old.Spec.Capabilities = &gameplanev1alpha1.CapabilitiesSpec{Lifecycle: &gameplanev1alpha1.LifecycleSpec{Stop: []string{"save", "stop"}}}
	pin, err := r.saveTemplateSnapshot(t.Context(), gs, old)
	if err != nil {
		t.Fatal(err)
	}
	gs.Annotations[effectiveTemplateAnnotation] = pin
	gs.Spec.TemplateRollout.Policy = "Automatic"
	if err := r.Update(t.Context(), gs); err != nil {
		t.Fatal(err)
	}
	newTemplate := old.DeepCopy()
	newTemplate.Spec.Image = "example.invalid/game:new"
	newTemplate.Spec.Capabilities = nil // latest no longer declares graceful stop
	if err := r.Update(t.Context(), newTemplate); err != nil {
		t.Fatal(err)
	}
	if _, changed, err := r.reconcileTemplateRevision(t.Context(), gs); err != nil || !changed {
		t.Fatalf("automatic admission: %v %v", changed, err)
	}
	if claimed, err := claimLifecycleOperation(t.Context(), r.Client, r.Client, gs, lifecycleIdentity{Kind: "Backup", Name: "copy", UID: "copy-uid", TargetUID: gs.UID}, nil); err != nil || claimed {
		t.Fatalf("backup entered rollout: %v %v", claimed, err)
	}
	one := int32(1)
	ss := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: gs.Name, Namespace: gs.Namespace, UID: "workload", OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(gs, gameplanev1alpha1.GroupVersion.WithKind("GameServer"))}}, Spec: appsv1.StatefulSetSpec{Replicas: &one}, Status: appsv1.StatefulSetStatus{ReadyReplicas: 1, Replicas: 1}}
	if err := r.Create(t.Context(), ss); err != nil {
		t.Fatal(err)
	}
	stopper := &revisionStopper{}
	r.AgentClient = stopper
	effective, _, err := r.reconcileTemplateRevision(t.Context(), gs)
	if err != nil {
		t.Fatal(err)
	}
	replicas, poll, err := r.desiredReplicas(t.Context(), gs, effective, idleAwake)
	if err != nil || replicas != 1 || poll <= 0 || stopper.calls != 1 {
		t.Fatalf("old graceful contract bypassed: replicas=%d poll=%v calls=%d err=%v", replicas, poll, stopper.calls, err)
	}
}

func TestPinnedTemplateSurvivesInstalledTemplateDeletion(t *testing.T) {
	r, gs, installed := revisionFixture(t)
	if err := r.Delete(t.Context(), installed); err != nil {
		t.Fatal(err)
	}
	effective, _, err := r.reconcileTemplateRevision(t.Context(), gs)
	if err != nil || effective.Spec.Image != "example.invalid/game:old" {
		t.Fatalf("installed deletion lost pinned content: %v", err)
	}
	if gs.Status.TemplateRevision.Desired != "" {
		t.Fatal("missing installed revision reported as available")
	}
	condition := meta.FindStatusCondition(gs.Status.Conditions, "TemplateUpdatePending")
	if condition == nil || condition.Status != metav1.ConditionUnknown {
		t.Fatalf("missing unavailable condition: %#v", condition)
	}
}

func TestStaleTemplateRenderCannotUndoCommittedPin(t *testing.T) {
	r, gs, old := revisionFixture(t)
	stale := gs.DeepCopy()
	newTemplate := old.DeepCopy()
	newTemplate.Spec.Image = "example.invalid/game:new"
	pin, err := r.saveTemplateSnapshot(t.Context(), gs, newTemplate)
	if err != nil {
		t.Fatal(err)
	}
	gs.Annotations[effectiveTemplateAnnotation] = pin
	if err := r.Update(t.Context(), gs); err != nil {
		t.Fatal(err)
	}
	if err := r.reconcileStatefulSet(t.Context(), stale, old, nil, &materializedConfig{}, 1); err == nil {
		t.Fatal("stale rendering created a workload after pin change")
	}
}

func TestTemplateBaselinePreservesLegacyWorkloadUntilAdmittedUpdate(t *testing.T) {
	for _, policy := range []string{"Automatic", "OnRequest"} {
		t.Run(policy, func(t *testing.T) {
			r, gs, installed := revisionFixture(t)
			delete(gs.Annotations, effectiveTemplateAnnotation)
			gs.Spec.TemplateRollout.Policy = policy
			if err := r.Update(t.Context(), gs); err != nil {
				t.Fatal(err)
			}
			installed.Spec.RCON = &gameplanev1alpha1.RCONSpec{Protocol: "source"}
			installed.Spec.Capabilities = &gameplanev1alpha1.CapabilitiesSpec{Lifecycle: &gameplanev1alpha1.LifecycleSpec{Stop: []string{"save", "stop"}}}
			if err := r.Update(t.Context(), installed); err != nil {
				t.Fatal(err)
			}
			one := int32(1)
			ss := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: gs.Name, Namespace: gs.Namespace, UID: "legacy-workload", OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(gs, gameplanev1alpha1.GroupVersion.WithKind("GameServer"))}}, Spec: appsv1.StatefulSetSpec{Replicas: &one, Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{"admission.example/default": "keep"}}, Spec: corev1.PodSpec{DNSPolicy: corev1.DNSClusterFirst, RestartPolicy: corev1.RestartPolicyAlways, SchedulerName: "default-scheduler", Containers: []corev1.Container{{Name: "game", Image: installed.Spec.Image, ImagePullPolicy: corev1.PullIfNotPresent, TerminationMessagePath: "/dev/termination-log", TerminationMessagePolicy: corev1.TerminationMessageReadFile}}}}}, Status: appsv1.StatefulSetStatus{Replicas: 1, ReadyReplicas: 1}}
			pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: gs.Name + "-0", Namespace: gs.Namespace, OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(ss, appsv1.SchemeGroupVersion.WithKind("StatefulSet"))}}, Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{Name: "game", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}}}}
			for _, obj := range []client.Object{ss, pod} {
				if err := r.Create(t.Context(), obj); err != nil {
					t.Fatal(err)
				}
			}
			// Credential isolation is already applied; provenance alone must
			// preserve the API-defaulted template without restarting it.
			applyWorkloadSecurity(&ss.Spec.Template.Spec, installed, false, 3600)
			if err := r.Update(t.Context(), ss); err != nil {
				t.Fatal(err)
			}
			before, err := json.Marshal(ss.Spec.Template)
			if err != nil {
				t.Fatal(err)
			}
			if _, changed, err := r.reconcileTemplateRevision(t.Context(), gs); err != nil || !changed {
				t.Fatalf("baseline admission: changed=%v err=%v", changed, err)
			}
			owner, held, err := lifecycleOwner(gs)
			if err != nil || !held || !owner.Baseline {
				t.Fatalf("missing durable baseline intent: %#v %v", owner, err)
			}
			// A restarted reconciler must preserve replicas while baseline ownership is held.
			stopper := &revisionStopper{}
			r = &GameServerReconciler{Client: r.Client, APIReader: r.APIReader, Scheme: r.Scheme, AgentClient: stopper}
			if replicas, _, err := r.desiredReplicas(t.Context(), gs, installed, idleAwake); err != nil || replicas != 1 || stopper.calls != 0 {
				t.Fatalf("baseline stopped legacy workload: replicas=%d calls=%d err=%v", replicas, stopper.calls, err)
			}
			if _, changed, err := r.reconcileTemplateRevision(t.Context(), gs); err != nil || !changed {
				t.Fatalf("baseline pin: changed=%v err=%v", changed, err)
			}
			pin := gs.Annotations[effectiveTemplateAnnotation]
			if !validTemplateRevision(pin) {
				t.Fatal("baseline did not save a pin")
			}
			if _, held, _ := lifecycleOwner(gs); held {
				t.Fatal("baseline retained lifecycle owner")
			}
			effective, _, err := r.reconcileTemplateRevision(t.Context(), gs)
			if err != nil {
				t.Fatal(err)
			}
			if gs.Status.TemplateRevision.Running != "" {
				t.Fatal("legacy Pod assigned an invented running revision")
			}
			mc, err := materializeConfig(gs, effective)
			if err != nil {
				t.Fatal(err)
			}
			if err := r.reconcileStatefulSet(t.Context(), gs, effective, nil, mc, 1); err != nil {
				t.Fatal(err)
			}
			if err := r.Get(t.Context(), client.ObjectKeyFromObject(ss), ss); err != nil {
				t.Fatal(err)
			}
			after, err := json.Marshal(ss.Spec.Template)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(before) || stopper.calls != 0 || *ss.Spec.Replicas != 1 {
				t.Fatal("baseline changed legacy Pod template or power state")
			}

			installed.Spec.Image = "example.invalid/game:new"
			installed.Spec.Capabilities = nil
			if err := r.Update(t.Context(), installed); err != nil {
				t.Fatal(err)
			}
			_, desired, err := templateSnapshot(installed)
			if err != nil {
				t.Fatal(err)
			}
			if policy == "OnRequest" {
				gs.Spec.TemplateRollout.RequestedRevision = desired
				if err := r.Update(t.Context(), gs); err != nil {
					t.Fatal(err)
				}
			}
			if _, changed, err := r.reconcileTemplateRevision(t.Context(), gs); err != nil || !changed {
				t.Fatalf("next revision admission: %v %v", changed, err)
			}
			effective, _, err = r.reconcileTemplateRevision(t.Context(), gs)
			if err != nil {
				t.Fatal(err)
			}
			if replicas, _, err := r.desiredReplicas(t.Context(), gs, effective, idleAwake); err != nil || replicas != 1 || stopper.calls != 1 {
				t.Fatalf("next revision bypassed old graceful stop: replicas=%d calls=%d err=%v", replicas, stopper.calls, err)
			}
			if err := r.reconcileStatefulSet(t.Context(), gs, effective, nil, mc, 0); err != nil {
				t.Fatal(err)
			}
			if _, changed, err := r.reconcileTemplateRevision(t.Context(), gs); err != nil || changed {
				t.Fatalf("adopted before owned drain: changed=%v err=%v", changed, err)
			}
			if gs.Annotations[effectiveTemplateAnnotation] != pin {
				t.Fatal("pin advanced while legacy Pod remains")
			}
			if err := r.Get(t.Context(), client.ObjectKeyFromObject(ss), ss); err != nil {
				t.Fatal(err)
			}
			if ss.Annotations[lifecycleOperationAnnotation] != gs.Annotations[lifecycleOperationAnnotation] {
				t.Fatal("drain not owned by admitted rollout")
			}
			ss.Status.Replicas, ss.Status.ReadyReplicas, ss.Status.ObservedGeneration = 0, 0, ss.Generation
			if err := r.Status().Update(t.Context(), ss); err != nil {
				t.Fatal(err)
			}
			if err := r.Delete(t.Context(), pod); err != nil {
				t.Fatal(err)
			}
			if _, changed, err := r.reconcileTemplateRevision(t.Context(), gs); err != nil || !changed {
				t.Fatalf("drained adoption: %v %v", changed, err)
			}
			if gs.Annotations[effectiveTemplateAnnotation] != desired {
				t.Fatal("drained rollout did not adopt requested pin")
			}
		})
	}
}

func TestTemplateBaselineAllowsRealWorkloadChanges(t *testing.T) {
	for _, change := range []string{"inputs", "stopped", "new-workload", "credential-isolation", "restricted-profile"} {
		t.Run(change, func(t *testing.T) {
			r, gs, _ := revisionFixture(t)
			pin := gs.Annotations[effectiveTemplateAnnotation]
			inputs, err := r.baselineRenderInputs(gs, pin)
			if err != nil {
				t.Fatal(err)
			}
			gs.Annotations[baselineTemplateAnnotation], gs.Annotations[baselineWorkloadAnnotation], gs.Annotations[baselineInputsAnnotation] = pin, "legacy", inputs
			one := int32(1)
			ss := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{UID: "legacy"}, Spec: appsv1.StatefulSetSpec{Replicas: &one}}
			applyWorkloadSecurity(&ss.Spec.Template.Spec, &gameplanev1alpha1.GameTemplate{}, false, 3600)
			if keep, err := r.preserveBaselinePodTemplate(t.Context(), gs, ss); err != nil || !keep {
				t.Fatalf("baseline not preserved: %v %v", keep, err)
			}
			switch change {
			case "inputs":
				gs.Spec.Image = "example.invalid/game:override"
			case "stopped":
				zero := int32(0)
				ss.Spec.Replicas = &zero
			case "new-workload":
				ss.UID = "replacement"
			case "credential-isolation":
				ss.Spec.Template.Spec.AutomountServiceAccountToken = nil
			case "restricted-profile":
				r.RestrictedWorkloads = true
			}
			if keep, err := r.preserveBaselinePodTemplate(t.Context(), gs, ss); err != nil || keep {
				t.Fatalf("real workload change frozen: %v %v", keep, err)
			}
		})
	}
}

func TestTemplateBaselineWithoutWorkloadPinsBeforeFirstRender(t *testing.T) {
	r, gs, _ := revisionFixture(t)
	delete(gs.Annotations, effectiveTemplateAnnotation)
	if err := r.Update(t.Context(), gs); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, changed, err := r.reconcileTemplateRevision(t.Context(), gs); err != nil || !changed {
			t.Fatalf("initial pin: %v %v", changed, err)
		}
	}
	effective, _, err := r.reconcileTemplateRevision(t.Context(), gs)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.reconcileStatefulSet(t.Context(), gs, effective, nil, &materializedConfig{}, 1); err != nil {
		t.Fatal(err)
	}
	var ss appsv1.StatefulSet
	if err := r.Get(t.Context(), client.ObjectKeyFromObject(gs), &ss); err != nil {
		t.Fatal(err)
	}
	if ss.Spec.Template.Annotations[templateRevisionAnnotation] != gs.Annotations[effectiveTemplateAnnotation] || *ss.Spec.Replicas != 1 {
		t.Fatal("first workload not stamped and started")
	}
}

func TestTemplateBaselineDoesNotSwallowConcurrentSpecEdit(t *testing.T) {
	r, gs, installed := revisionFixture(t)
	delete(gs.Annotations, effectiveTemplateAnnotation)
	gs.Generation = 1
	if err := r.Update(t.Context(), gs); err != nil {
		t.Fatal(err)
	}
	one := int32(1)
	ss := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: gs.Name, Namespace: gs.Namespace, UID: "legacy", OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(gs, gameplanev1alpha1.GroupVersion.WithKind("GameServer"))}}, Spec: appsv1.StatefulSetSpec{Replicas: &one, Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "game", Image: installed.Spec.Image}}}}}}
	if err := r.Create(t.Context(), ss); err != nil {
		t.Fatal(err)
	}
	if _, changed, err := r.reconcileTemplateRevision(t.Context(), gs); err != nil || !changed {
		t.Fatalf("baseline admission: %v %v", changed, err)
	}
	gs.Generation++ // Fake clients do not increment spec generations.
	gs.Spec.Image = "example.invalid/game:requested"
	if err := r.Update(t.Context(), gs); err != nil {
		t.Fatal(err)
	}
	if _, changed, err := r.reconcileTemplateRevision(t.Context(), gs); err != nil || !changed {
		t.Fatalf("baseline initialization: %v %v", changed, err)
	}
	if gs.Annotations[baselineInputsAnnotation] != "" {
		t.Fatal("concurrent edit was recorded as already applied")
	}
	effective, _, err := r.reconcileTemplateRevision(t.Context(), gs)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.reconcileStatefulSet(t.Context(), gs, effective, nil, &materializedConfig{}, 1); err != nil {
		t.Fatal(err)
	}
	if err := r.Get(t.Context(), client.ObjectKeyFromObject(ss), ss); err != nil {
		t.Fatal(err)
	}
	if ss.Spec.Template.Spec.Containers[0].Image != gs.Spec.Image {
		t.Fatal("baseline swallowed the requested image change")
	}
}

func TestTemplateRevisionRejectsRestrictedIncompatibleTargetBeforeAdmission(t *testing.T) {
	r, gs, installed := revisionFixture(t)
	r.RestrictedWorkloads = true
	old := gs.Annotations[effectiveTemplateAnnotation]
	installed.Spec.Image = "example.invalid/game:incompatible"
	if err := r.Update(t.Context(), installed); err != nil {
		t.Fatal(err)
	}
	gs.Spec.TemplateRollout.Policy = "Automatic"
	if err := r.Update(t.Context(), gs); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.reconcileTemplateRevision(t.Context(), gs); err == nil {
		t.Fatal("admitted a template without Restricted-compatible identity")
	}
	if _, held, _ := lifecycleOwner(gs); held || gs.Annotations[effectiveTemplateAnnotation] != old {
		t.Fatal("invalid security profile changed the pin or admitted a drain")
	}
}
