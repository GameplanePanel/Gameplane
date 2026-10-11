FROM alpine:3.20

LABEL org.opencontainers.image.title="Gameplane BeamMP Dedicated Server" \
      org.opencontainers.image.description="Dedicated server container for BeamNG.drive (BeamMP) with diagnostic idle support for missing auth keys" \
      org.opencontainers.image.authors="Gameplane Maintainers" \
      org.opencontainers.image.source="https://github.com/GameplanePanel/Gameplane"

RUN apk add --no-cache \
    bash \
    curl \
    ca-certificates \
    tini \
    procps \
    libstdc++ \
    libgcc \
    openssl \
    zlib

# Create gameplane runtime user with fixed UID/GID 1000
RUN addgroup -g 1000 gameplane && \
    adduser -u 1000 -G gameplane -h /home/gameplane -s /bin/bash -D gameplane

# Prepare directories
RUN mkdir -p /serverdata /opt/beammp /server && \
    ln -s /serverdata /server/Root && \
    chown -R gameplane:gameplane /serverdata /opt/beammp /home/gameplane /server

COPY --chown=gameplane:gameplane entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

USER 1000:1000
WORKDIR /serverdata
ENV HOME=/home/gameplane \
    USER=gameplane \
    PORT=30814

EXPOSE 30814/udp 30814/tcp

ENTRYPOINT ["/sbin/tini", "--", "/entrypoint.sh"]
