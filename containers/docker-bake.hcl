# Bake definition for the e2e images (used by .github/actions/e2e-images).
# `docker buildx bake --load e2e` builds all seven concurrently —
# compile steps are independent, so this cuts image-build wall time versus
# sequential `docker build`s. Tags match what `make e2e-images` produces
# and what deploy/kind/e2e.sh loads.
group "e2e" {
  targets = ["e2e-operator", "e2e-api", "e2e-agent", "e2e-sentinel", "e2e-capture-sidecar", "e2e-telemetry-receiver", "e2e-fakeoidc"]
}

target "e2e-operator" {
  context    = "."
  dockerfile = "operator/Dockerfile"
  tags       = ["gameplane-test/operator:e2e"]
}

target "e2e-api" {
  context    = "."
  dockerfile = "api/Dockerfile"
  tags       = ["gameplane-test/api:e2e"]
}

target "e2e-agent" {
  context    = "."
  dockerfile = "agent/Dockerfile"
  tags       = ["gameplane-test/agent:e2e"]
}

target "e2e-sentinel" {
  context    = "."
  dockerfile = "sentinel/Dockerfile"
  tags       = ["gameplane-test/sentinel:e2e"]
}

target "e2e-capture-sidecar" {
  context    = "."
  dockerfile = "capture-sidecar/Dockerfile"
  tags       = ["gameplane-test/capture-sidecar:e2e"]
}

# The bundled telemetry receiver every e2e cluster deploys (deploy/kind/e2e.sh
# sets api.telemetry.receiver.enabled=true, spec 022 R17).
target "e2e-telemetry-receiver" {
  context    = "."
  dockerfile = "telemetry-receiver/Dockerfile"
  tags       = ["gameplane-test/telemetry-receiver:e2e"]
}

# The fake OIDC issuer every e2e bucket's cluster bring-up points the
# api Deployment's --oidc-issuer at (deploy/kind/e2e.sh), so it lives in
# the base "e2e" group rather than being an opt-in target like gameprobe.
target "e2e-fakeoidc" {
  context    = "."
  dockerfile = "test/e2e/Dockerfile.fakeoidc"
  tags       = ["gameplane-test/fakeoidc:e2e"]
}

# The headless protocol bot the game-bot job runs inside the cluster. It is
# deliberately outside the "e2e" group: only that one job needs it, and the
# other e2e buckets shouldn't pay to build it.
target "e2e-gameprobe" {
  context    = "."
  dockerfile = "test/e2e/Dockerfile"
  tags       = ["gameplane-test/gameprobe:e2e"]
}
