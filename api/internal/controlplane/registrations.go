package controlplane

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
)

func (b *backend) registerCluster(ctx context.Context, ns string, secret *corev1.Secret, registration *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	if secret == nil || registration == nil {
		return nil, apierrors.NewBadRequest("registration and credential are required")
	}
	name := registration.GetName()
	ref, _, _ := unstructured.NestedString(registration.Object, "spec", "kubeconfigSecret", "name")
	if secret.Name != "cluster-"+name+"-kubeconfig" || ref != secret.Name || secret.Labels[kube.ClusterKubeconfigLabel] != "true" || secret.Labels["gameplane.local/managed-by"] != "gameplane-api" {
		return nil, apierrors.NewBadRequest("registration requires its API-owned kubeconfig credential")
	}
	credential, err := secretObject(secret)
	if err != nil {
		return nil, err
	}
	credential, err = prepareRegistrationObject(credential, ns)
	if err != nil {
		return nil, err
	}
	registration, err = prepareRegistrationObject(registration, "")
	if err != nil {
		return nil, err
	}
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin cluster registration: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := b.lockKeyState(ctx, tx); err != nil {
		return nil, err
	}
	// The absence check and possible legacy-orphan adoption occur in the
	// same transaction as both writes. No failed registration strands a key.
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM management_objects WHERE kind = 'clusters' AND namespace = '' AND name = ?`, name).Scan(&count); err != nil {
		return nil, fmt.Errorf("inspect cluster registration: %w", err)
	}
	if count != 0 {
		return nil, apierrors.NewAlreadyExists(kube.GVRCluster.GroupResource(), name)
	}
	credentials := &objects{backend: b, kind: "secrets", ns: ns}
	var uid, payload string
	var version int64
	err = tx.QueryRowContext(ctx, `SELECT uid, version, payload FROM management_objects WHERE kind = 'secrets' AND namespace = ? AND name = ?`, ns, secret.Name).Scan(&uid, &version, &payload)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if err := insertRegistrationObject(ctx, tx, credentials, credential); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, fmt.Errorf("inspect registration credential: %w", err)
	default:
		existing, err := asSecret(credentials.decode(secret.Name, uid, version, payload))
		if err != nil {
			return nil, err
		}
		// Recover a legacy interrupted POST only when its fixed name,
		// ownership labels and exact credential match this retry. Adopt it
		// without changing its identity or contents; external secrets survive.
		incoming, err := asSecret(credential, nil)
		if err != nil {
			return nil, err
		}
		if existing.Labels[kube.ClusterKubeconfigLabel] != "true" || existing.Labels["gameplane.local/managed-by"] != "gameplane-api" || len(existing.Data) != 1 || len(incoming.Data) != 1 || !bytes.Equal(existing.Data["kubeconfig"], incoming.Data["kubeconfig"]) {
			return nil, apierrors.NewAlreadyExists(corev1.Resource("secrets"), secret.Name)
		}
	}
	if err := insertRegistrationObject(ctx, tx, &objects{backend: b, kind: "clusters"}, registration); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit cluster registration: %w", err)
	}
	return registration, nil
}

func prepareRegistrationObject(obj *unstructured.Unstructured, ns string) (*unstructured.Unstructured, error) {
	obj = obj.DeepCopy()
	if len(validation.IsDNS1123Subdomain(obj.GetName())) != 0 || (obj.GetNamespace() != "" && obj.GetNamespace() != ns) || obj.GetResourceVersion() != "" {
		return nil, apierrors.NewBadRequest("invalid registration object metadata")
	}
	identity := make([]byte, 16)
	if _, err := rand.Read(identity); err != nil {
		return nil, fmt.Errorf("generate registration identity: %w", err)
	}
	obj.SetNamespace(ns)
	obj.SetUID(types.UID(hex.EncodeToString(identity)))
	obj.SetCreationTimestamp(metav1.Now())
	obj.SetResourceVersion("1")
	return obj, nil
}

func insertRegistrationObject(ctx context.Context, tx *sql.Tx, store *objects, obj *unstructured.Unstructured) error {
	payload, err := store.encode(obj, 1)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO management_objects (kind, namespace, name, uid, version, payload) VALUES (?, ?, ?, ?, 1, ?) ON CONFLICT (kind, namespace, name) DO NOTHING`, store.kind, store.ns, obj.GetName(), string(obj.GetUID()), payload)
	if err != nil {
		return fmt.Errorf("persist cluster registration: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect cluster registration write: %w", err)
	}
	if count == 0 {
		return apierrors.NewAlreadyExists(store.resource(), obj.GetName())
	}
	return nil
}
