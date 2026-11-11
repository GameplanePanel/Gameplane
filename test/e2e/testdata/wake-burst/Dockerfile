FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
    teeworlds=0.7.5-2 teeworlds-server=0.7.5-2 xvfb xauth libgl1-mesa-dri python3 \
    && rm -rf /var/lib/apt/lists/*
COPY teeworlds-join.py /opt/teeworlds-join.py
ENV HOME=/tmp
ENTRYPOINT ["sh","-ec","sleep 12; exec /usr/games/teeworlds-server 'sv_register 0' 'sv_map dm1' 'sv_port 8303' 'sv_max_clients_per_ip 8'"]
