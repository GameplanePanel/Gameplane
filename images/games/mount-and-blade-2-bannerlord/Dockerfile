# Gameplane game server image for Mount & Blade II: Bannerlord (dedicated server).
#
# TaleWorlds ships the dedicated server as a Windows build only, so SteamCMD downloads it with STEAM_PLATFORM=windows and it runs under Wine. SERVER_TOKEN (from the in-game "customserver.gettoken" command, valid for 3 months) is required; without it the container idles with instructions instead of crash-looping (FR-013).
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
    apt-get install -y --no-install-recommends tini wine && \
    rm -rf /var/lib/apt/lists/*
USER gameserver:gameserver

COPY --chmod=0755 entrypoint.sh /entrypoint.sh

ENTRYPOINT ["/usr/bin/tini", "-g", "--", "/entrypoint.sh"]

ARG VERSION=dev
LABEL org.opencontainers.image.version="${VERSION}"
LABEL org.opencontainers.image.title="Gameplane Mount & Blade II: Bannerlord Server"
LABEL org.opencontainers.image.description="Mount & Blade II: Bannerlord dedicated server (Windows build under Wine) for Gameplane"
LABEL org.opencontainers.image.source="https://github.com/gameplanepanel/gameplane"
