FROM alpine:3.20

LABEL org.opencontainers.image.title="Gameplane FiveM Dedicated Server" \
      org.opencontainers.image.description="Dedicated server container for FiveM (GTA V MP) with txAdmin and embedded database supervision" \
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
    icu-libs \
    mariadb \
    mariadb-client \
    sqlite \
    tzdata

# Create gameplane runtime user with fixed UID/GID 1000
RUN addgroup -g 1000 gameplane && \
    adduser -u 1000 -G gameplane -h /home/gameplane -s /bin/bash -D gameplane

# Prepare directories
RUN mkdir -p /opt/cfx-server /server-data && \
    chown -R gameplane:gameplane /opt/cfx-server /server-data /home/gameplane

# Copy entrypoint script
COPY --chown=gameplane:gameplane entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

USER 1000:1000
WORKDIR /server-data
ENV HOME=/home/gameplane \
    USER=gameplane \
    PORT=30120 \
    TXADMIN_PORT=40120

EXPOSE 30120/tcp 30120/udp 40120/tcp

ENTRYPOINT ["/sbin/tini", "--", "/entrypoint.sh"]
