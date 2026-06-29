# Ansible VPS Edge Deployment

Deploy one or many public VPS edges that terminate TLS and forward to the same
internal `reverse_ssh` + `rssh-logger` stack over SoftEther/VPN.

Repository references:

- `reverse_logger`: <https://github.com/durck/reverse_logger>
- `reverse_ssh`: <https://github.com/durck/reverse_ssh>

The playbook owns the VPS edge layer only:

- installs nginx, Go, git, Snap Certbot, and runtime dependencies;
- issues a free Let's Encrypt certificate with HTTP-01 webroot validation or
  Timeweb DNS-01 validation;
- clones `reverse_logger`;
- builds and installs `cmd/nginx-edge-forwarder`;
- creates state directories;
- renders `/etc/reverse-logger/nginx-edge-forwarder.env` per host;
- renders nginx with custom WSS, HTTPS polling, and `/dl/` download paths;
- enables and starts nginx and `nginx-edge-forwarder`.

It does **not** provision SoftEther accounts, DNS records, cloud firewall rules,
or the main `reverse_ssh` server.

## Multi-VPS layout

```text
entry1.example.com (203.0.113.20) ─VPN──┐
entry2.example.com (203.0.113.30) ─VPN──┼──> main 192.0.2.10
entry3.example.com (...)          ─VPN──┘     reverse_ssh :3232
                                           rssh-logger :8080
```

## Four edge groups with different transport paths

Use child groups when each fleet gets its own `link` paths but all edges share one
main `reverse_ssh` listener:

```text
edge_group_1 -> /track383211 + /ping198287
edge_group_2 -> /track_group2 + /ping_group2
edge_group_3 -> ...
edge_group_4 -> ...
```

Layout:

```text
Client (group 1 paths) -> VPS nginx /track383211 -> main reverse_ssh /ws
Client (group 2 paths) -> VPS nginx /track_group2 -> main reverse_ssh /ws
```

Files:

```sh
cp deploy/ansible/group_vars/edge_group_1.example.yml deploy/ansible/group_vars/edge_group_1.yml
cp deploy/ansible/group_vars/edge_group_2.example.yml deploy/ansible/group_vars/edge_group_2.yml
cp deploy/ansible/group_vars/edge_group_3.example.yml deploy/ansible/group_vars/edge_group_3.yml
cp deploy/ansible/group_vars/edge_group_4.example.yml deploy/ansible/group_vars/edge_group_4.yml
```

Shared backend paths on main live in `group_vars/vps_edge.yml`:

```yaml
main_ws_path: /ws
main_push_path: /push
```

Per-group public paths live in `group_vars/edge_group_N.yml`:

```yaml
rssh_ws_path: /track383211
rssh_push_path: /ping198287
```

Generate one `link` per group on main with the same paths:

```text
link --wss --ws-path /track383211 --push-path /ping198287 --name dl/main-g1 ...
```

After deploy, the playbook prints:

```text
INGRESS_WS_PATH=/track383211,/track_group2,...
INGRESS_PUSH_PATH=/ping198287,/ping_group2,...
REVERSE_SSH_WS_PATH=/ws
REVERSE_SSH_PUSH_PATH=/push
```

Add those values to main `.env`, recreate `reverse_ssh` and `rssh-logger`, then
deploy clients from each group's download URL.

Alternative: set `main_reverse_ssh_port` per group and run separate `reverse_ssh`
listeners on main when you do not want nginx path rewriting.

Each VPS gets:

- its own `rssh_domain` and Let's Encrypt certificate;
- optional auto-detected `vps_internal_ip` (VPN source IP toward main);
- shared `main_internal_ip`, `edge_forward_token`, transport paths.

## Prerequisites (before Ansible)

On **each VPS**:

1. Ubuntu with root SSH access.
2. SoftEther/VPN client configured and connected to main.
3. Route to main works: `ip route get <main_internal_ip>` should show a `src` VPN IP.
   If it does not, deployment still works, but trusted proxy CIDR output is skipped
   and central event correlation falls back to observed `forwarder_ip`.
4. DNS `A` record: `<rssh_domain>` → public IP of this VPS.
5. Inbound `443/tcp` open. Inbound `80/tcp` is required only for the default
   HTTP-01 ACME challenge.

On **main** (`/opt/reverse-logger`):

1. Stack running with `docker-compose.edge-forward.yml`.
2. `.env` contains `EDGE_FORWARD_TOKEN`, `REVERSE_SSH_BIND_IP`, matching `INGRESS_*`
   and `REVERSE_SSH_*` paths.

## Prepare inventory

From the repo root:

```sh
cp deploy/ansible/inventory.example.ini deploy/ansible/inventory.ini
cp deploy/ansible/group_vars/vps_edge.example.yml deploy/ansible/group_vars/vps_edge.yml
```

Or use YAML inventory:

```sh
cp deploy/ansible/inventory.example.yml deploy/ansible/inventory.yml
```

When using YAML inventory, pass it explicitly with `-i inventory.yml`; local
`ansible.cfg` defaults to `inventory.ini`.

Both inventory files are gitignored once copied.

### Inventory: groups and hosts

`deploy/ansible/inventory.ini`:

```ini
[vps_edge:children]
edge_group_1
edge_group_2
edge_group_3
edge_group_4

[edge_group_1]
edge1 ansible_host=203.0.113.20 rssh_domain=entry1.example.com

[edge_group_2]
edge2-a ansible_host=203.0.113.30 rssh_domain=entry2a.example.com

[vps_edge:vars]
ansible_user=root
ansible_ssh_pass=your-root-password
```

Deploy one group only:

```sh
ansible-playbook vps-edge.yml --limit edge_group_1
```

Or pass the password interactively:

```sh
ansible-playbook ... --ask-pass
```

### Shared group vars

`deploy/ansible/group_vars/vps_edge.yml`:

```yaml
main_internal_ip: 192.0.2.10
edge_forward_token: <from main .env>
redirect_target: https://example.com
main_ws_path: /ws
main_push_path: /push
```

Per-group `rssh_ws_path` / `rssh_push_path` stay in `group_vars/edge_group_N.yml`.
Per-host `rssh_domain` stays in inventory.

### Timeweb DNS-01 certificates

Default certificate issuance uses HTTP-01:

```yaml
nginx_edge_acme_challenge: http-01
```

For Timeweb DNS-01, switch the challenge mode and provide a Timeweb Cloud API
token that can edit DNS records for the domain zone:

```yaml
nginx_edge_acme_challenge: dns-timeweb
timewebcloud_auth_token: <Timeweb Cloud API token>
timewebcloud_propagation_timeout: 120
timewebcloud_polling_interval: 5
```

The playbook installs `certbot-dns-multi`, writes credentials to
`/etc/letsencrypt/dns-multi.ini` with mode `0600`, and runs certbot with the
`dns-multi` authenticator. In this mode the CA validates
`_acme-challenge.<domain>` in DNS, so the VPS does not need inbound `80/tcp`
for certificate issuance.

Do not commit the Timeweb token. `group_vars/vps_edge.yml` is gitignored, but
`ansible-vault` is still preferred:

```sh
ansible-vault encrypt_string '<Timeweb Cloud API token>' --name timewebcloud_auth_token
```

Paste the encrypted variable into `group_vars/vps_edge.yml` and run playbooks
with:

```sh
ansible-playbook vps-edge.yml --ask-vault-pass
```

If you are migrating an already-issued HTTP-01 certificate and need to force an
immediate DNS-01 reissue once, set:

```yaml
nginx_edge_acme_force_renewal: true
```

Set it back to `false` after the migration.

### Auto-derived per VPS

| Variable | Source |
|----------|--------|
| `backend_reverse_ssh_url` | `https://<main_internal_ip>:3232` |
| `edge_forward_url` | `http://<main_internal_ip>:8080/ingress-events` |
| `vps_name` | inventory hostname |
| `vps_public_ip` | `ansible_host` |
| `vps_internal_ip` | optional `ip route get <main_internal_ip>` → `src` |
| `nginx_edge_acme_email` | `admin@<rssh_domain>` |

## Run

From `deploy/ansible` (uses local `ansible.cfg`):

```sh
cd deploy/ansible
ansible-playbook vps-edge.yml
```

With YAML inventory:

```sh
ansible-playbook -i inventory.yml vps-edge.yml
```

From repo root:

```sh
ansible-playbook -i deploy/ansible/inventory.ini deploy/ansible/vps-edge.yml
```

Limit to one host:

```sh
ansible-playbook vps-edge.yml --limit edge1
```

Staging certificates first:

```sh
ansible-playbook vps-edge.yml -e nginx_edge_acme_staging=true
```

Check SSH before the full run:

```sh
ansible vps_edge -m ping
```

## After deploy

The playbook prints a summary per host and a combined line for main:

```text
REVERSE_SSH_TRUSTED_PROXY_CIDR=192.0.2.98/32,192.0.2.99/32
```

Add that to main `.env` when it is printed. If no `vps_internal_ip` was
detected, keep the existing trusted proxy value and rely on central
`forwarder_ip` correlation fallback.

Then recreate listeners:

```sh
cd /opt/reverse-logger
docker compose -f docker-compose.yml -f docker-compose.edge-forward.yml up -d --force-recreate reverse_ssh rssh-logger
```

Verify each edge:

```sh
ansible vps_edge -m shell -a 'systemctl is-active nginx nginx-edge-forwarder'
curl -I https://entry1.example.com/dl/main
```

## Verify (single host)

```sh
ssh root@203.0.113.20 'systemctl status nginx nginx-edge-forwarder --no-pager'
ssh root@203.0.113.20 'grep VPS_INTERNAL_IP /etc/reverse-logger/nginx-edge-forwarder.env'
ssh root@203.0.113.20 'curl -I https://entry1.example.com/not-a-path'
```

## Rollback

```sh
ansible vps_edge -b -m systemd -a 'name=nginx-edge-forwarder enabled=no state=stopped'
ansible vps_edge -b -m file -a 'path=/etc/nginx/sites-enabled/rssh-entrypoint.conf state=absent'
ansible vps_edge -b -m systemd -a 'name=nginx state=reloaded'
```

## Notes

- Custom transport paths must match main `.env`, forwarder `RSSH_*`, and `link --wss`.
- `/dl/` uses `proxy_buffering off` for large client binaries.
- Mirror capture requires `$rssh_mirror_path` in the nginx template (already rendered).
- Do not run `certbot --nginx`; the playbook uses `certbot certonly` with the
  configured ACME challenge.
