package kube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	unstructuredpkg "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
)

// As in backupexport.go, these types mirror the operator's Restore CRD
// field-for-field (same JSON tags) for the upload path only (spec 041). They
// mirror operator/api/v1alpha1/restore_types.go.

// RestorePhaseReceiving mirrors operator/api/v1alpha1.RestorePhaseReceiving:
// an upload restore is receiving and checking its archive.
const RestorePhaseReceiving = "Receiving"

// RestorePhaseAwaitingConfirmation mirrors
// operator/api/v1alpha1.RestorePhaseAwaitingConfirmation: the upload passed
// its checks and waits for the user to confirm.
const RestorePhaseAwaitingConfirmation = "AwaitingConfirmation"

// restoreNamePrefixMax bounds the server name copied into a Restore's
// generateName so the generated name stays well under the DNS-1123 limit.
const restoreNamePrefixMax = 40

// restoreUploadFileNameMax mirrors the operator's RestoreUpload.fileName limit.
const restoreUploadFileNameMax = 255

// GVRRestore addresses the Restore CRD (spec 041 upload path).
var GVRRestore = GVRs["restores"]

// RestoreUploadSpec mirrors operator/api/v1alpha1.RestoreUpload.
type RestoreUploadSpec struct {
	FileName          string `json:"fileName"`
	Format            string `json:"format"`
	DeclaredBytes     int64  `json:"declaredBytes"`
	ConfirmedTemplate string `json:"confirmedTemplate,omitempty"`
	Confirmed         bool   `json:"confirmed,omitempty"`
}

// RestoreSpec mirrors the subset of operator/api/v1alpha1.RestoreSpec the
// upload path reads and writes.
type RestoreSpec struct {
	// BackupRef is set for backup restores; upload restores leave it nil.
	BackupRef *corev1.LocalObjectReference `json:"backupRef,omitempty"`
	// ServerRef is the target GameServer.
	ServerRef corev1.LocalObjectReference `json:"serverRef"`
	// Upload is set for upload restores.
	Upload *RestoreUploadSpec `json:"upload,omitempty"`
}

// RestoreUploadStatus mirrors the subset of operator/api/v1alpha1.RestoreUploadStatus
// the gateway reads.
type RestoreUploadStatus struct {
	// Kind is "gameplane" or "plain" once the archive has been checked.
	Kind string `json:"kind,omitempty"`
	// StagingClaimName is the temporary staging PVC.
	StagingClaimName string `json:"stagingClaimName,omitempty"`
	// ReceiverService is the ClusterIP Service the upload is streamed to.
	ReceiverService string `json:"receiverService,omitempty"`
	// ReceivedAt is when the whole file reached the staging volume.
	ReceivedAt *metav1.Time `json:"receivedAt,omitempty"`
}

// RestoreStatus mirrors the subset of operator/api/v1alpha1.RestoreStatus the
// upload path reads.
type RestoreStatus struct {
	// Phase is the lifecycle phase of the Restore.
	Phase string `json:"phase,omitempty"`
	// Upload is set for upload restores.
	Upload *RestoreUploadStatus `json:"upload,omitempty"`
}

// Restore mirrors operator/api/v1alpha1.Restore (only the upload-relevant fields).
type Restore struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RestoreSpec   `json:"spec,omitempty"`
	Status RestoreStatus `json:"status,omitempty"`
}

// GetRestore fetches a Restore by namespace and name. Returns nil, nil when it
// does not exist.
func GetRestore(ctx context.Context, dyn dynamic.Interface, ns, name string) (*Restore, error) {
	u, err := dyn.Resource(GVRRestore).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("get restore %s: %w", name, err)
	}
	r := &Restore{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, r); err != nil {
		return nil, fmt.Errorf("get restore %s: convert from unstructured: %w", name, err)
	}
	return r, nil
}

// CreateUploadRestore creates an upload Restore targeting server. The apiserver
// assigns the name from generateName. template is the GameTemplate the user
// named for a plain archive; pass "" for a Gameplane archive. It returns the
// name and UID the apiserver persisted.
func CreateUploadRestore(ctx context.Context, dyn dynamic.Interface, ns, server, fileName, format string, declaredBytes int64, template string) (string, string, error) {
	if len(fileName) == 0 || len(fileName) > restoreUploadFileNameMax {
		return "", "", errors.New("create upload restore: invalid file name")
	}
	if format != "tar.gz" && format != "zip" {
		return "", "", errors.New("create upload restore: unsupported archive format")
	}
	if declaredBytes < 1 {
		return "", "", errors.New("create upload restore: declared size must be positive")
	}
	prefix := server
	if len(prefix) > restoreNamePrefixMax {
		prefix = prefix[:restoreNamePrefixMax]
	}
	obj := &Restore{
		// TypeMeta must be explicit for unstructured creates; see the
		// comment on CreateNetworkCapture.
		TypeMeta: metav1.TypeMeta{
			APIVersion: "gameplane.local/v1alpha1",
			Kind:       "Restore",
		},
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: prefix + "-restore-",
			Namespace:    ns,
		},
		Spec: RestoreSpec{
			ServerRef: corev1.LocalObjectReference{Name: server},
			Upload: &RestoreUploadSpec{
				FileName:          fileName,
				Format:            format,
				DeclaredBytes:     declaredBytes,
				ConfirmedTemplate: template,
			},
		},
	}
	raw, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return "", "", fmt.Errorf("create upload restore for %s: convert to unstructured: %w", server, err)
	}
	created, err := dyn.Resource(GVRRestore).Namespace(ns).
		Create(ctx, &unstructuredpkg.Unstructured{Object: raw}, metav1.CreateOptions{})
	if err != nil {
		return "", "", fmt.Errorf("create upload restore for %s: %w", server, err)
	}
	return created.GetName(), string(created.GetUID()), nil
}

// ConfirmUploadRestore sets spec.upload.confirmed on the named Restore. It
// succeeds only while the Restore is AwaitingConfirmation at the given
// generation and is an upload restore; otherwise it returns a Conflict status
// error. The patch carries the resourceVersion it read, so a concurrent write
// makes the apiserver refuse it rather than confirm a changed upload.
func ConfirmUploadRestore(ctx context.Context, dyn dynamic.Interface, ns, name string, generation int64) error {
	u, err := dyn.Resource(GVRRestore).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("confirm restore %s: get: %w", name, err)
	}
	r := &Restore{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, r); err != nil {
		return fmt.Errorf("confirm restore %s: convert from unstructured: %w", name, err)
	}
	if r.Status.Phase != RestorePhaseAwaitingConfirmation || r.Generation != generation || r.Spec.Upload == nil {
		return apierrors.NewConflict(GVRRestore.GroupResource(), name, fmt.Errorf("restore is not awaiting confirmation at generation %d", generation))
	}
	patchBody := map[string]any{"spec": map[string]any{"upload": map[string]any{"confirmed": true}}}
	if r.ResourceVersion != "" {
		patchBody["metadata"] = map[string]any{"resourceVersion": r.ResourceVersion}
	}
	patch, err := json.Marshal(patchBody)
	if err != nil {
		return fmt.Errorf("confirm restore %s: encode patch: %w", name, err)
	}
	if _, err := dyn.Resource(GVRRestore).Namespace(ns).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
		return fmt.Errorf("confirm restore %s: patch: %w", name, err)
	}
	return nil
}

// DeleteRestore deletes a Restore by namespace and name. A missing Restore is
// not an error.
func DeleteRestore(ctx context.Context, dyn dynamic.Interface, ns, name string) error {
	if err := dyn.Resource(GVRRestore).Namespace(ns).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete restore %s: %w", name, err)
		}
	}
	return nil
}
