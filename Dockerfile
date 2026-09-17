# ModelScope studio wrapper for Octopus.
#
# Two things happen here that the upstream image does not do:
#   1. strip-proxy is compiled and installed next to Octopus.
#   2. The startup script runs Octopus on loopback and strip-proxy on the public
#      port, rewriting the `X-Frame-Options` / CSP `frame-ancestors` headers that
#      otherwise stop ModelScope from embedding the UI in its studio iframe.
#
# Override OCTOPUS_IMAGE when rebuilding the wrapper for a newer published
# release; keep the default pinned for reproducible ModelScope builds.
ARG OCTOPUS_IMAGE=ghcr.io/u188/octopus:v4.0.8
ARG GO_IMAGE=golang:1.25-alpine

# ---------------------------------------------------------------------------
# Stage 1 — build the header-rewriting proxy (stdlib only, no modules needed)
# ---------------------------------------------------------------------------
FROM ${GO_IMAGE} AS proxy-build

WORKDIR /src
COPY scripts/ms-proxy/go.mod ./
COPY scripts/ms-proxy/main.go ./

# CGO_ENABLED=0 keeps the binary fully static so it runs on both the musl
# (alpine) and glibc (debian) flavours of the Octopus image.
ENV CGO_ENABLED=0 GOOS=linux
RUN go build -trimpath -ldflags="-s -w" -o /out/strip-proxy .

# ---------------------------------------------------------------------------
# Stage 2 — runtime
# ---------------------------------------------------------------------------
FROM ${OCTOPUS_IMAGE}

LABEL org.opencontainers.image.title="Octopus" \
      org.opencontainers.image.description="Octopus ModelScope wrapper (iframe-embeddable)" \
      org.opencontainers.image.source="https://github.com/U188/octopus"

ENV TZ=Asia/Shanghai \
    OCTOPUS_SERVER_HOST=127.0.0.1 \
    OCTOPUS_SERVER_PORT=8080 \
    OCTOPUS_INTERNAL_HOST=127.0.0.1 \
    OCTOPUS_INTERNAL_PORT=8080 \
    OCTOPUS_DATABASE_TYPE=sqlite \
    OCTOPUS_DATABASE_PATH=/mnt/workspace/octopus-v5.db \
    UPSTREAM=http://127.0.0.1:8080 \
    LISTEN=:7860 \
    FRAME_ANCESTORS="https://www.modelscope.ai https://*.modelscope.ai https://*.modelscope.cn"

USER root

# Keep the application and bootstrap credential paths available even when the
# platform has not mounted its persistent workspace yet.
RUN mkdir -p /app /mnt/workspace

COPY --from=proxy-build /out/strip-proxy /usr/local/bin/strip-proxy
COPY scripts/ms-proxy/start-ms.sh /usr/local/bin/start-ms.sh
RUN chmod +x /usr/local/bin/strip-proxy /usr/local/bin/start-ms.sh

WORKDIR /app

# Make the intended graceful-shutdown signal explicit for the PID-1 wrapper.
STOPSIGNAL SIGTERM

EXPOSE 7860

CMD ["/usr/local/bin/start-ms.sh"]
