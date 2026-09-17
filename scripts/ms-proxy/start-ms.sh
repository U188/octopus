#!/bin/sh
# ModelScope startup wrapper.
#
# Layout:
#   strip-proxy  -> 0.0.0.0:7860  (public; ModelScope proxies this port)
#   octopus      -> 127.0.0.1:8080 (internal only)
#
# Why the extra hop: Octopus hardcodes `X-Frame-Options: DENY` and
# `frame-ancestors 'none'`, which makes browsers refuse to render it inside the
# ModelScope studio iframe. strip-proxy rewrites those two headers on the way
# out. Binding Octopus to loopback also guarantees every request takes that path.
set -e

# --- 1) Octopus on the internal port -----------------------------------------
export OCTOPUS_SERVER_HOST="${OCTOPUS_INTERNAL_HOST:-127.0.0.1}"
export OCTOPUS_SERVER_PORT="${OCTOPUS_INTERNAL_PORT:-8080}"

/entrypoint.sh &
octopus_pid=$!

# --- 2) Header-rewriting proxy on the public port -----------------------------
strip-proxy &
proxy_pid=$!

# --- 3) Forward shutdown so the platform can stop us cleanly ------------------
trap 'kill -TERM "$proxy_pid" "$octopus_pid" 2>/dev/null || true; wait; exit 0' TERM INT

# --- 4) Surface the one-time admin credential in the studio log ---------------
i=0
while [ "$i" -lt 90 ]; do
    if [ -s /mnt/workspace/initial-admin-password.txt ]; then
        echo '========== OCTOPUS INITIAL LOGIN =========='
        echo 'Username: admin'
        printf 'Password: '
        cat /mnt/workspace/initial-admin-password.txt
        echo '==========================================='
        break
    fi
    kill -0 "$octopus_pid" 2>/dev/null || break
    i=$((i + 1))
    sleep 1
done

wait "$proxy_pid"
