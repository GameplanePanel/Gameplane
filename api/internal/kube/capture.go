package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	unstructuredpkg "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
)

// The api module deliberately has no compile dependency on the operator
// module (api/go.mod declares replaces only for netguard/gameaction; CRDs
// are read/written exclusively through the dynamic client as
// *unstructured.Unstructured). The types below mirror the relevant subset
// of operator/api/v1alpha1/networkcapture_types.go and
// operator/api/v1alpha1/gameserver_types.go field-for-field (same JSON
// tags) so runtime.DefaultUnstructuredConverter can convert either
// direction, without importing the operator package. See
// api/internal/handlers/mod_ids.go for the same mirroring pattern.

// CapturePhase mirrors operator/api/v1alpha1.CapturePhase.
type CapturePhase string

// CapturePhase constants mirror operator/api/v1alpha1's CapturePhase* consts.
const (
	CapturePhasePending   CapturePhase = "Pending"
	CapturePhaseRunning   CapturePhase = "Running"
	CapturePhaseCompleted CapturePhase = "Completed"
	CapturePhaseFailed    CapturePhase = "Failed"
	CapturePhaseExpired   CapturePhase = "Expired"
)

// NetworkCaptureSpec mirrors operator/api/v1alpha1.NetworkCaptureSpec.
// Each field carries its own doc comment so gofmt formats it independently
// rather than column-aligning the whole struct (a run of aligned fields is
// fragile to hand-edit and easy to get byte-for-byte wrong without running
// gofmt, which this environment cannot do).
type NetworkCaptureSpec struct {
	// ServerRef is the GameServer being captured.
	ServerRef corev1.LocalObjectReference `json:"serverRef"`
	// Filter is a pcap-filter(7) expression restricting captured packets.
	Filter *string `json:"filter,omitempty"`
	// MaxDuration is the maximum runtime for the capture.
	MaxDuration *metav1.Duration `json:"maxDuration"`
	// MaxSize is the maximum file size for the capture.
	MaxSize *resource.Quantity `json:"maxSize"`
	// TTLSecondsAfterFinished is the auto-expiration window after completion.
	TTLSecondsAfterFinished *int32 `json:"ttlSecondsAfterFinished,omitempty"`
}

// NetworkCaptureStatus mirrors operator/api/v1alpha1.NetworkCaptureStatus.
type NetworkCaptureStatus struct {
	// Phase is the current lifecycle phase of the capture.
	Phase CapturePhase `json:"phase"`
	// StartTime is when the capture started.
	StartTime *metav1.Time `json:"startTime,omitempty"`
	// CompletionTime is when the capture stopped.
	CompletionTime *metav1.Time `json:"completionTime,omitempty"`
	// PacketsWritten is the number of packets captured.
	PacketsWritten int64 `json:"packetsWritten,omitempty"`
	// BytesWritten is the total size of the captured file, in bytes.
	BytesWritten *resource.Quantity `json:"bytesWritten,omitempty"`
	// Message is a human-readable status or error message.
	Message string `json:"message,omitempty"`
	// Conditions contains the operator's observed lifecycle outcomes.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// CaptureStopRequestedAnnotation is the annotation StopNetworkCapture sets
// to ask the operator to stop a capture (F-259). Its value is the RFC3339
// UTC time of the first stop request. It must match
// operator/internal/controller/networkcapture_controller.go's
// stopRequestedAnnotation exactly: the operator's reconciler is the only
// writer of the resulting Completed phase.
const CaptureStopRequestedAnnotation = "gameplane.local/stop-requested"

// StopRequested reports whether a stop has already been requested for nc.
func (nc *NetworkCapture) StopRequested() bool {
	_, ok := nc.Annotations[CaptureStopRequestedAnnotation]
	return ok
}

// NetworkCapture mirrors operator/api/v1alpha1.NetworkCapture.
type NetworkCapture struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NetworkCaptureSpec   `json:"spec,omitempty"`
	Status NetworkCaptureStatus `json:"status,omitempty"`
}

// CaptureConfiguration mirrors operator/api/v1alpha1.CaptureConfiguration
// (only the fields the API needs to read).
type CaptureConfiguration struct {
	Enabled        bool `json:"enabled,omitempty"`
	NodeDiagnostic bool `json:"nodeDiagnostic,omitempty"`
	// RetentionSeconds is the per-GameServer override of the cluster-default
	// retention window applied when a capture-start request omits its own
	// ttlSecondsAfterFinished. Still bounded by the cluster maximum, the same
	// as any client-supplied value.
	RetentionSeconds *int32 `json:"retentionSeconds,omitempty"`
}

// GameServerCaptureStatus mirrors the status.capture subset of
// operator/api/v1alpha1.CaptureStatus (only the field the API needs to read).
type GameServerCaptureStatus struct {
	ActiveCapture *string `json:"activeCapture,omitempty"`
}

// GameServer is the minimal projection of operator/api/v1alpha1.GameServer
// the capture handlers need: identity plus spec.capture.enabled and
// status.capture.activeCapture. Not a full mirror of the CRD — extend the
// nested Spec/Status structs here if a future handler needs another field.
type GameServer struct {
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec struct {
		Capture *CaptureConfiguration `json:"capture,omitempty"`
	} `json:"spec,omitempty"`
	Status struct {
		Capture *GameServerCaptureStatus `json:"capture,omitempty"`
	} `json:"status,omitempty"`
}

// GetGameServer fetches a GameServer by namespace and name, converted to
// the local GameServer projection so callers can read spec.capture/status
// without hand-walking unstructured fields. Unlike GetServer (whose doc
// comment claims a nil-on-not-found contract it doesn't actually
// implement), this propagates the real error — callers classify it with
// k8s.io/apimachinery/pkg/api/errors.IsNotFound.
func (c *Client) GetGameServer(ctx context.Context, ns, name string) (*GameServer, error) {
	u, err := c.GetServer(ctx, ns, name)
	if err != nil {
		return nil, fmt.Errorf("get game server %s: %w", name, err)
	}
	gs := &GameServer{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, gs); err != nil {
		return nil, fmt.Errorf("get game server %s: convert from unstructured: %w", name, err)
	}
	return gs, nil
}

// CreateNetworkCapture creates a new NetworkCapture CRD owned by the target
// GameServer. serverUID carries the GameServer's UID so the created object
// gets a real ownerReference — without it, a NetworkCapture would outlive
// the GameServer it was taken against and never be garbage-collected with
// it. The object returned reflects what the apiserver actually persisted
// (server-assigned name/creationTimestamp/status), not the unsubmitted
// local copy.
func (c *Client) CreateNetworkCapture(
	ctx context.Context,
	ns,
	captureID,
	serverName string,
	serverUID types.UID,
	filter *string,
	maxDuration *metav1.Duration,
	maxSize *resource.Quantity,
	ttlSecondsAfterFinished *int32,
) (*NetworkCapture, error) {
	ownerRefs := []metav1.OwnerReference{
		{
			APIVersion: "gameplane.local/v1alpha1",
			Kind:       "GameServer",
			Name:       serverName,
			UID:        serverUID,
		},
	}
	nc := &NetworkCapture{
		// TypeMeta MUST be set explicitly here: this object is converted to
		// *unstructured.Unstructured below and submitted via the dynamic
		// client, not a typed clientset. For unstructured Create requests
		// the apiserver's generic REST handler ignores the URL-implied
		// default GVK entirely (unstructuredJSONScheme.Decode discards its
		// defaultGVK parameter, apimachinery/pkg/apis/meta/v1/unstructured/
		// helpers.go) and rejects a body with no "kind" as
		// runtime.NewMissingKindErr, which the apiserver maps to
		// errors.NewBadRequest ("the object provided is unrecognized") —
		// classified by httperr as a bare, opaque "bad request" 400. Typed
		// clientsets don't need this because their decode path defaults the
		// GVK from the URL; the unstructured path does not.
		TypeMeta: metav1.TypeMeta{
			APIVersion: "gameplane.local/v1alpha1",
			Kind:       "NetworkCapture",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:            captureID,
			Namespace:       ns,
			OwnerReferences: ownerRefs,
		},
		Spec: NetworkCaptureSpec{
			ServerRef: corev1.LocalObjectReference{
				Name: serverName,
			},
			Filter:                  filter,
			MaxDuration:             maxDuration,
			MaxSize:                 maxSize,
			TTLSecondsAfterFinished: ttlSecondsAfterFinished,
		},
	}

	raw, err := runtime.DefaultUnstructuredConverter.ToUnstructured(nc)
	if err != nil {
		return nil, fmt.Errorf("create network capture %s: convert to unstructured: %w", captureID, err)
	}

	created, err := c.Dynamic.Resource(GVRNetworkCapture).Namespace(ns).
		Create(ctx, &unstructuredpkg.Unstructured{Object: raw}, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("create network capture %s: %w", captureID, err)
	}

	// The CRD declares subresources.status, so the apiserver strips any
	// "status" field from the create body above — a NetworkCapture is
	// always born with status.phase=="" from Create alone. Set it to
	// Pending here via a UpdateStatus call so callers (and the reconciler,
	// which gates on Status.Phase) see a real phase immediately rather
	// than waiting on the first reconcile to notice an empty phase.
	//
	// The freshly-created object can legitimately be modified between the
	// Create above and this UpdateStatus — e.g. the operator's reconciler
	// picks up the new NetworkCapture and writes a status update of its own
	// before we get here, which is a normal, expected race (worse under
	// CI's slower/contended runners), not a client error. A resourceVersion
	// conflict on that update must not surface as a 500; retry against the
	// latest version rather than let the conflict escape to the handler.
	//
	// `latest` is the object the next attempt writes: `created` for the
	// first attempt, and nil after a failed one to force a re-fetch. On
	// success `updated` holds the object the caller gets back, and it is
	// only ever set on the closure's nil-error paths — so it is non-nil
	// whenever retryErr == nil.
	var updated *unstructuredpkg.Unstructured
	latest := created
	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if latest == nil {
			fresh, getErr := c.Dynamic.Resource(GVRNetworkCapture).Namespace(ns).
				Get(ctx, captureID, metav1.GetOptions{})
			if getErr != nil {
				return fmt.Errorf("re-fetch after conflict: %w", getErr)
			}
			latest = fresh
		}
		// Never write Pending over a phase the reconciler has already
		// advanced past. The operator treats an empty phase as Pending
		// itself (networkcapture_controller.go: `if nc.Status.Phase == ""`),
		// so this update is a UX convenience, not a precondition — whereas
		// stomping a Running or Failed it just set would send it back
		// through the Pending branch and start the capture a second time.
		if phase, _, err := unstructuredpkg.NestedString(latest.Object, "status", "phase"); err == nil && phase != "" {
			updated = latest
			return nil
		}
		if err := unstructuredpkg.SetNestedField(latest.Object, string(CapturePhasePending), "status", "phase"); err != nil {
			return fmt.Errorf("set pending phase: %w", err)
		}
		result, err := c.Dynamic.Resource(GVRNetworkCapture).Namespace(ns).
			UpdateStatus(ctx, latest, metav1.UpdateOptions{})
		if err != nil {
			// Both a conflict (retried) and any other failure (returned
			// as-is by RetryOnConflict) leave `latest` stale; drop it so a
			// retry starts from a fresh read.
			latest = nil
			return err
		}
		updated = result
		return nil
	})
	if retryErr != nil {
		return nil, fmt.Errorf("create network capture %s: set pending phase: %w", captureID, retryErr)
	}

	result := &NetworkCapture{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(updated.Object, result); err != nil {
		return nil, fmt.Errorf("create network capture %s: convert from unstructured: %w", captureID, err)
	}
	return result, nil
}

// GetNetworkCapture fetches a NetworkCapture by namespace and name.
// Returns nil if not found.
func (c *Client) GetNetworkCapture(ctx context.Context, ns, name string) (*NetworkCapture, error) {
	u, err := c.Dynamic.Resource(GVRNetworkCapture).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("get network capture %s: %w", name, err)
	}

	nc := &NetworkCapture{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, nc); err != nil {
		return nil, fmt.Errorf("get network capture %s: convert from unstructured: %w", name, err)
	}
	return nc, nil
}

// ListNetworkCaptures lists all NetworkCaptures for a GameServer in a namespace.
func (c *Client) ListNetworkCaptures(ctx context.Context, ns, serverName string) ([]NetworkCapture, error) {
	ul, err := c.Dynamic.Resource(GVRNetworkCapture).Namespace(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list network captures for server %s: %w", serverName, err)
	}

	var captures []NetworkCapture
	for _, u := range ul.Items {
		nc := &NetworkCapture{}
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, nc); err != nil {
			return nil, fmt.Errorf("list network captures for server %s: convert from unstructured: %w", serverName, err)
		}
		if nc.Spec.ServerRef.Name == serverName {
			captures = append(captures, *nc)
		}
	}
	return captures, nil
}

// StopNetworkCapture requests that the operator stop a NetworkCapture by
// setting the gameplane.local/stop-requested annotation (F-259; maintainer
// decision 2026-09-25, see api/specs.md "Network capture stop flow"). It
// deliberately does NOT touch status: the operator's NetworkCapture
// reconciler tells the capture sidecar to stop (which closes and flushes
// the PCAPNG file) and only then sets phase=Completed, so Completed always
// means "downloadable". Patching phase=Completed here, as earlier versions
// did, opened a window where a download reached a sidecar still holding the
// capture as running and got a 409.
//
// Idempotent: when the annotation is already present the capture is
// returned unchanged, so a repeated stop keeps the original request time.
// The patch carries the observed resourceVersion, so two concurrent stops
// cannot both write the annotation; the loser re-reads and returns the
// winner's object.
func (c *Client) StopNetworkCapture(ctx context.Context, ns, name string) (*NetworkCapture, error) {
	res := c.Dynamic.Resource(GVRNetworkCapture).Namespace(ns)
	var out *NetworkCapture
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		u, err := res.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if _, ok := u.GetAnnotations()[CaptureStopRequestedAnnotation]; !ok {
			metadata := map[string]any{
				"annotations": map[string]any{
					CaptureStopRequestedAnnotation: time.Now().UTC().Format(time.RFC3339),
				},
			}
			if rv := u.GetResourceVersion(); rv != "" {
				metadata["resourceVersion"] = rv
			}
			patch := map[string]any{"metadata": metadata}
			patchBytes, err := json.Marshal(patch)
			if err != nil {
				return fmt.Errorf("marshal patch: %w", err)
			}
			u, err = res.Patch(ctx, name, types.MergePatchType, patchBytes, metav1.PatchOptions{})
			if err != nil {
				return err
			}
		}
		nc := &NetworkCapture{}
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, nc); err != nil {
			return fmt.Errorf("convert from unstructured: %w", err)
		}
		out = nc
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("stop network capture %s: %w", name, err)
	}
	return out, nil
}

// DeleteNetworkCapture deletes a NetworkCapture by namespace and name.
func (c *Client) DeleteNetworkCapture(ctx context.Context, ns, name string) error {
	if err := c.Dynamic.Resource(GVRNetworkCapture).Namespace(ns).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete network capture %s: %w", name, err)
		}
	}
	return nil
}
