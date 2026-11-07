package controller

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

const restoreTemplateAnnotation = "restore.gameplane.local/template-snapshot"

func (r *RestoreReconciler) planRestoredTemplate(ctx context.Context, rs *gameplanev1alpha1.Restore, source, target *gameplanev1alpha1.GameServer) error {
	// Pre-upgrade sources retain historical restore behavior. New snapshots
	// cannot reconstruct the template used when an older backup was taken.
	if source.Annotations[effectiveTemplateAnnotation] == "" {
		return nil
	}
	identity := sha256.Sum256([]byte(string(rs.UID)))
	name := fmt.Sprintf("restore-template-%x", identity[:16])
	var existing corev1.ConfigMap
	err := r.apiReader().Get(ctx, client.ObjectKey{Namespace: rs.Namespace, Name: name}, &existing)
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	if err == nil {
		if !metav1.IsControlledBy(&existing, rs) || existing.Immutable == nil || !*existing.Immutable {
			return fmt.Errorf("restore template plan is foreign or mutable")
		}
	} else {
		tmpl, err := effectiveGameTemplate(ctx, r.apiReader(), source)
		if err != nil {
			return err
		}
		data, revision, err := templateSnapshot(tmpl)
		if err != nil {
			return err
		}
		immutable := true
		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: rs.Namespace, Annotations: map[string]string{templateRevisionAnnotation: revision}}, Immutable: &immutable, Data: map[string]string{templateSnapshotKey: string(data)}}
		if err := controllerutil.SetControllerReference(rs, cm, r.Scheme); err != nil {
			return err
		}
		if err := r.Create(ctx, cm); err != nil {
			return err
		}
	}
	target.Annotations[restoreTemplateAnnotation] = name
	return nil
}

func (r *RestoreReconciler) copyRestoredTemplate(ctx context.Context, rs *gameplanev1alpha1.Restore, target *gameplanev1alpha1.GameServer) error {
	var cm corev1.ConfigMap
	if err := r.apiReader().Get(ctx, client.ObjectKey{Namespace: target.Namespace, Name: target.Annotations[restoreTemplateAnnotation]}, &cm); err != nil {
		return err
	}
	if !metav1.IsControlledBy(&cm, rs) || cm.Immutable == nil || !*cm.Immutable || !cm.DeletionTimestamp.IsZero() {
		return fmt.Errorf("restore template plan is foreign, mutable or deleting")
	}
	var tmpl gameplanev1alpha1.GameTemplate
	if err := json.Unmarshal([]byte(cm.Data[templateSnapshotKey]), &tmpl); err != nil {
		return err
	}
	_, revision, err := templateSnapshot(&tmpl)
	if err != nil {
		return err
	}
	if tmpl.Name != target.Spec.TemplateRef.Name || revision != cm.Annotations[templateRevisionAnnotation] {
		return fmt.Errorf("restore template plan content changed")
	}
	writer := &GameServerReconciler{Client: r.Client, APIReader: r.apiReader(), Scheme: r.Scheme}
	if _, err := writer.saveTemplateSnapshot(ctx, target, &tmpl); err != nil {
		return err
	}
	target.Annotations[effectiveTemplateAnnotation] = revision
	return r.Update(ctx, target)
}
