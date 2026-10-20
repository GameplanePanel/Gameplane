# Gameplane dashboard image: build the Vite SPA, serve it with nginx.
#
# Build context is the repo root (matches the other components: the
# publish/release workflows pass `context: .` and `file: web/Dockerfile`).
# Everything this stage needs lives under web/, so only web/ is copied in.

# ---- compute the language manifest ----
# The language listing rule (which locales are shown in pickers) lives in the
# shared Go checker (i18n/), so the manifest is produced by that checker here
# rather than re-implemented in TypeScript. Only i18n/ is needed for this stage.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS i18n
WORKDIR /src
COPY i18n/ ./i18n/
WORKDIR /src/i18n
# i18n-check exit 1 = findings: it still writes the filtered packs and manifest,
# so the image build accepts exit 1. CI fails on findings separately. Exit 2+ is a
# real failure and still fails the build. The manifest must exist either way.
RUN mkdir -p /out && go build -o /tmp/i18n-check ./cmd/i18n-check && { /tmp/i18n-check --manifest /out/languages.json --valid-out /out/valid locales || [ $? -eq 1 ]; } && test -f /out/languages.json

# ---- build the SPA ----
FROM node:26-bookworm-slim AS build
WORKDIR /src/web

# Install deps first (cached until the lockfile changes), then build.
COPY web/package.json web/package-lock.json* ./
RUN npm ci

# Shared locale catalogs live outside web/; the i18n scripts and Vite alias
# read them from ../i18n/locales relative to /src/web.
COPY i18n/locales ../i18n/locales
# Filtered packs (only entries the checker accepts), written by the i18n stage.
# The compile script reads them through GAMEPLANE_I18N_VALID, so no Go is needed here.
COPY --from=i18n /out/valid ../i18n/valid
ENV GAMEPLANE_I18N_VALID=../i18n/valid

COPY web/ ./
# Generated language manifest (computed by the i18n stage above).
COPY --from=i18n /out/languages.json ./src/generated/i18n/languages.json
# Vite emits to web/dist. tsc + vite build run via `npm run build`.
RUN npm run build

# ---- serve with a non-root nginx ----
# nginx-unprivileged listens on 8080 and runs as uid 101 (nginx), so it needs
# no root and satisfies runAsNonRoot/readOnlyRootFilesystem in the chart.
FROM nginxinc/nginx-unprivileged:1.31-alpine

# The publish/release workflows pass --build-arg VERSION=…; consume it as a
# provenance label (and to avoid a BuildKit "unconsumed build arg" warning).
ARG VERSION=dev
LABEL org.opencontainers.image.version="${VERSION}"

# API_UPSTREAM is substituted into the config at container start by the base
# image's envsubst step (it templates /etc/nginx/templates/*.template into
# /etc/nginx/conf.d/). The chart sets it to the in-cluster API service; this
# default keeps a bare `docker run` from failing to start.
ENV API_UPSTREAM=http://gameplane-api.gameplane-system.svc.cluster.local

# Replace the base image's default server block with ours.
RUN rm -f /etc/nginx/conf.d/default.conf
COPY web/nginx.conf.template /etc/nginx/templates/gameplane.conf.template

# Sourced at start (before envsubst) to export NGINX_RESOLVER from the pod's
# /etc/resolv.conf — see the file and nginx.conf.template for why. The nginx
# entrypoint sources *.envsh ONLY if it's executable; the file is committed
# mode 0755 and COPY preserves it, so no chmod is needed. (A RUN chmod fails
# here: the base image runs as the non-root nginx user, which can't chmod a
# root-owned COPY — that broke the first build.)
COPY web/docker-entrypoint.d/15-resolver.envsh /docker-entrypoint.d/15-resolver.envsh

# Static assets.
COPY --from=build /src/web/dist /usr/share/nginx/html

EXPOSE 8080
