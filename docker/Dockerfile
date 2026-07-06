# Single image for nft; the container's command decides the role.
# Build stage produces a static binary; runtime stage installs the
# minimal userland the daemon and panel need.
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/nft ./cmd/nft

FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends nftables iproute2 ca-certificates curl python3 sqlite3 procps \
 && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/nft /usr/local/sbin/nft
COPY install.sh /usr/local/sbin/nft-install
RUN chmod +x /usr/local/sbin/nft-install
# No ENTRYPOINT — docker-compose.yml supplies the per-service command so the
# same image serves daemon, server, or agent roles.
