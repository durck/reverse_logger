# Main Server Firewall

This runbook describes the intended main-server firewall policy for the
`reverse_ssh` listener and the published central logger. It uses placeholders;
keep the real VPS inventory and operator addresses in private operational
state, not in the repository.

## Intended Access Matrix

| Surface | Typical host port | Allowed sources | Notes |
| --- | ---: | --- | --- |
| Main `reverse_ssh` listener | `REVERSE_SSH_BIND_PORT` (for example `3333`) | VPS edge egress addresses and explicitly approved operator addresses | Generated clients enter through VPS nginx; this is not a general public SSH port. |
| Central logger | `LOGGER_BIND_PORT` (normally `8080`) | VPS edge forwarders/health agents; operators only when direct dashboard access is required | Prefer an SSH tunnel for the dashboard. Never use this host-published address for the container-local lifecycle webhook. |
| Administrative SSH | deployment-specific | Fixed administrator/VPN addresses where possible | Keep the current SSH session open while changing rules and test from a second session before logout. |
| Docker-internal webhook | container `rssh-logger:8080` | `reverse_ssh` on the Compose network | No UFW rule or host port is required. Use Docker DNS, not the main server's public IP. |

With an IPv4 allowlist, explicitly deny the listener/logger ports for all
other IPv4 and IPv6 sources. A default-deny incoming policy is still
recommended; explicit per-port denies make the intended boundary visible in
`ufw status numbered`.

## Why UFW Alone Is Not the Whole Boundary

Docker-published ports may traverse Docker NAT and `FORWARD`/`DOCKER-USER`
rather than only the host `INPUT` chain. UFW rules are useful host policy, but
do not assume that an `INPUT` deny alone protects every Docker-published port.

The repository's `deploy/iptables/main-firewall.sh` attaches the same managed
guard to both `INPUT` and `DOCKER-USER` when Docker is present. Its final rule
is an interface-wide `DROP`, not a port-only drop. Use it only when
`INGRESS_IFACE` is a dedicated ingress interface on which all other new inbound
traffic should be denied. Do not point it at a shared public/management
interface carrying administrative SSH.

Do not add UFW rules for generated bridge names such as `br-<hash>`.
Those names can change when the Compose network is recreated. Services on the
same Compose network should use service DNS names and container ports; the
normal internal webhook path is `http://rssh-logger:8080`.

## Audit Before Changing Anything

Run from a privileged shell while retaining a separate working SSH session:

```sh
sudo ufw status verbose
sudo ufw status numbered
sudo ufw show added

sudo ss -lntp
sudo iptables -S INPUT
sudo iptables -S DOCKER-USER
sudo iptables -S RSSH_INGRESS_GUARD 2>/dev/null || true
sudo iptables -t nat -S DOCKER

docker compose \
  --env-file .env \
  -f docker-compose.yml \
  -f docker-compose.edge-forward.yml \
  ps
```

Confirm that the expected host bind addresses and ports match `.env`. A port
bound to `0.0.0.0` or a public address needs both an allowlist and the Docker
guard. Prefer a private/VPN bind address when all VPS edges can reach it.

## UFW Allowlist Pattern

Substitute the actual main bind address, ports, VPS edge addresses, and
operator address. Add allow rules *before* terminal deny rules. When a deny
already exists, a plain `ufw allow ...` appends after it and may never match;
`ufw insert 1 ...` is deliberately used below.

```sh
MAIN_IP=<main-bind-ip>
RSSH_PORT=<reverse-ssh-host-port>
LOGGER_PORT=<logger-host-port>

sudo ufw allow in on lo comment 'loopback'

sudo ufw insert 1 allow proto tcp \
  from <vps-edge-ip> to "$MAIN_IP" port "$RSSH_PORT" \
  comment 'reverse_ssh from VPS edge'
sudo ufw insert 1 allow proto tcp \
  from <vps-edge-ip> to "$MAIN_IP" port "$LOGGER_PORT" \
  comment 'logger ingest from VPS edge'

sudo ufw insert 1 allow proto tcp \
  from <operator-ip> to "$MAIN_IP" port "$RSSH_PORT" \
  comment 'reverse_ssh operator console'

sudo ufw deny proto tcp to "$MAIN_IP" port "$RSSH_PORT" \
  comment 'deny other reverse_ssh sources'
sudo ufw deny proto tcp to "$MAIN_IP" port "$LOGGER_PORT" \
  comment 'deny other logger sources'
```

Repeat the two VPS rules for every current edge. Do not grant operators direct
`8080` access unless needed; use an SSH tunnel instead:

```sh
ssh -L 18080:127.0.0.1:<logger-host-port> <admin>@<main-server>
```

Then open `http://127.0.0.1:18080/dashboard/` locally.

If the server has public IPv6, keep equivalent IPv6 allowlists or explicit
IPv6 denies. Do not leave IPv6 open merely because the deployment primarily
uses IPv4.

### Safely adding a new VPS later

Because terminal denies already exist, insert the new allows at the beginning:

```sh
sudo ufw insert 1 allow proto tcp \
  from <new-vps-ip> to <main-bind-ip> port <reverse-ssh-host-port> \
  comment 'reverse_ssh from new VPS edge'
sudo ufw insert 1 allow proto tcp \
  from <new-vps-ip> to <main-bind-ip> port <logger-host-port> \
  comment 'logger ingest from new VPS edge'
sudo ufw status numbered
```

Test both ports from the new VPS before deploying clients:

```sh
nc -vz <main-bind-ip> <reverse-ssh-host-port>
curl -fsS http://<main-bind-ip>:<logger-host-port>/healthz
```

### Removing stale or bridge-specific rules

Use `ufw status numbered`, delete by number in descending order, and refresh
the list after every deletion because UFW renumbers rules:

```sh
sudo ufw status numbered
sudo ufw delete <highest-rule-number>
sudo ufw status numbered
```

Bridge-name rules (`ALLOW IN ... on br-<hash>`) are normally unnecessary for
this stack and should be removed after verifying that the lifecycle webhook is
registered with `rssh-logger:8080`. Never remove the current administrative SSH
allow until a second login succeeds.

## Docker `DOCKER-USER` Guard

### Dedicated ingress interface

For the normal nginx-edge deployment, specify every VPS address in
`ALLOWED_SOURCE_IPS`. Do not use this allowlist for raw DNAT mode when the main
host sees arbitrary original client IPs; see `docs/softether-entrypoint.md`.

```sh
cd /opt/reverse-logger
sudo env \
  INGRESS_IFACE=<main-ingress-interface> \
  REVERSE_SSH_BIND_IP=<main-bind-ip> \
  REVERSE_SSH_PORT=<reverse-ssh-host-port> \
  LOGGER_BIND_IP=<main-bind-ip> \
  LOGGER_BIND_PORT=<logger-host-port> \
  ALLOWED_SOURCE_IPS='<vps-1-ip> <vps-2-ip> <vps-3-ip>' \
  sh deploy/iptables/main-firewall.sh
```

The script is idempotent for its managed chain: it flushes and rebuilds
`RSSH_INGRESS_GUARD`, then ensures jumps from `INPUT` and `DOCKER-USER`.
Persist it using the host's established firewall persistence mechanism only
after validation.

Because the chain ends in an unconditional `DROP`, first confirm that
`<main-ingress-interface>` is not also required for new SSH, monitoring, or
other unrelated connections.

Verify:

```sh
sudo iptables -S RSSH_INGRESS_GUARD
sudo iptables -S INPUT | grep RSSH_INGRESS_GUARD
sudo iptables -S DOCKER-USER | grep RSSH_INGRESS_GUARD
```

### Shared public or management interface

If Docker publishes `3333/8080` on the same public interface used by
administrative SSH, use a port-scoped `DOCKER-USER` chain instead of
`main-firewall.sh`. The following example returns all unrelated traffic to the
normal firewall path and drops only non-allowlisted connections whose original
Docker destination was one of the protected ports:

```sh
MAIN_IP=<main-bind-ip>
RSSH_PORT=<reverse-ssh-host-port>
LOGGER_PORT=<logger-host-port>
PUBLIC_IFACE=<shared-public-interface>

sudo iptables -N RSSH_DOCKER_PORT_GUARD 2>/dev/null || true
sudo iptables -F RSSH_DOCKER_PORT_GUARD
sudo iptables -A RSSH_DOCKER_PORT_GUARD \
  -m conntrack --ctstate ESTABLISHED,RELATED -j RETURN

for source_ip in <vps-1-ip> <vps-2-ip> <vps-3-ip>; do
  sudo iptables -A RSSH_DOCKER_PORT_GUARD -p tcp -s "$source_ip" \
    -m conntrack --ctorigdst "$MAIN_IP" --ctorigdstport "$RSSH_PORT" -j RETURN
  sudo iptables -A RSSH_DOCKER_PORT_GUARD -p tcp -s "$source_ip" \
    -m conntrack --ctorigdst "$MAIN_IP" --ctorigdstport "$LOGGER_PORT" -j RETURN
done

sudo iptables -A RSSH_DOCKER_PORT_GUARD -p tcp \
  -m conntrack --ctorigdst "$MAIN_IP" --ctorigdstport "$RSSH_PORT" -j DROP
sudo iptables -A RSSH_DOCKER_PORT_GUARD -p tcp \
  -m conntrack --ctorigdst "$MAIN_IP" --ctorigdstport "$LOGGER_PORT" -j DROP
sudo iptables -A RSSH_DOCKER_PORT_GUARD -j RETURN

sudo iptables -C DOCKER-USER -i "$PUBLIC_IFACE" \
  -j RSSH_DOCKER_PORT_GUARD 2>/dev/null || \
sudo iptables -I DOCKER-USER 1 -i "$PUBLIC_IFACE" \
  -j RSSH_DOCKER_PORT_GUARD
```

If an operator needs direct access to the catcher port, add the operator source
to the `RSSH_PORT` allow rules only; do not grant it logger access by default.
Keep the current SSH session open, test a second SSH login, and test allowed and
disallowed Docker-port sources before persisting these rules.

Verify counters as traffic is tested:

```sh
sudo iptables -L RSSH_DOCKER_PORT_GUARD -n -v --line-numbers
sudo iptables -L DOCKER-USER -n -v --line-numbers
```

From an allowed VPS, both listener and logger checks should succeed. From a
non-allowlisted host, both should fail. Container-local health must remain
available:

```sh
docker compose exec reverse_ssh sh -lc \
  'wget -qO- http://rssh-logger:8080/healthz || curl -fsS http://rssh-logger:8080/healthz'
```

## Review of a Typical Production Rule Set

A rule set with these characteristics follows the intended design:

- one operator-only allow for the `reverse_ssh` console port;
- matching `reverse_ssh` and logger allows for every active VPS edge;
- terminal IPv4 and IPv6 denies for both ports;
- loopback allowed;
- administrative SSH intentionally allowed or rate-limited;
- no public allow for `8080`;
- no dependency on a generated `br-<hash>` interface name.

For the supplied production snapshot, the per-VPS `3333` and `8080` allows
precede the terminal denies, so their current UFW ordering is correct. The
operator-only source on `3333` also follows least privilege. The items to
improve are:

- remove the two generated Docker bridge-interface allows after the private
  webhook has been verified; they are brittle and unnecessary for service-DNS
  traffic;
- verify from a non-allowlisted external host that Docker-published `3333` and
  `8080` are actually blocked, then install the appropriate `DOCKER-USER` guard
  if they are not;
- restrict the administrative SSH port currently allowed from anywhere to
  fixed operator/VPN sources where practical, or at minimum apply UFW rate
  limiting and key-only SSH authentication;
- keep every future VPS allow before the terminal denies by using
  `ufw insert 1`, not a plain appended `ufw allow`;
- confirm `ufw status verbose` reports default incoming deny and that IPv6 has
  an equivalent policy.

When replacing an unrestricted administrative SSH allow with a restricted or
rate-limited rule, do not append the new rule behind the old allow. Keep the
current session open, insert and test the replacement first, then delete the
old rule by its current number.

Check for drift monthly and whenever a VPS is added, removed, or changes its
egress address. Remove both port rules for a retired VPS and remove it from
`ALLOWED_SOURCE_IPS` in the same maintenance window.
