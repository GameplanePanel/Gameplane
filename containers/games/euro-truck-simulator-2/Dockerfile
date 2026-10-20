FROM debian:bookworm-slim

LABEL org.opencontainers.image.title="Gameplane Euro Truck Simulator 2 Dedicated Server" \
      org.opencontainers.image.description="Dedicated server container for Euro Truck Simulator 2 with diagnostic idle support for missing logon tokens" \
      org.opencontainers.image.authors="Gameplane Maintainers" \
      org.opencontainers.image.source="https://github.com/GameplanePanel/Gameplane"

ENV DEBIAN_FRONTEND=noninteractive

RUN dpkg --add-architecture i386 && \
    apt-get update && \
    apt-get install -y --no-install-recommends \
        bash=5.2.15-2+b13 \
        curl=7.88.1-10+deb12u15 \
        ca-certificates=20250419~deb12u1 \
        tini=0.19.0-1+b3 \
        procps=2:4.0.2-3 \
        lib32gcc-s1=12.2.0-14+deb12u1 \
        libstdc++6=12.2.0-14+deb12u1 \
        libstdc++6:i386=12.2.0-14+deb12u1 \
        locales=2.36-9+deb12u14 && \
    sed -i -e 's/# en_US.UTF-8 UTF-8/en_US.UTF-8 UTF-8/' /etc/locale.gen && \
    locale-gen && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*

# Create gameplane runtime user with fixed UID/GID 1000
RUN groupadd -g 1000 gameplane && \
    useradd -u 1000 -g gameplane -m -s /bin/bash gameplane

# Prepare directories
RUN mkdir -p /serverdata /home/gameplane/.local/share/Euro\ Truck\ Simulator\ 2 && \
    ln -s /home/gameplane /home/steam && \
    chown -R gameplane:gameplane /serverdata /home/gameplane

COPY --chown=gameplane:gameplane entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

USER 1000:1000
WORKDIR /serverdata
ENV HOME=/home/gameplane \
    USER=gameplane \
    LANG=en_US.UTF-8 \
    GAME_PORT=27015 \
    QUERY_PORT=27016

EXPOSE 27015/udp 27016/udp

ENTRYPOINT ["/usr/bin/tini", "--", "/entrypoint.sh"]
