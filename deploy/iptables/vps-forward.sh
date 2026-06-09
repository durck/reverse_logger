#!/usr/bin/env sh
set -eu

# SoftEther-neutral DNAT entrypoint for a public VPS.
# Default path keeps real client IP visible to reverse_ssh when routing allows it:
#
#   client -> VPS_PUBLIC_IP:443 -> SoftEther VPN -> RSSH_TARGET_IP:RSSH_TARGET_PORT
#
# Set SNAT_SOURCE_IP only when the target cannot route replies back through the
# VPS. With SNAT enabled, reverse_ssh will see the VPS/VPN source address.

: "${PUBLIC_IFACE:?set PUBLIC_IFACE, for example eth0}"
: "${VPN_IFACE:?set VPN_IFACE, for example vpn_softether or tap_softether}"
: "${RSSH_TARGET_IP:?set RSSH_TARGET_IP, for example 192.0.2.10}"
: "${RSSH_TARGET_PORT:?set RSSH_TARGET_PORT, for example 3232}"

PUBLIC_PORT="${PUBLIC_PORT:-${REVERSE_SSH_PUBLIC_PORT:-443}}"
NAT_PREROUTING_CHAIN="${NAT_PREROUTING_CHAIN:-RSSH_VPS_PREROUTING}"
NAT_POSTROUTING_CHAIN="${NAT_POSTROUTING_CHAIN:-RSSH_VPS_POSTROUTING}"
FILTER_FORWARD_CHAIN="${FILTER_FORWARD_CHAIN:-RSSH_VPS_FORWARD}"

command -v iptables >/dev/null

sysctl -w net.ipv4.ip_forward=1

ensure_chain() {
  table="$1"
  chain="$2"
  if ! iptables -t "$table" -L "$chain" -n >/dev/null 2>&1; then
    iptables -t "$table" -N "$chain"
  fi
  iptables -t "$table" -F "$chain"
}

ensure_jump() {
  table="$1"
  base_chain="$2"
  managed_chain="$3"
  if ! iptables -t "$table" -C "$base_chain" -j "$managed_chain" >/dev/null 2>&1; then
    iptables -t "$table" -I "$base_chain" 1 -j "$managed_chain"
  fi
}

ensure_chain nat "$NAT_PREROUTING_CHAIN"
ensure_chain nat "$NAT_POSTROUTING_CHAIN"
ensure_chain filter "$FILTER_FORWARD_CHAIN"

ensure_jump nat PREROUTING "$NAT_PREROUTING_CHAIN"
ensure_jump nat POSTROUTING "$NAT_POSTROUTING_CHAIN"
ensure_jump filter FORWARD "$FILTER_FORWARD_CHAIN"

iptables -t nat -A "$NAT_PREROUTING_CHAIN" -i "$PUBLIC_IFACE" -p tcp --dport "$PUBLIC_PORT" \
  -j DNAT --to-destination "$RSSH_TARGET_IP:$RSSH_TARGET_PORT"
iptables -t nat -A "$NAT_PREROUTING_CHAIN" -j RETURN

if [ "${SNAT_SOURCE_IP:-}" ]; then
  iptables -t nat -A "$NAT_POSTROUTING_CHAIN" -o "$VPN_IFACE" -p tcp -d "$RSSH_TARGET_IP" --dport "$RSSH_TARGET_PORT" \
    -j SNAT --to-source "$SNAT_SOURCE_IP"
fi
iptables -t nat -A "$NAT_POSTROUTING_CHAIN" -j RETURN

iptables -t filter -A "$FILTER_FORWARD_CHAIN" -i "$PUBLIC_IFACE" -o "$VPN_IFACE" -p tcp -d "$RSSH_TARGET_IP" --dport "$RSSH_TARGET_PORT" \
  -m conntrack --ctstate NEW,ESTABLISHED,RELATED -j ACCEPT

iptables -t filter -A "$FILTER_FORWARD_CHAIN" -i "$VPN_IFACE" -o "$PUBLIC_IFACE" -p tcp -s "$RSSH_TARGET_IP" --sport "$RSSH_TARGET_PORT" \
  -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
iptables -t filter -A "$FILTER_FORWARD_CHAIN" -j RETURN
