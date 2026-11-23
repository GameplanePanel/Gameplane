package auth

import "context"

// ActorHolder is a request-scoped, settable username carrier. The audit
// middleware injects one into the request context before authentication
// runs, and Authenticate sets the resolved username on it. This lets the
// (outer) audit middleware learn the acting user even though Authenticate
// stores the user on a child context that does not propagate back up the
// middleware chain — the root cause of audit rows recording "anonymous"
// for authenticated actions.
//
// Set/Name run in the same goroutine sequentially (Authenticate sets
// before the handler runs; audit reads after it returns), so no locking
// is needed.
type ActorHolder struct{ name, source, target string }

// Set records the resolved username. A nil receiver is a no-op so callers
// needn't check whether a holder is present.
func (h *ActorHolder) Set(name string) {
	if h != nil {
		h.name = name
	}
}

// Name returns the recorded username, or "" if none was set.
func (h *ActorHolder) Name() string {
	if h == nil {
		return ""
	}
	return h.name
}

type actorHolderKey struct{}

// WithActorHolder returns a context carrying a fresh ActorHolder plus the
// holder itself, so the caller can read it after the chain runs.
func WithActorHolder(ctx context.Context) (context.Context, *ActorHolder) {
	h := &ActorHolder{}
	return context.WithValue(ctx, actorHolderKey{}, h), h
}

func actorHolderFrom(ctx context.Context) *ActorHolder {
	h, _ := ctx.Value(actorHolderKey{}).(*ActorHolder)
	return h
}

// SetActor records the acting username on the request's audit actor
// holder, if one is present. Authenticate calls this; exported so other
// authenticated entry points can attribute their audit rows too.
func SetActor(ctx context.Context, name string) {
	actorHolderFrom(ctx).Set(name)
}

// SetSource records the request source ("sftp" for SFTP endpoint requests). A nil receiver is a no-op.
func (h *ActorHolder) SetSource(source string) {
	if h != nil {
		h.source = source
	}
}

// Source returns the recorded request source, or "" if none was set.
func (h *ActorHolder) Source() string {
	if h == nil {
		return ""
	}
	return h.source
}

// SetTarget records the audit target the request resolved to (for example "cluster/namespace/server path").
// A nil receiver is a no-op.
func (h *ActorHolder) SetTarget(target string) {
	if h != nil {
		h.target = target
	}
}

// Target returns the recorded audit target, or "" if none was set.
func (h *ActorHolder) Target() string {
	if h == nil {
		return ""
	}
	return h.target
}

// SetSource records the request source on the request's audit actor holder, if one is present.
func SetSource(ctx context.Context, source string) {
	actorHolderFrom(ctx).SetSource(source)
}

// SetAuditTarget records the audit target on the request's audit actor holder, if one is present.
func SetAuditTarget(ctx context.Context, target string) {
	actorHolderFrom(ctx).SetTarget(target)
}
