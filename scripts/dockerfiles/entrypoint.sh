#!/bin/sh
set -e

PUID="${PUID:-0}"
PGID="${PGID:-0}"

chmod +x /app/octopus

if [ "$PUID" != "0" ] || [ "$PGID" != "0" ]; then
    chown -R "$PUID:$PGID" /app
fi

cd /app

if command -v su-exec >/dev/null 2>&1; then
    exec su-exec "$PUID:$PGID" ./octopus start
elif command -v gosu >/dev/null 2>&1; then
    exec gosu "$PUID:$PGID" ./octopus start
else
    if [ "$PUID" != "0" ] || [ "$PGID" != "0" ]; then
        echo "Warning: neither su-exec nor gosu is available; running as root." >&2
    fi
    exec ./octopus start
fi
