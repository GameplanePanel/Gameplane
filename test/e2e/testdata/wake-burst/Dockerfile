FROM debian:bookworm-20261027-slim@sha256:99c0b25c46d0214d5a2091092e7908907dcaecd6a962cde0ac700acee222b6f3
# Keep dependencies reproducible after bookworm receives another security
# update. APT still verifies each archived Release and its package hashes.
RUN rm /etc/apt/sources.list.d/debian.sources \
    && printf '%s\n' \
    'deb [check-valid-until=no] http://snapshot.debian.org/archive/debian/20261102T000000Z/ bookworm main' \
    'deb [check-valid-until=no] http://snapshot.debian.org/archive/debian-security/20261102T000000Z/ bookworm-security main' \
    >/etc/apt/sources.list \
    && apt-get update && apt-get install -y --no-install-recommends \
    teeworlds=0.7.5-2 teeworlds-server=0.7.5-2 xvfb=2:21.1.7-3+deb12u13 \
    xauth=1:1.1.2-1 libgl1-mesa-dri=22.3.6-1+deb12u2 python3=3.11.2-1+b1 \
    && rm -rf /var/lib/apt/lists/*
COPY teeworlds-join.py /opt/teeworlds-join.py
ENV HOME=/tmp
ENTRYPOINT ["sh","-ec","sleep 12; exec /usr/games/teeworlds-server 'sv_register 0' 'sv_map dm1' 'sv_port 8303' 'sv_max_clients_per_ip 8'"]
