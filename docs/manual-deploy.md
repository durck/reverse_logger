# Manual Deployment from a Clean Ubuntu Image

This runbook assumes fresh Ubuntu 22.04 LTS or 24.04 LTS servers.

Repository references:

- `reverse_logger`: <https://github.com/durck/reverse_logger>
- `reverse_ssh`: <https://github.com/durck/reverse_ssh>

Topology:

```text
external client -> VPS public IP:443 -> VPS SoftEther interface -> main internal IP:3232 -> reverse_ssh
```

For WSS/HTTPS public transports, nginx terminates public TLS on the VPS and
proxies only the configured transport paths to the same internal
`reverse_ssh` target. Raw DNAT remains a fallback for raw TCP-style exposure.

The main server does not need a SoftEther interface. It only needs a
private/internal address reachable from the VPS through the existing SoftEther
network path. SoftEther installation and account provisioning are intentionally
out of scope because they are handled by deployment automation. The Ansible
playbook in `deploy/ansible/` automates the nginx edge host after DNS,
SoftEther reachability, open `80/tcp` and `443/tcp`, and central logger values
are known.

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

Allow the deploy user to run Docker. The rest of this runbook uses `docker`
and `docker compose` without `sudo`.

```sh
sudo usermod -aG docker "$USER"
newgrp docker
docker info >/dev/null
docker compose version
```

If `docker info` still fails with a permission error, log out and back in, then
rerun the check.

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

Generate the operator SSH key that will be seeded into `reverse_ssh`
`authorized_keys`. This is the key you will use when connecting through
`reverse_ssh`; it is not the SSH key used by Ansible to manage the VPS:

```sh
ssh-keygen -t ed25519 -a 100 -f ~/.ssh/reverse_ssh_operator -C "reverse_ssh_operator"
cat ~/.ssh/reverse_ssh_operator.pub
```

Keep the private key in your local `~/.ssh/` or a vault. Put only the single
public key line into `SEED_AUTHORIZED_KEYS`.

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
- `REVERSE_SSH_IMAGE`: local Docker image tag for your custom `reverse_ssh`
  build, normally `reverse-ssh:local`.
- `REVERSE_SSH_REPO_URL`: optional repository URL to clone automatically.
- `REVERSE_SSH_SOURCE_DIR`: local path for a manually cloned or
  automatically cloned `reverse_ssh` repository.
- `SEED_AUTHORIZED_KEYS`: `reverse_ssh` operator public key contents, not a
  file path and not the private key.
- `WEBHOOK_TOKEN`: first generated token.
- `EDGE_FORWARD_TOKEN`: second generated token if optional edge forwarding is
  used.
- `INGRESS_WS_PATH` / `INGRESS_PUSH_PATH`: central validation paths for nginx
  ingress events. Keep them aligned with VPS nginx and `nginx-edge-forwarder`
  `RSSH_WS_PATH` / `RSSH_PUSH_PATH`; defaults are `/ws` and `/push`.
- `TELEGRAM_*` settings and `TELEGRAM_PROXY_URL` if alerts are enabled.

## 4. Build the reverse_ssh Image Locally

If you want the helper to clone the repository, set `REVERSE_SSH_REPO_URL` in
`.env` first:

```text
REVERSE_SSH_REPO_URL=https://github.com/durck/reverse_ssh.git
REVERSE_SSH_REPO_REF=main
REVERSE_SSH_SOURCE_DIR=/opt/reverse-logger/src/reverse_ssh
REVERSE_SSH_IMAGE=reverse-ssh:local
```

If you prefer to clone manually, leave `REVERSE_SSH_REPO_URL` empty and clone
the repository to `REVERSE_SSH_SOURCE_DIR`:

```sh
mkdir -p /opt/reverse-logger/src
git clone https://github.com/durck/reverse_ssh.git /opt/reverse-logger/src/reverse_ssh
```

Build the local image:

```sh
cd /opt/reverse-logger
set -a
. ./.env
set +a
sh deploy/docker/build-reverse-ssh-image.sh
docker image inspect "$REVERSE_SSH_IMAGE" >/dev/null
```

The build helper expects the `reverse_ssh` repository to contain a Dockerfile.
If the Dockerfile is in a custom location, set `REVERSE_SSH_DOCKERFILE` and
`REVERSE_SSH_BUILD_CONTEXT` in `.env`.

## 5. Start the Main Stack

```sh
cd /opt/reverse-logger
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

## 6. Register reverse_ssh Webhook

Enter the `reverse_ssh` server console using your normal workflow and register:

```text
webhook --on http://rssh-logger:8080/hooks/<WEBHOOK_TOKEN>
webhook -l
```

Use the exact token from `.env`.

## 7. VPS Base Bootstrap

Run on each clean Ubuntu VPS that will accept public `80/tcp` and `443/tcp`:

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

SoftEther provisioning is handled outside this runbook. After the existing
automation has prepared the VPS, verify the VPN interface, routes, and target
reachability:

```sh
ip -br addr
ip route
nc -vz 192.0.2.10 3232
```

In the examples below, `vpn_softether` is the VPS SoftEther interface and
`192.0.2.10:3232` is the main `reverse_ssh` target.

## 8. Apply VPS DNAT

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

Open only `80/tcp` and `443/tcp` on the public VPS firewall or cloud firewall.
Port `80/tcp` is required for Let's Encrypt HTTP-01 validation and may redirect
all non-ACME traffic. Restrict SSH and management access separately.

See [softether-entrypoint.md](softether-entrypoint.md) for additional DNAT and
SNAT notes.

## 9. Optional Main Ingress Guard

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

## 10. Optional edge-logger

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

## 10a. Optional Nginx WSS/HTTPS Entrypoint

If clients use `wss://` or `https://` transports and the VPS should present a
normal HTTPS surface, use [nginx-wss-https-entrypoint.md](nginx-wss-https-entrypoint.md)
instead of raw DNAT or `edge-logger`.

This path logs only WSS handshakes and HTTPS polling init requests on the VPS,
forwards them to `/ingress-events/<EDGE_FORWARD_TOKEN>`, and keeps the
`reverse_ssh` webhook as the canonical connected/disconnected source.

Keep these values identical across the stack:

- nginx public `rssh_ws_path` / `rssh_push_path`;
- VPS forwarder `RSSH_WS_PATH` / `RSSH_PUSH_PATH`;
- central logger `INGRESS_WS_PATH` / `INGRESS_PUSH_PATH`;
- `reverse_ssh` server/client `--ws-path` / `--push-path` or baked client
  values from `link --ws-path` / `link --push-path`.

Use absolute base paths without a trailing slash, for example `/ws`,
`/rssh-ws`, `/push`, or `/rssh-push`.

When nginx is in front of `reverse_ssh`, run the patched server with
`--trusted-proxy-cidr <vps_internal_ip>/32` or a narrower CIDR that contains
only trusted VPS nginx sources. The nginx route overwrites `X-Real-IP` and
`X-Forwarded-For` with `$remote_addr`; do not accept those headers from
untrusted sources.

## 10b. Automated Nginx WSS/HTTPS VPS Entrypoint

For a clean VPS edge rollout, use the Ansible playbook:

```sh
cp deploy/ansible/inventory.example.ini deploy/ansible/inventory.ini
cp deploy/ansible/group_vars/vps_edge.example.yml deploy/ansible/group_vars/vps_edge.yml
nano deploy/ansible/inventory.ini
nano deploy/ansible/group_vars/vps_edge.yml
ansible-playbook -i deploy/ansible/inventory.ini deploy/ansible/vps-edge.yml
```

The playbook installs nginx, Snap Certbot, builds `nginx-edge-forwarder`,
issues a free Let's Encrypt certificate with HTTP-01 webroot validation,
renders systemd and nginx config, and enables both services. It still requires
an existing A record pointing `rssh_domain` at the VPS, open `80/tcp` and
`443/tcp`, SoftEther/internal reachability to `backend_reverse_ssh_url`, and
`nginx_edge_acme_email`. PTR is useful for operations hygiene but is not used
for ACME validation. Wildcard certificates are not supported by this HTTP-01
flow; use DNS-01 if wildcard certificates are required. Do not run
`certbot --nginx` against this entrypoint because Ansible owns the nginx
configuration.
See [../deploy/ansible/README.md](../deploy/ansible/README.md) for all
variables and rollback commands.

## 11. Configure Telegram Proxy

If the main server cannot reach Telegram directly, configure the proxy on a
VPS using [telegram-proxy.md](telegram-proxy.md). Then smoke-test from the main
server:

```sh
curl -x "$TELEGRAM_PROXY_URL" "https://api.telegram.org/bot${TELEGRAM_BOT_TOKEN}/getMe"
```

## 12. Smoke Test

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

## 13. Optional systemd Installation

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
