FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
WORKDIR /src

# netguard, gameaction, gp-module, and telemetryschema are in-repo modules the API depends
# on via local replaces (../netguard, ../gameaction, ../gp-module, ../telemetryschema);
# copy them first so `go mod download` and the build resolve them.
COPY netguard/ ./netguard/
COPY gameaction/ ./gameaction/
COPY gp-module/ ./gp-module/
COPY telemetryschema/ ./telemetryschema/
COPY api/go.mod api/go.sum* ./api/
WORKDIR /src/api
RUN go mod download

WORKDIR /src
COPY api/ ./api/
WORKDIR /src/api

ARG VERSION=dev
# modernc sqlite is pure Go, no CGO required.
# Cross-compile on the build host ($BUILDPLATFORM above) rather than under
# QEMU emulation: multi-arch release builds of the arm64 image were slow
# enough to hit the release job timeout.
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build \
    -trimpath \
    -ldflags="-s -w -X main.Version=${VERSION}" \
    -o /out/api ./cmd

RUN mkdir -p /out/data /out/keys && chmod 0700 /out/keys && chown 65532:65532 /out/data /out/keys

FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=build /out/api /api
# Seed named Docker volumes with a directory writable by the runtime user.
COPY --from=build --chown=65532:65532 /out/data /data
COPY --from=build --chown=65532:65532 /out/keys /keys
USER 65532:65532
EXPOSE 8000
VOLUME ["/data"]
ENTRYPOINT ["/api"]
