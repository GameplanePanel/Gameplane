# Gameplane game server image for The Isle (dedicated server).
#
# Installs the public "evrima" beta branch of app 412680 (override with THE_ISLE_BRANCH).
#
# Builds on the shared SteamCMD base (images/common/steamcmd/). The game is
# installed at container start by steam-install.sh into STEAM_INSTALL_DIR
# (default /data, the GameServer's persistent volume). No game files are
# baked into the image.

# CI passes: --build-arg STEAMCMD_BASE_IMAGE=ghcr.io/gameplanepanel/gameplane/common-steamcmd@sha256:...
ARG STEAMCMD_BASE_IMAGE=gameplane-steamcmd-base:latest
FROM ${STEAMCMD_BASE_IMAGE}

# tini (-g) forwards SIGTERM to the whole process group, so the server gets
# it even when it is started through a vendor wrapper script.
USER root
RUN apt-get update && \
    apt-get install -y --no-install-recommends tini && \
    rm -rf /var/lib/apt/lists/*
USER gameserver:gameserver

COPY --chmod=0755 entrypoint.sh /entrypoint.sh

ENTRYPOINT ["/usr/bin/tini", "-g", "--", "/entrypoint.sh"]

ARG VERSION=dev
LABEL org.opencontainers.image.version="${VERSION}"
LABEL org.opencontainers.image.title="Gameplane The Isle Server"
LABEL org.opencontainers.image.description="The Isle (Evrima) dedicated server for Gameplane"
LABEL org.opencontainers.image.source="https://github.com/gameplanepanel/gameplane"
