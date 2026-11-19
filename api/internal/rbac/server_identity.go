package rbac

import (
	"context"
	"errors"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// ServerIdentity is the exact resource that granted owner/collaborator access.
type ServerIdentity struct {
	Cluster   string
	Namespace string
	Name      string
	UID       string
}

type serverIdentityKey struct{}

type serverGrant struct {
	ServerIdentity
	ownerOnly bool
}

func (g serverGrant) permits(obj *unstructured.Unstructured, userID int64) bool {
	if obj == nil {
		return false
	}
	role := ownershipRole(obj, userID)
	return role == roleOwner || (!g.ownerOnly && role == roleCollaborator)
}

func bindServerGrant(ctx context.Context, fetch ServerFetcher, cluster, ns, name string, userID int64, ownerOnly bool) (context.Context, bool) {
	if fetch == nil {
		return ctx, false
	}
	obj, err := fetch.GetServer(ctx, cluster, ns, name)
	grant := serverGrant{ownerOnly: ownerOnly}
	if err != nil || !grant.permits(obj, userID) {
		return ctx, false
	}
	grant.ServerIdentity = ServerIdentity{Cluster: cluster, Namespace: ns, Name: name, UID: string(obj.GetUID())}
	return context.WithValue(ctx, serverIdentityKey{}, grant), true
}

// StreamGrantValidator rechecks the exact resource grant used to open a stream.
// A namespace-wide SQL permission needs no additional Kubernetes lookup.
func StreamGrantValidator(fetch ServerFetcher) func(context.Context, int64) bool {
	return func(ctx context.Context, userID int64) bool {
		grant, bound := ctx.Value(serverIdentityKey{}).(serverGrant)
		if !bound {
			return true
		}
		if fetch == nil {
			return false
		}
		obj, err := fetch.GetServer(ctx, grant.Cluster, grant.Namespace, grant.Name)
		return err == nil && grant.permits(obj, userID) &&
			ValidateServerIdentity(ctx, grant.Cluster, obj.GetNamespace(), obj.GetName(), string(obj.GetUID())) == nil
	}
}

// ErrServerIdentityChanged rejects a resource different from the one authorized.
var ErrServerIdentityChanged = errors.New("authorized server identity changed")

// BoundServerIdentity returns the immutable identity used by an ownership grant.
// Namespace-wide permissions remain unbound unless an owner-only operation also
// requires ownership of the individual server.
func BoundServerIdentity(ctx context.Context) (ServerIdentity, bool) {
	grant, ok := ctx.Value(serverIdentityKey{}).(serverGrant)
	return grant.ServerIdentity, ok
}

// ValidateServerIdentity prevents a later lookup from silently rebinding an
// ownership grant to a replacement server. Unbound namespace permissions retain
// their existing behavior because they authorize every server in that scope.
func ValidateServerIdentity(ctx context.Context, cluster, namespace, name, uid string) error {
	bound, ok := BoundServerIdentity(ctx)
	if !ok {
		return nil
	}
	if bound.UID == "" || bound != (ServerIdentity{Cluster: cluster, Namespace: namespace, Name: name, UID: uid}) {
		return ErrServerIdentityChanged
	}
	return nil
}
