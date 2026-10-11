# Gameplane shared SteamCMD base image.
#
# This image is a foundation for game server images that use SteamCMD to
# download and install game files. It contains:
# - The SteamCMD binary from the official steamcmd/steamcmd image
# - A non-root user for running game servers
# - The steam-install.sh helper script on PATH (for container entrypoints)
# - Minimal dependencies (ca-certificates for HTTPS)
#
# Game server files are NOT baked into this image. Individual games are
# downloaded at container startup via steam-install.sh into a persistent
# volume. This keeps the image small and avoids redistributing third-party
# game binaries whose licenses are silent on the matter.
#
# Game images derive from this base with:
#   FROM gameplane-steamcmd-base:latest
#   COPY entrypoint.sh /entrypoint.sh
#   ENTRYPOINT ["/entrypoint.sh"]
#
# The entrypoint script can call `steam-install.sh` to populate the volume.
#
# Base image facts (measured directly against steamcmd/steamcmd:latest on a
# real cluster — do not "correct" these back to assumptions):
#   - OS is Ubuntu 26.04 LTS. It is Debian-family: apt-get/apt, useradd/
#     groupadd. There is no apk, no adduser/addgroup (BusyBox forms).
#   - The image runs as root (uid=0). There is no pre-existing "steam" user.
#   - steamcmd is on PATH at /usr/bin/steamcmd (not under /home/steam/...).
#   - The upstream ENTRYPOINT consumes trailing args as steamcmd commands —
#     passing args to it leaves you at an interactive prompt. Derived images
#     must set their own ENTRYPOINT (see "No ENTRYPOINT" note at the bottom).

# Pinning: the tag above is not pinned to a digest. Unlike some other
# Gameplane images, `modules/validate.py --pin` does NOT apply here — that
# flag only rewrites `spec.image` in modules/*/template.yaml, and has no
# notion of a Dockerfile's FROM line, so running it against this directory
# would be a silent no-op. To pin by hand instead:
#   docker pull steamcmd/steamcmd:latest
#   docker inspect --format='{{index .RepoDigests 0}}' steamcmd/steamcmd:latest
# (or `crane digest steamcmd/steamcmd:latest` if docker isn't available),
# then replace the FROM line below with the resulting
# steamcmd/steamcmd@sha256:... reference and commit it.
FROM steamcmd/steamcmd@sha256:a3ea6f8722e7b2ed2300927076794db199febd6fa4a316c0b0af2d2b7ce358f9

# ARGs must be re-declared after FROM to be in scope for RUN instructions in
# this build stage. A bare `ARG X` before the first FROM is scoped ONLY to
# the FROM line itself (for use in an image reference like
# `FROM base:${TAG}`) — it does NOT carry into the stage's instructions.
# Declaring GAMESERVER_UID/GID only at the top of the file (as a previous
# version of this Dockerfile did) means every RUN below would see them as
# empty strings, and `useradd -u ""` would fail. Do not "clean up" this
# apparent duplication — it is required. See:
# https://docs.docker.com/reference/dockerfile/#understand-how-arg-and-from-interact
ARG GAMESERVER_UID=10000
ARG GAMESERVER_GID=10000

# Install dependencies needed by SteamCMD and game servers.
# ca-certificates is needed for HTTPS to the Steam CDN. Whether the upstream
# steamcmd/steamcmd image already ships it could not be verified offline (no
# shell access to introspect the base image's package list from here); the
# install is idempotent and cheap either way, so it stays as a safety net —
# `apt-get install` on an already-installed package is a no-op.
RUN apt-get update && \
    apt-get install -y --no-install-recommends ca-certificates && \
    rm -rf /var/lib/apt/lists/*

# Copy the shared installation helper script.
COPY steam-install.sh /usr/local/bin/steam-install.sh
RUN chmod +x /usr/local/bin/steam-install.sh

# The upstream steamcmd/steamcmd image runs as root — there is no
# pre-existing "steam" user to remove. Create our own non-root user with the
# build-arg UID/GID so gameplane operators can set spec.security.runAsUser
# to a value that modules/validate.py can enforce matches numerically.
#
# --no-log-init avoids useradd writing a sparse /var/log/lastlog entry sized
# to the UID (harmless at UID 10000, but avoids surprises if an operator picks
# a large custom GAMESERVER_UID).
#
# -m creates a home directory (owned by the new user) — SteamCMD writes its
# own state (content manifests, depot cache, login cache) under $HOME by
# default; without a home dir SteamCMD falls back to writing under / , which
# fails once the image runs as non-root. -s points the login shell at
# nologin since this user is never meant to get an interactive shell; the
# game process itself is launched directly by the derived image's
# ENTRYPOINT, not via a shell login.
RUN groupadd -g "${GAMESERVER_GID}" gameserver && \
    useradd -u "${GAMESERVER_UID}" -g gameserver -m -s /usr/sbin/nologin \
        --no-log-init gameserver

# HOME for the gameserver user, matching the -m home directory above. Set
# explicitly (rather than relying on the container runtime to resolve it
# from /etc/passwd) so it's correct regardless of how a derived image's
# ENTRYPOINT invokes the process.
#
# This is deliberately on the image's own writable layer, not the mounted
# game-data volume: SteamCMD's $HOME state (login cache, depot manifests) is
# disposable — losing it just means the next steamcmd run re-fetches
# metadata, it does not affect the actual game install, which
# steam-install.sh always writes to STEAM_INSTALL_DIR on the persistent
# volume via `+force_install_dir`. Keeping $HOME off the persistent volume
# also avoids two containers (or two installs) contending over SteamCMD's
# lock files in the same state dir.
#
# This does NOT survive a read-only root filesystem. If a future hardening
# pass sets `readOnlyRootFilesystem: true` on the pod's securityContext,
# $HOME as created here becomes unwritable and SteamCMD will fail to start.
# That would require the derived image's pod spec to mount a writable
# ephemeral volume (e.g. emptyDir) at /home/gameserver — this Dockerfile
# does not attempt that itself, since the volume mount is a pod-spec /
# Helm-chart concern, not an image concern. Left as an open item for
# whoever adds read-only-rootfs support.
ENV HOME=/home/gameserver

# Gameplane operator may set spec.security.runAsUser to match this UID.
# modules/validate.py enforces that the image's declared User UID and
# spec.security.runAsUser must match numerically.
USER gameserver:gameserver

# OCI metadata (following web/Dockerfile pattern).
ARG VERSION=dev
LABEL org.opencontainers.image.version="${VERSION}"
LABEL org.opencontainers.image.title="Gameplane SteamCMD Base"
LABEL org.opencontainers.image.description="Shared SteamCMD base image for Gameplane game servers"
LABEL org.opencontainers.image.source="https://github.com/gameplanepanel/gameplane"

# No ENTRYPOINT or CMD here — derived images define their own.
# The upstream steamcmd/steamcmd ENTRYPOINT is not inherited: it treats any
# trailing args as steamcmd commands and drops into an interactive prompt,
# which is not what a derived game-server image wants. Child images are
# responsible for their own launch strategy (typically calling
# steam-install.sh, then exec'ing the game binary).
