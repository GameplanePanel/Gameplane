# Wake retry and mixed-load regression (#729)

## Intent and constraints

A short Kubernetes API outage should not discard a genuine join's wake signal.
Retry within the existing shared wake operation and total join deadline, without
creating a retry loop per player. Preserve successful-PATCH suppression, the UDP
capacity-one worker, per-player upstream sockets, and session shutdown draining.
No new dependency, environment variable, or public configuration is needed.

## Approach

Implementation choice: a focused `sentinel/wake_retry.go` helper retries the
existing PATCH closure. Retrying in each handler would multiply load; replacing
the shared-operation lifecycle would unnecessarily change cancellation behavior.
Generate one annotation token and immutable merge patch per shared operation.

Implementation choices: at most four application-level PATCH calls in the
existing five-second PATCH context (also bounded by the owner's join context).
Use exponential backoff starting at 100 ms, capped at 800 ms, with +/-25% jitter
and a final 800 ms cap. A retry delay suggested by Kubernetes is a minimum, subject
to context cancellation. Recognizable transient API/transport errors are eligible;
permanent API errors and unknown errors return immediately. Preserve the last API
error for `errors.Is/As`, including cancellation during backoff. Never renew the
five-second budget per attempt. Existing client-go transport retries remain
subject to this same context; the application bound counts resource PATCH calls.

Live waiters may still replace a canceled owning caller; canceled callers never
start another attempt. Only success starts the existing suppression interval.

## Regression and measurement

Write regression sources before production changes. Cover transient recovery,
attempt limits, permanent/unknown errors, cancellation and deadlines during
backoff, shared callers, retry-after, stable patch bytes, and jitter bounds.

Add a deterministic mixed-load fixture with actual loopback TCP/UDP listeners
and an HTTP fake Kubernetes API. Hold the outage response until TCP callers and
UDP packet reception are observable, recover the API, and explicitly gate the
delayed cold start. Measure attempts, shared PATCH concurrency, TCP handoffs,
UDP receives, UDP wake concurrency, elapsed time, and handoff latency. Verify
gameplay beyond the join deadline and listener/session shutdown. Event channels
coordinate phases; time bounds are safety timeouts, not capacity claims.

CI should print the mixed-load fixture's logs in the existing sentinel Go matrix
leg without changing test-selection policy. Runtime tests and lint run only in
CI. Local verification is limited to compilation; red/green runtime verification
is explicitly deferred to CI.
