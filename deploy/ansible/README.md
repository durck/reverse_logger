# Ansible VPS Edge Deployment

This playbook installs the nginx WSS/HTTPS entrypoint and
`nginx-edge-forwarder` on a clean Ubuntu VPS.

Repository references:

- `reverse_logger`: <https://github.com/durck/reverse_logger>
- `reverse_ssh`: <https://github.com/durck/reverse_ssh>

The playbook owns the VPS edge layer only:

- installs nginx, Go, git, Snap Certbot, and runtime dependencies;
- issues a free Let's Encrypt certificate with HTTP-01 webroot validation;
- clones `reverse_logger`;
- builds and installs `cmd/nginx-edge-forwarder`;
- creates `/var/lib/reverse-logger` and the nginx edge spool directory;
- renders `/etc/reverse-logger/nginx-edge-forwarder.env`;
- renders nginx with custom WSS and HTTPS polling paths;
- enables and starts nginx and `nginx-edge-forwarder`.

It does not provision SoftEther accounts, DNS records, reverse DNS/PTR records,
cloud firewall rules, or the main `reverse_ssh` server. DNS and network reachability
must exist before running the playbook. A self-signed certificate option is
available only for initial smoke testing when ACME is disabled.

## Prepare Inventory

Copy the examples and edit them:

```sh
cp deploy/ansible/inventory.example.ini deploy/ansible/inventory.ini
cp deploy/ansible/group_vars/vps_edge.example.yml deploy/ansible/group_vars/vps_edge.yml
nano deploy/ansible/inventory.ini
nano deploy/ansible/group_vars/vps_edge.yml
```

Set at minimum:

- `rssh_domain`;
- `backend_reverse_ssh_url`, normally `https://<main_internal_ip>:3232`;
- `edge_forward_url`, normally `http://<main_internal_ip>:8080/ingress-events`;
- `edge_forward_token`, copied from the main server `.env`;
- `vps_name`, `vps_public_ip`, `vps_internal_ip`;
- `nginx_edge_acme_email`, used for Let's Encrypt registration and expiry notices;
- `nginx_edge_acme_staging`, set to `true` for the first test run;
- `rssh_ws_path` and `rssh_push_path`, kept aligned with nginx, forwarder,
  central `INGRESS_*`, and `reverse_ssh` server/client flags;
- `rssh_download_path_prefix`, normally `/dl`. Public downloads are
  `https://<rssh_domain>/dl/<filename>` while `link --name <filename>` serves
  backend `/<filename>`.

Custom paths must be absolute base paths without a trailing slash, for example
`/ws`, `/rssh-ws`, `/push`, `/rssh-push`, or `/dl`.

The rendered nginx config preserves original transport paths for mirror capture
with `$rssh_mirror_path`. Do not replace `X-Original-Path $uri` in the capture
location; mirror subrequests use `/_rssh_ingress_capture` as `$uri`.

Before running:

- the DNS A record for `rssh_domain` must point at this VPS public IP;
- inbound `80/tcp` and `443/tcp` must be allowed by the cloud firewall and OS firewall;
- PTR/reverse DNS may be configured for operations hygiene, but it is not used
  by ACME HTTP-01 validation;
- wildcard certificates are not supported by this HTTP-01 flow; use DNS-01 if
  wildcard certificates are required.

## Run

```sh
ansible-playbook -i deploy/ansible/inventory.ini deploy/ansible/vps-edge.yml
```

Use staging first to avoid production rate limits while validating DNS and firewall:

```sh
ansible-playbook -i deploy/ansible/inventory.ini deploy/ansible/vps-edge.yml \
  -e nginx_edge_acme_staging=true
```

For a temporary smoke certificate:

```sh
ansible-playbook -i deploy/ansible/inventory.ini deploy/ansible/vps-edge.yml \
  -e nginx_edge_acme_enabled=false \
  -e nginx_edge_create_self_signed_cert=true \
  -e tls_cert_path=/etc/reverse-logger/tls/fullchain.pem \
  -e tls_key_path=/etc/reverse-logger/tls/privkey.pem
```

Do not run `certbot --nginx` for this entrypoint. The playbook uses
`certbot certonly --webroot` so Certbot never rewrites Ansible-managed nginx
configuration.

## Verify

```sh
ssh <vps> 'systemctl status nginx nginx-edge-forwarder --no-pager'
ssh <vps> 'journalctl -u nginx-edge-forwarder -n 100 --no-pager'
ssh <vps> 'tail -n 20 /var/log/nginx/reverse_ssh_ingress.json'
ssh <vps> 'test -f /etc/letsencrypt/live/<rssh_domain>/fullchain.pem'
ssh <vps> 'curl -I http://<rssh_domain>/.well-known/acme-challenge/test || true'
```

The main server must run `rssh-logger` with
`docker-compose.edge-forward.yml`, and `reverse_ssh` should trust only the VPS
nginx source when real-IP headers are enabled:

```sh
docker compose -f docker-compose.yml -f docker-compose.edge-forward.yml up -d
```

The main server `reverse_ssh` listener must use the same paths as the VPS
nginx edge. In this repository's Docker stack, set:

```text
REVERSE_SSH_WS_PATH=/ws
REVERSE_SSH_PUSH_PATH=/push
REVERSE_SSH_TRUSTED_PROXY_CIDR=<vps_internal_ip>/32
INGRESS_WS_PATH=/ws
INGRESS_PUSH_PATH=/push
```

Then recreate the listener:

```sh
docker compose up -d --force-recreate reverse_ssh rssh-logger
```

Use the same custom paths in generated clients:

```text
link --wss --ws-path /ws --push-path /push --name main
```

Verify a download path reaches `reverse_ssh` instead of the decoy redirect:

```sh
curl -I https://<rssh_domain>/dl/main
```

## Rollback

```sh
ssh <vps> 'sudo systemctl disable --now nginx-edge-forwarder'
ssh <vps> 'sudo rm -f /etc/systemd/system/nginx-edge-forwarder.service'
ssh <vps> 'sudo rm -f /etc/nginx/sites-enabled/rssh-entrypoint.conf /etc/nginx/sites-available/rssh-entrypoint.conf'
ssh <vps> 'sudo systemctl reload nginx'
```
