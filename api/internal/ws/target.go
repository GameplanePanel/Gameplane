package ws

import (
	"context"
	"fmt"
	"net/http"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/rbac"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// streamClient resolves the same selector RBAC authorizes, then pins a client
// to this connection. Removed, unknown, or unavailable clusters never fall
// back to the home cluster. No shared handler state changes per request.
func streamClient(w http.ResponseWriter, req *http.Request, reg *kube.Registry) (*kube.Client, bool) {
	if reg == nil {
		http.Error(w, "cluster unavailable", http.StatusServiceUnavailable)
		return nil, false
	}
	id, err := scope.ResolveCluster(req, reg)
	if err != nil {
		httperr.Write(w, req, err)
		return nil, false
	}
	if bound, ok := rbac.BoundServerIdentity(req.Context()); ok && bound.Cluster != id {
		http.NotFound(w, req)
		return nil, false
	}
	k, ok := reg.Get(id)
	if !ok {
		httperr.Write(w, req, scope.ErrForbiddenCluster)
		return nil, false
	}
	if k == nil || k.Typed == nil || k.Dynamic == nil {
		http.Error(w, "cluster unavailable", http.StatusServiceUnavailable)
		return nil, false
	}
	return k, true
}

// serverPod verifies the operator's ownership chain, not just a predictable
// pod name. A stale StatefulSet or unrelated same-named pod cannot satisfy
// authorization for a newly created GameServer.
func serverPod(ctx context.Context, k *kube.Client, ns, name string) (*corev1.Pod, error) {
	return serverPodForUID(ctx, k, ns, name, "")
}

// localAgentTarget binds a new local operation before opening its transport.
// The agent independently enforces this UID on its versioned route.
func localAgentTarget(ctx context.Context, k *kube.Client, target agentTarget) (agentTarget, error) {
	if err := target.validate(); err != nil {
		return agentTarget{}, apierrors.NewBadRequest(err.Error())
	}
	if k == nil || k.Dynamic == nil {
		return agentTarget{}, apierrors.NewServiceUnavailable("cluster unavailable")
	}
	server, err := k.GetServer(ctx, target.namespace, target.name)
	if err != nil {
		return agentTarget{}, err
	}
	if err := rbac.ValidateServerIdentity(ctx, scope.DefaultCluster, target.namespace, target.name, string(server.GetUID())); err != nil || server.GetUID() == "" {
		return agentTarget{}, apierrors.NewNotFound(kube.GVRs["servers"].GroupResource(), target.name)
	}
	target.uid = string(server.GetUID())
	return target, nil
}

// serverPodForUID also binds ownership preflight to an earlier authorized or
// resolved GameServer identity. A legitimate replacement's ownership chain must
// not be accepted for an operation on the previous server.
func serverPodForUID(ctx context.Context, k *kube.Client, ns, name, expectedUID string) (*corev1.Pod, error) {
	gs, err := k.GetServer(ctx, ns, name)
	if err != nil {
		return nil, fmt.Errorf("get stream server: %w", err)
	}
	if bound, ok := rbac.BoundServerIdentity(ctx); ok {
		if err := rbac.ValidateServerIdentity(ctx, bound.Cluster, ns, name, string(gs.GetUID())); err != nil {
			return nil, apierrors.NewNotFound(schema.GroupResource{Group: "gameplane.local", Resource: "gameservers"}, name)
		}
	}
	if expectedUID != "" && string(gs.GetUID()) != expectedUID {
		return nil, apierrors.NewNotFound(schema.GroupResource{Group: "gameplane.local", Resource: "gameservers"}, name)
	}
	ss, err := k.Typed.AppsV1().StatefulSets(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get stream statefulset: %w", err)
	}
	pod, err := k.Typed.CoreV1().Pods(ns).Get(ctx, name+"-0", metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get stream pod: %w", err)
	}
	if !controlledBy(ss, "gameplane.local/v1alpha1", "GameServer", name, gs.GetUID()) ||
		!controlledBy(pod, "apps/v1", "StatefulSet", name, ss.UID) || pod.UID == "" {
		return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "pods"}, name+"-0")
	}
	return pod, nil
}

func controlledBy(obj metav1.Object, apiVersion, kind, name string, uid types.UID) bool {
	owner := metav1.GetControllerOf(obj)
	return uid != "" && owner != nil && owner.APIVersion == apiVersion &&
		owner.Kind == kind && owner.Name == name && owner.UID == uid
}
