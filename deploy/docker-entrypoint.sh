#!/bin/sh
# Starts vCLU as the user vclu. Started as root, it first hands the data directory to vclu
# (a volume from an image before v1.6 was written by root) and lets vclu use a GPIO device
# passed in, then drops root. Started with --user, it runs vCLU as that user unchanged.
# A step that cannot be done is reported and never stops the start.
set -e

home=/opt/vclu
data=./data
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
# as vCLU resolves it: relative to the working directory, empty is ./clu
[ -n "$data" ] || data=./clu
case "$data" in
    /*) ;;
    *) data="$home/${data#./}" ;;
esac
while [ "${data%/}" != "$data" ]; do data="${data%/}"; done

# HEALTHCHECK asks this port (8080 when the file cannot be written)
{ echo "$port" > /tmp/vclu-web-port; } 2>/dev/null || true

# the file capability for port 69 needs NET_BIND_SERVICE in the bounding set (bit 10)
bnd=$(awk '/^CapBnd:/ { print $2 }' /proc/self/status)
if [ -n "$bnd" ] && [ $(( 0x$bnd >> 10 & 1 )) = 0 ]; then
    echo "docker-entrypoint: NET_BIND_SERVICE was dropped, vCLU cannot start: add --cap-add NET_BIND_SERVICE" >&2
fi

if [ "$(id -u)" != "0" ]; then
    exec "$home/vclu" "$@"
fi

case "$data" in
    ""|/opt|"$home"|"$home"/vclu|"$home"/runtime*|"$home"/web*|"$home"/docs*)
        echo "docker-entrypoint: not giving ${data:-/} to vclu, use a directory of its own" >&2 ;;
    *)
        mkdir -p "$data" 2>/dev/null || true
        if [ -n "$(find "$data" ! -user vclu -print -quit 2>/dev/null)" ]; then
            echo "docker-entrypoint: giving $data to vclu (uid $(id -u vclu))"
            chown -R -h vclu:vclu "$data" ||
                echo "docker-entrypoint: could not give $data to vclu, vCLU may not write to it" >&2
        fi ;;
esac

# su-exec sets the groups from /etc/group, so a GPIO device's group must be there
if [ -e /dev/gpiomem ]; then
    gid=$(stat -c %g /dev/gpiomem)
    # a device passed with --device sits in the container's own /dev (tmpfs); a host /dev is left alone
    devfs=$(awk '$2 == "/dev" { print $3; exit }' /proc/mounts)
    if [ "$gid" = "0" ] || [ -z "$(find /dev/gpiomem -perm -g+rw 2>/dev/null)" ]; then
        if [ "$devfs" = "tmpfs" ] && chgrp vclu /dev/gpiomem 2>/dev/null && chmod g+rw /dev/gpiomem 2>/dev/null; then
            gid=$(stat -c %g /dev/gpiomem)
        else
            echo "docker-entrypoint: vclu cannot open /dev/gpiomem (group $gid, /dev on $devfs)" >&2
        fi
    fi
    group=$(awk -F: -v g="$gid" '$3 == g { print $1; exit }' /etc/group)
    if [ -z "$group" ] && addgroup -g "$gid" "gpio$gid" 2>/dev/null; then
        group=gpio$gid
    fi
    if [ -n "$group" ] && [ "$group" != "vclu" ]; then
        addgroup vclu "$group" 2>/dev/null ||
            awk -F: -v g="$group" '$1 == g && ("," $4 ",") ~ /,vclu,/ { f = 1 } END { exit !f }' /etc/group ||
            echo "docker-entrypoint: could not add vclu to group $group" >&2
    fi
fi

exec su-exec vclu "$home/vclu" "$@"
