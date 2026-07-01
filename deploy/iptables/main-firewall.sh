#!/usr/bin/env sh
set -eu

# Main-server ingress guard for traffic arriving from the internal network path
# used by SoftEther entrypoints.
# Run manually after reviewing values and making sure you have console access.
#
# Docker-published ports are filtered in FORWARD, not only INPUT. This script
# installs one guard chain and attaches it to both INPUT and DOCKER-USER when
# Docker is present. Logger/dashboard rules use LOGGER_BIND_IP when it is set;
# otherwise they fall back to REVERSE_SSH_BIND_IP.

: "${INGRESS_IFACE:?set INGRESS_IFACE, for example eth0 or ens18}"
: "${REVERSE_SSH_BIND_IP:?set REVERSE_SSH_BIND_IP, for example 192.0.2.10}"
: "${REVERSE_SSH_PORT:?set REVERSE_SSH_PORT, for example 3232}"

GUARD_CHAIN="${GUARD_CHAIN:-RSSH_INGRESS_GUARD}"
LOGGER_PORT="${LOGGER_BIND_PORT:-}"
LOGGER_IP="${LOGGER_BIND_IP:-$REVERSE_SSH_BIND_IP}"
ALLOWED_SOURCES="${ALLOWED_SOURCE_IPS:-}"

command -v iptables >/dev/null

if ! iptables -L "$GUARD_CHAIN" -n >/dev/null 2>&1; then
  iptables -N "$GUARD_CHAIN"
fi
iptables -F "$GUARD_CHAIN"

iptables -A "$GUARD_CHAIN" -m conntrack --ctstate ESTABLISHED,RELATED -j RETURN

allow_target_from_source() {
  source_ip="$1"
  if [ "$source_ip" ]; then
    iptables -A "$GUARD_CHAIN" -p tcp -s "$source_ip" -d "$REVERSE_SSH_BIND_IP" --dport "$REVERSE_SSH_PORT" -j RETURN
    iptables -A "$GUARD_CHAIN" -p tcp -s "$source_ip" -m conntrack --ctorigdst "$REVERSE_SSH_BIND_IP" --ctorigdstport "$REVERSE_SSH_PORT" -j RETURN
  else
    iptables -A "$GUARD_CHAIN" -p tcp -d "$REVERSE_SSH_BIND_IP" --dport "$REVERSE_SSH_PORT" -j RETURN
    iptables -A "$GUARD_CHAIN" -p tcp -m conntrack --ctorigdst "$REVERSE_SSH_BIND_IP" --ctorigdstport "$REVERSE_SSH_PORT" -j RETURN
  fi
  if [ "$LOGGER_PORT" ]; then
    if [ "$source_ip" ]; then
      iptables -A "$GUARD_CHAIN" -p tcp -s "$source_ip" -d "$LOGGER_IP" --dport "$LOGGER_PORT" -j RETURN
      iptables -A "$GUARD_CHAIN" -p tcp -s "$source_ip" -m conntrack --ctorigdst "$LOGGER_IP" --ctorigdstport "$LOGGER_PORT" -j RETURN
    else
      iptables -A "$GUARD_CHAIN" -p tcp -d "$LOGGER_IP" --dport "$LOGGER_PORT" -j RETURN
      iptables -A "$GUARD_CHAIN" -p tcp -m conntrack --ctorigdst "$LOGGER_IP" --ctorigdstport "$LOGGER_PORT" -j RETURN
    fi
  fi
}

if [ "$ALLOWED_SOURCES" ]; then
  for source_ip in $ALLOWED_SOURCES; do
    allow_target_from_source "$source_ip"
  done
else
  allow_target_from_source ""
fi

iptables -A "$GUARD_CHAIN" -j DROP

ensure_jump() {
  chain="$1"
  if iptables -C "$chain" -i "$INGRESS_IFACE" -j "$GUARD_CHAIN" >/dev/null 2>&1; then
    return
  fi
  iptables -I "$chain" 1 -i "$INGRESS_IFACE" -j "$GUARD_CHAIN"
}

ensure_jump INPUT
if iptables -L DOCKER-USER -n >/dev/null 2>&1; then
  ensure_jump DOCKER-USER
else
  echo "warning: DOCKER-USER chain not found; Docker-published ports are not guarded by this run" >&2
fi
