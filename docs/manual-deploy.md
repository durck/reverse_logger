# Manual Deployment from a Clean Ubuntu Image

This runbook assumes fresh Ubuntu 22.04 LTS or 24.04 LTS servers.

Topology:

```text
external client -> VPS public IP:443 -> VPS SoftEther interface -> main internal IP:3232 -> reverse_ssh
```

The main server does not need a SoftEther interface. It only needs a
private/internal address reachable from the VPS through the existing SoftEther
network path. SoftEther installation and account provisioning are intentionally
out of scope because they are handled by deployment automation.

Replace all example addresses, tokens, image names, and interface names before
applying commands.

## 1. Main Server Base Bootstrap

Run as a sudo-capable user on a clean main Ubuntu server:

```sh
sudo apt-get update
sudo apt-get upgrade -y
sudo apt-get install -y ca-certificates curl git gnupg openssh-client openssl sqlite3
```

Install Docker Engine and the Compose plugin from Docker's official apt
repository:

```sh
sudo install -m 0755 -d /etc/apt/keyrings
sudo curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
sudo chmod a+r /etc/apt/keyrings/docker.asc

sudo tee /etc/apt/sources.list.d/docker.sources >/dev/null <<EOF
Types: deb
URIs: https://download.docker.com/linux/ubuntu
Suites: $(. /etc/os-release && echo "${UBUNTU_CODENAME:-$VERSION_CODENAME}")
Components: stable
Architectures: $(dpkg --print-architecture)
Signed-By: /etc/apt/keyrings/docker.asc
EOF

sudo apt-get update
sudo apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
sudo systemctl enable --now docker
sudo docker run --rm hello-world
```

Optional, if the deploy user should run Docker without `sudo`:

```sh
sudo usermod -aG docker "$USER"
newgrp docker
docker compose version
```

## 2. Clone the Repository

```sh
sudo mkdir -p /opt/reverse-logger
sudo chown "$USER:$USER" /opt/reverse-logger
git clone https://github.com/durck/reverse_logger.git /opt/reverse-logger
```

For SSH-based deployment, use:

```sh
git clone git@github.com:durck/reverse_logger.git /opt/reverse-logger
```

## 3. Prepare Main Server Data and Env

```sh
sudo mkdir -p /opt/reverse-logger/data/reverse_ssh
sudo mkdir -p /opt/reverse-logger/data/logger
sudo chown -R "$USER:$USER" /opt/reverse-logger/data

cd /opt/reverse-logger
cp .env.example .env
chmod 600 .env
```

Generate deployment tokens:

```sh
openssl rand -hex 32
openssl rand -hex 32
```

Edit `.env`:

```sh
nano .env
```

Set at minimum:

- `REVERSE_SSH_BIND_IP`: private/internal main-server address reachable from
  the VPS.
- `REVERSE_SSH_BIND_PORT`: internal target port, normally `3232`.
- `REVERSE_SSH_EXTERNAL_ADDRESS`: public VPS entrypoint, for example
  `vps-entrypoint.example.com:443`.
- `REVERSE_SSH_PUBLIC_PORT`: public VPS entrypoint port; keep it aligned with
  the VPS DNAT `PUBLIC_PORT`.
- `REVERSE_SSH_IMAGE`: your custom `reverse_ssh` image.
- `SEED_AUTHORIZED_KEYS`: public key contents, not a file path.
- `WEBHOOK_TOKEN`: first generated token.
- `EDGE_FORWARD_TOKEN`: second generated token if optional edge forwarding is
  used.
- `TELEGRAM_*` settings and `TELEGRAM_PROXY_URL` if alerts are enabled.

## 4. Start the Main Stack

```sh
cd /opt/reverse-logger
docker compose pull || true
docker compose build rssh-logger
docker compose up -d
docker compose ps
```

The `reverse_ssh` listener must be bound only to the internal address, for
example:

```text
192.0.2.10:3232 -> reverse_ssh:2222
```

Do not bind it to `0.0.0.0` unless you explicitly want direct public exposure
from the main server.

## 5. Register reverse_ssh Webhook

Enter the `reverse_ssh` server console using your normal workflow and register:

```text
webhook --on http://rssh-logger:8080/hooks/<WEBHOOK_TOKEN>
webhook -l
```

Use the exact token from `.env`.

## 6. VPS Base Bootstrap

Run on each clean Ubuntu VPS that will accept public `443/tcp`:

```sh
sudo apt-get update
sudo apt-get upgrade -y
sudo DEBIAN_FRONTEND=noninteractive apt-get install -y \
  ca-certificates curl git iproute2 iptables iptables-persistent \
  netcat-openbsd netfilter-persistent tcpdump
```

Enable IPv4 forwarding:

```sh
sudo tee /etc/sysctl.d/99-reverse-logger-forwarding.conf >/dev/null <<EOF
net.ipv4.ip_forward=1
EOF
sudo sysctl --system
```

Run the existing Ansible automation that installs and configures SoftEther for
the VPS. After Ansible completes, verify the VPN interface, routes, and target
reachability:

```sh
ip -br addr
ip route
nc -vz 192.0.2.10 3232
```

In the examples below, `vpn_softether` is the VPS SoftEther interface and
`192.0.2.10:3232` is the main `reverse_ssh` target.

## 7. Apply VPS DNAT

Clone the repository on the VPS or copy only `deploy/iptables/vps-forward.sh`
from the main server:

```sh
sudo mkdir -p /opt/reverse-logger
sudo chown "$USER:$USER" /opt/reverse-logger
git clone https://github.com/durck/reverse_logger.git /opt/reverse-logger
```

Apply DNAT:

```sh
sudo env \
  PUBLIC_IFACE=eth0 \
  VPN_IFACE=vpn_softether \
  PUBLIC_PORT=443 \
  RSSH_TARGET_IP=192.0.2.10 \
  RSSH_TARGET_PORT=3232 \
  sh /opt/reverse-logger/deploy/iptables/vps-forward.sh
```

Leave `SNAT_SOURCE_IP` unset by default. If replies cannot route back through
the VPS, rerun with `SNAT_SOURCE_IP=<VPS_SOFTETHER_IP>` and accept that webhook
`ip_raw` will show the VPS/VPN source address.

If you changed `REVERSE_SSH_PUBLIC_PORT` in the main `.env`, pass the same
value as `PUBLIC_PORT` here. The script also accepts exported
`REVERSE_SSH_PUBLIC_PORT` as a fallback when `PUBLIC_PORT` is unset.

Persist the applied iptables rules after validation:

```sh
sudo netfilter-persistent save
sudo systemctl enable netfilter-persistent
```

Open only `443/tcp` on the public VPS firewall or cloud firewall. Restrict SSH
and management access separately.

See [softether-entrypoint.md](softether-entrypoint.md) for additional DNAT and
SNAT notes.

## 8. Optional Main Ingress Guard

Apply a default-deny policy on the main server's internal ingress interface if
that network also carries traffic from untrusted sources. This is not a
SoftEther interface on the main server; it is the normal interface where the
VPS-routed traffic arrives.

In the default DNAT-only path, do not set a source allowlist here: the main
server sees the real internet client IP, not the VPS VPN IP.

```sh
sudo env \
  INGRESS_IFACE=eth0 \
  REVERSE_SSH_BIND_IP=192.0.2.10 \
  REVERSE_SSH_PORT=3232 \
  sh /opt/reverse-logger/deploy/iptables/main-firewall.sh
```

If centralized edge-event forwarding is enabled, add
`LOGGER_BIND_PORT=8080` so the same path may also reach the central logger. If
VPS forwarding uses `SNAT_SOURCE_IP`, you may additionally set
`ALLOWED_SOURCE_IPS` to those SNAT source addresses.

Verify after applying:

```sh
sudo iptables -S RSSH_INGRESS_GUARD
sudo iptables -S INPUT | grep RSSH_INGRESS_GUARD
sudo iptables -S DOCKER-USER | grep RSSH_INGRESS_GUARD
```

Persist only after verifying access:

```sh
sudo netfilter-persistent save
```

## 9. Optional edge-logger

`edge-logger` is not the default forwarding path in the SoftEther model. Use
it only if you explicitly need VPS-side connection events and accept a
user-space TCP proxy in the data path.

If enabled, point it at the same internal target:

```text
EDGE_LISTEN_ADDR=:443
EDGE_TARGET_ADDR=192.0.2.10:3232
VPS_NAME=vps-1
VPS_PUBLIC_IP=198.51.100.10
VPS_PUBLIC_PORT=443
EDGE_FORWARD_ENABLED=false
```

The systemd unit runs with a dynamic unprivileged user and grants only
`CAP_NET_BIND_SERVICE`, so it can bind `443/tcp` without broader root
privileges.

By default, edge events stay in `/var/lib/reverse-logger/edge_events.jsonl` on
the VPS. To forward them centrally, expose the logger only on the main
internal bind IP with `docker-compose.edge-forward.yml` and set:

```text
EDGE_FORWARD_ENABLED=true
EDGE_FORWARD_URL=http://192.0.2.10:8080/edge-events
EDGE_FORWARD_TOKEN=<EDGE_FORWARD_TOKEN_FROM_MAIN_ENV>
```

## 10. Configure Telegram Proxy

If the main server cannot reach Telegram directly, configure the proxy on a
VPS using [telegram-proxy.md](telegram-proxy.md). Then smoke-test from the main
server:

```sh
curl -x "$TELEGRAM_PROXY_URL" "https://api.telegram.org/bot${TELEGRAM_BOT_TOKEN}/getMe"
```

## 11. Smoke Test

Send a sample webhook inside the Compose network:

```sh
docker compose exec rssh-logger wget -qO- \
  --post-data='{"Status":"connected","ID":"sample","IP":"203.0.113.10:50000","HostName":"user.host","Version":"test","Timestamp":"2026-06-09T12:00:00Z"}' \
  --header='Content-Type: application/json' \
  "http://127.0.0.1:8080/hooks/${WEBHOOK_TOKEN}"
```

Check durable logs:

```sh
tail -n 5 /opt/reverse-logger/data/logger/events.jsonl
sqlite3 /opt/reverse-logger/data/logger/events.db 'select status, host_name, ip_raw, received_at from events order by id desc limit 5;'
```

Then test the real path:

1. Connect from outside to `VPS_PUBLIC_IP:443`.
2. Confirm the reverse_ssh webhook event arrives.
3. Check `ip_raw` in JSONL or SQLite.
4. If `ip_raw` is the real client IP, keep plain DNAT.
5. If `ip_raw` is a VPS/VPN IP, either accept that limitation or enable
   optional VPS-side connection logging.

## 12. Optional systemd Installation

After manual validation:

```sh
sudo cp deploy/systemd/rssh-monitor.service /etc/systemd/system/rssh-monitor.service
sudo systemctl daemon-reload
sudo systemctl enable rssh-monitor
sudo systemctl start rssh-monitor
```

Check:

```sh
systemctl status rssh-monitor
journalctl -u rssh-monitor -n 100 --no-pager
```

## Rollback

Main server:

```sh
cd /opt/reverse-logger
docker compose down
sudo systemctl disable --now rssh-monitor || true
```

VPS:

```sh
sudo iptables -t nat -F RSSH_VPS_PREROUTING || true
sudo iptables -t nat -F RSSH_VPS_POSTROUTING || true
sudo iptables -F RSSH_VPS_FORWARD || true
sudo netfilter-persistent save
```

Then remove or disable:

- VPS DNAT/firewall rules;
- SoftEther account/session for affected VPS nodes;
- Telegram proxy credentials;
- any manually installed `edge-logger` systemd units.
