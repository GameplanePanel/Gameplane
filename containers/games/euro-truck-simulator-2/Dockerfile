# Gameplane game server image for Euro Truck Simulator 2 (dedicated server).
#
# Builds on the shared SteamCMD base (containers/common/steamcmd/). The server
# (Steam app 1948160, anonymous login) is installed at container start by
# steam-install.sh into GP_DATA_DIR (set by the template; the GameServer's persistent
# volume). No game files are baked into the image.

# CI passes: --build-arg STEAMCMD_BASE_IMAGE=ghcr.io/gameplanepanel/gameplane/common-steamcmd@sha256:...
ARG STEAMCMD_BASE_IMAGE=gameplane-steamcmd-base:latest
FROM ${STEAMCMD_BASE_IMAGE}

LABEL org.opencontainers.image.title="Gameplane Euro Truck Simulator 2 Dedicated Server" \
      org.opencontainers.image.description="Dedicated server container for Euro Truck Simulator 2 with diagnostic idle support for missing logon tokens" \
      org.opencontainers.image.authors="Gameplane Maintainers" \
      org.opencontainers.image.source="https://github.com/GameplanePanel/Gameplane"

USER root
RUN apt-get update && \
    apt-get install -y --no-install-recommends tini=0.19.0-6 && \
    rm -rf /var/lib/apt/lists/*
USER gameserver:gameserver

COPY --chmod=0755 entrypoint.sh /entrypoint.sh

WORKDIR /data
ENV GAME_PORT=27015 \
    QUERY_PORT=27016

EXPOSE 27015/udp 27016/udp

ENTRYPOINT ["/usr/bin/tini", "-g", "--", "/entrypoint.sh"]
