FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
WORKDIR /src

COPY capture-sidecar/go.mod capture-sidecar/go.sum* ./capture-sidecar/
WORKDIR /src/capture-sidecar
RUN go mod download

WORKDIR /src
COPY capture-sidecar/ ./capture-sidecar/
WORKDIR /src/capture-sidecar

RUN apk add --no-cache libcap~=2.78

ARG VERSION=dev
# Cross-compile on the build host ($BUILDPLATFORM above) rather than under
# QEMU emulation: multi-arch release builds of the arm64 image were slow
# enough to hit the release job timeout.
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build \
    -trimpath \
    -ldflags="-s -w -X main.Version=${VERSION}" \
    -o /out/capture-sidecar ./cmd

# Apply CAP_NET_RAW via file capabilities instead of securityContext.capabilities.add.
# This is required because Kubernetes does not grant ambient capabilities — a
# non-root process's effective capability set is cleared on execve regardless of
# what the pod spec's capabilities.add list contains. File capabilities are the
# only mechanism that survives a non-root execve. The final stage relies on this
# grant and runs as nonroot, accepting allowPrivilegeEscalation: true to permit
# file capabilities to take effect (they are ignored under no_new_privs).
RUN setcap cap_net_raw+ep /out/capture-sidecar

FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=build /out/capture-sidecar /capture-sidecar
USER 65532:65532
EXPOSE 9091
ENTRYPOINT ["/capture-sidecar"]
