# SoftEther VPS Entrypoint

Each VPS is an untrusted public entrypoint. It connects to the existing
SoftEther server and forwards public `443/tcp` to the private `reverse_ssh`
listener reachable over that VPN.

## Target Topology

```text
client -> VPS public IP:443 -> SoftEther VPN -> reverse_ssh bind IP:3232
```

Recommended default:

- kernel DNAT on the VPS;
- no `edge-logger` in the data path;
- no SNAT unless the target cannot route replies back through the VPS.

This keeps throughput in the kernel forwarding path and avoids adding a
user-space TCP proxy as a mandatory bottleneck.

## Main Server

The main server must run `reverse_ssh` on a private/internal address reachable
from the VPS through the SoftEther path, for example:

```text
REVERSE_SSH_BIND_IP=192.0.2.10
REVERSE_SSH_BIND_PORT=3232
```

The main server does not need its own SoftEther interface in this topology. Use
`deploy/iptables/main-firewall.sh` only as an optional main-server guard after
reviewing the actual internal ingress interface where VPS-routed traffic
arrives. In DNAT-only mode, the main server sees real internet client source
addresses, so do not restrict this guard to VPS source IPs unless SNAT is
enabled.

## VPS DNAT

Use `deploy/iptables/vps-forward.sh` on each VPS after SoftEther is connected
and the VPS can reach `RSSH_TARGET_IP:RSSH_TARGET_PORT`.

SoftEther installation and account provisioning are handled outside this
repository. After the existing automation completes, verify the VPS interface,
routes, and target reachability:

```sh
ip -br addr
ip route
nc -vz 192.0.2.10 3232
```

```sh
sudo env \
  PUBLIC_IFACE=eth0 \
  VPN_IFACE=vpn_softether \
  PUBLIC_PORT=443 \
  RSSH_TARGET_IP=192.0.2.10 \
  RSSH_TARGET_PORT=3232 \
  sh deploy/iptables/vps-forward.sh
```

The script manages dedicated `RSSH_VPS_*` chains and flushes those chains on
each run, so changing `PUBLIC_PORT`, `RSSH_TARGET_IP`, or `RSSH_TARGET_PORT`
does not leave stale managed forwarding rules active.

## SNAT Option

Leave `SNAT_SOURCE_IP` unset by default. This gives `reverse_ssh` the best
chance to see the real client IP in webhook payloads.

Set `SNAT_SOURCE_IP` only when the target cannot route replies back through
the VPS:

```sh
sudo env \
  PUBLIC_IFACE=eth0 \
  VPN_IFACE=vpn_softether \
  PUBLIC_PORT=443 \
  RSSH_TARGET_IP=192.0.2.10 \
  RSSH_TARGET_PORT=3232 \
  SNAT_SOURCE_IP=192.0.2.2 \
  sh deploy/iptables/vps-forward.sh
```

With SNAT enabled, `reverse_ssh` will see the VPS/VPN source address instead
of the real client IP.

## Optional Edge Logging

`edge-logger` remains available for cases where VPS-side connection logs are
required, but it is not the recommended default forwarding path. It runs as a
user-space TCP proxy, so it adds another service, another copy path, and
another failure point.

Before enabling it, first smoke-test the webhook `ip_raw` field with plain
DNAT. If DNAT preserves the real client IP, keep the simpler kernel path.

## Revoke a VPS

On the main server:

1. Remove or disable the VPS SoftEther account/session.
2. Remove the matching firewall allow rule or rerun the ingress guard script
   without that source if source allowlists are used.
3. Rotate any proxy or edge-forward credentials used by that VPS.
