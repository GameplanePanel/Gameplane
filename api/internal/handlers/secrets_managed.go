// API-managed credential Secrets. Admin Settings lets an admin type a
// sink's (and later a provider's) secret material directly into the
// dashboard; the API persists it as a labelled Secret in the
// control-plane namespace so the consuming subsystem (notify, auth) can
// keep its existing "read only labelled Secrets" contract.
//
// Two labels bound what this surface may touch:
//   - the feature label (e.g. gameplane.local/notification-sink=true)
//     gates reads and updates, exactly like destinations.go;
//   - ManagedByLabel gates deletes, so Secrets an admin created with
//     kubectl (or a GitOps pipeline) are never deleted through HTTP even
//     though they carry the feature label.

package handlers

import (
	"context"
	"errors"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
)

// ManagedByLabel marks Secrets the API created on an admin's behalf.
const ManagedByLabel = "gameplane.local/managed-by"

const managedByValue = "gameplane-api"

var errNotManagedSecret = errors.New("a secret with that name exists but is not managed by Gameplane")

// upsertLabelledSecret creates or updates ns/name with featureLabel=true
// + managed-by and the supplied keys. Callers must pass the complete key
// set for their schema (empty string for unset optionals) — the update is
// a merge patch, so an omitted key would otherwise keep its old value.
// Updating a Secret that lacks featureLabel is refused, so this path
// can't overwrite arbitrary control-plane Secrets.
func upsertLabelledSecret(ctx context.Context, k *kube.Client, ns, name, featureLabel string, data map[string]string) error {
	labels := map[string]string{featureLabel: "true", ManagedByLabel: managedByValue}
	desired := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Labels:    labels,
		},
		Type:       corev1.SecretTypeOpaque,
		StringData: data,
	}
	_, err := k.Secrets(ns).Create(ctx, desired, metav1.CreateOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsAlreadyExists(err) {
		return err
	}
	existing, err := k.Secrets(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if existing.Labels[featureLabel] != "true" {
		return errNotManagedSecret
	}
	// Keep unrelated keys/labels and bind the update to the version just read.
	if existing.Labels == nil {
		existing.Labels = map[string]string{}
	}
	for key, value := range labels {
		existing.Labels[key] = value
	}
	existing.StringData = data
	_, err = k.Secrets(ns).Update(ctx, existing, metav1.UpdateOptions{})
	return err
}

// deleteManagedSecret deletes ns/name only when it carries BOTH the
// feature label and managed-by=gameplane-api. Anything else reads as
// not-found so the endpoint doesn't leak which Secrets exist.
func deleteManagedSecret(ctx context.Context, k *kube.Client, ns, name, featureLabel string) error {
	existing, err := k.Secrets(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if existing.Labels[featureLabel] != "true" || existing.Labels[ManagedByLabel] != managedByValue {
		return apierrors.NewNotFound(corev1.Resource("secrets"), name)
	}
	return k.Secrets(ns).Delete(ctx, name, metav1.DeleteOptions{Preconditions: objectDeletePreconditions(existing)})
}
