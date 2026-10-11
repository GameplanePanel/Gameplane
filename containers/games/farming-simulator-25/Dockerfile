FROM debian:bookworm-slim

LABEL org.opencontainers.image.title="Gameplane Farming Simulator 25 Dedicated Server" \
      org.opencontainers.image.description="Dedicated server container for Farming Simulator 25 with headless Wine, dummy X11, and web admin portal supervision" \
      org.opencontainers.image.authors="Gameplane Maintainers" \
      org.opencontainers.image.source="https://github.com/GameplanePanel/Gameplane"

ENV DEBIAN_FRONTEND=noninteractive

RUN dpkg --add-architecture i386 && \
    apt-get update && \
    apt-get install -y --no-install-recommends \
        bash \
        curl \
        ca-certificates \
        tini \
        procps \
        xvfb \
        xauth \
        wine \
        wine32 \
        wine64 \
        libwine \
        libwine:i386 && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*

# Create gameplane runtime user with fixed UID/GID 1000
RUN groupadd -g 1000 gameplane && \
    useradd -u 1000 -g gameplane -m -s /bin/bash gameplane

# Prepare directories
RUN mkdir -p /serverdata /home/gameplane/.wine && \
    chown -R gameplane:gameplane /serverdata /home/gameplane

COPY --chown=gameplane:gameplane entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

USER 1000:1000
WORKDIR /serverdata
ENV HOME=/home/gameplane \
    USER=gameplane \
    WINEPREFIX=/home/gameplane/.wine \
    WINEDEBUG=-all \
    DISPLAY=:99 \
    GAME_PORT=10823 \
    WEB_PORT=8080

EXPOSE 10823/udp 8080/tcp

ENTRYPOINT ["/usr/bin/tini", "--", "/entrypoint.sh"]
