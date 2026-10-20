FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
WORKDIR /src

# telemetryschema is an in-repo module the receiver depends on via a local replace
# (../telemetryschema); copy it first so `go mod download` and the build resolve it.
COPY telemetryschema/ ./telemetryschema/
COPY telemetry-receiver/go.mod telemetry-receiver/go.sum* ./telemetry-receiver/
WORKDIR /src/telemetry-receiver
RUN go mod download

WORKDIR /src
COPY telemetry-receiver/ ./telemetry-receiver/
WORKDIR /src/telemetry-receiver

ARG VERSION=dev
# Cross-compile on the build host ($BUILDPLATFORM above) rather than under
# QEMU emulation: multi-arch release builds of the arm64 image were slow
# enough to hit the release job timeout.
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build \
    -trimpath \
    -ldflags="-s -w -X main.Version=${VERSION}" \
    -o /out/telemetry-receiver .

FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=build /out/telemetry-receiver /telemetry-receiver
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/telemetry-receiver"]
