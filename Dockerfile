# Override OCTOPUS_IMAGE when rebuilding the wrapper for a newer published
# release; keep the default pinned for reproducible ModelScope builds.
ARG OCTOPUS_IMAGE=ghcr.io/u188/octopus:v0.3.53
FROM ${OCTOPUS_IMAGE}

LABEL org.opencontainers.image.title="Octopus" \
      org.opencontainers.image.description="Octopus ModelScope wrapper" \
      org.opencontainers.image.source="https://github.com/U188/octopus"

ENV TZ=Asia/Shanghai \
    OCTOPUS_SERVER_HOST=0.0.0.0 \
    OCTOPUS_SERVER_PORT=7860 \
    OCTOPUS_DATABASE_TYPE=sqlite \
    OCTOPUS_DATABASE_PATH=/mnt/workspace/octopus-v5.db

USER root

# Keep the application and bootstrap credential paths available even when the
# platform has not mounted its persistent workspace yet.
RUN mkdir -p /app /mnt/workspace
WORKDIR /app

# Make the intended graceful-shutdown signal explicit for the PID-1 wrapper.
STOPSIGNAL SIGTERM

EXPOSE 7860

# Print the generated one-time password to the ModelScope startup log.
CMD ["/bin/sh", "-c", "unset OCTOPUS_INITIAL_ADMIN_USERNAME OCTOPUS_INITIAL_ADMIN_PASSWORD; /entrypoint.sh & pid=$!; trap 'kill -TERM \"$pid\" 2>/dev/null || true; wait \"$pid\"; exit $?' TERM INT; i=0; while [ \"$i\" -lt 60 ]; do if [ -s /mnt/workspace/initial-admin-password.txt ]; then echo '========== OCTOPUS INITIAL LOGIN =========='; echo 'Username: admin'; printf 'Password: '; cat /mnt/workspace/initial-admin-password.txt; echo '==========================================='; break; fi; kill -0 \"$pid\" 2>/dev/null || break; i=$((i + 1)); sleep 1; done; wait \"$pid\""]
