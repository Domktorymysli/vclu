#!/bin/sh
# Starts vCLU as the user vclu. Started as root, it first hands the data directory to vclu
# (a volume from an image before v1.6 was written by root) and lets vclu use a GPIO device
# passed in, then drops root. Started with --user, it runs vCLU as that user unchanged.
set -e

data=/data
port=8080
prev=
for arg in "$@"; do
    case "$arg" in
        --data-dir=*|-data-dir=*) data="${arg#*=}" ;;
        --web-port=*|-web-port=*) port="${arg#*=}" ;;
    esac
    case "$prev" in
        --data-dir|-data-dir) data="$arg" ;;
        --web-port|-web-port) port="$arg" ;;
    esac
    prev="$arg"
done
# HEALTHCHECK asks this port
echo "$port" > /tmp/vclu-web-port

if [ "$(id -u)" != "0" ]; then
    exec /opt/vclu/vclu "$@"
fi

mkdir -p "$data"
if [ -n "$(find "$data" ! -user vclu -print -quit)" ]; then
    echo "docker-entrypoint: giving $data to vclu (uid $(id -u vclu))"
    chown -R vclu:vclu "$data"
fi

# su-exec sets the groups from /etc/group, so a GPIO device's group must be there
if [ -e /dev/gpiomem ]; then
    gid=$(stat -c %g /dev/gpiomem)
    if [ "$gid" = "0" ]; then
        echo "docker-entrypoint: /dev/gpiomem belongs to group root, vclu cannot open it"
    else
        group=$(awk -F: -v g="$gid" '$3 == g { print $1; exit }' /etc/group)
        if [ -z "$group" ]; then
            group=gpio$gid
            addgroup -g "$gid" "$group"
        fi
        addgroup vclu "$group" 2>/dev/null || true
    fi
fi

exec su-exec vclu /opt/vclu/vclu "$@"
