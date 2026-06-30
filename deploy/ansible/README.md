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
- optionally generates matching `reverse_ssh link` entries on main after the
  VPS edge hosts pass readiness checks.

It does **not** provision SoftEther accounts, DNS records, cloud firewall rules,
or the main `reverse_ssh` server itself.

## Multi-VPS layout

```text
entry1.example.com (203.0.113.20) ─VPN──┐
entry2.example.com (203.0.113.30) ─VPN──┼──> main 192.0.2.10
entry3.example.com (...)          ─VPN──┘     reverse_ssh :3232
                                           rssh-logger :8080
```

## Four edge groups with different transport paths

Use child groups when each fleet gets its own `link` paths but all edges share one
main `reverse_ssh` listener. Alternatively, enable per-host random paths in
`group_vars/vps_edge.yml` and let Ansible persist them locally.

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

Generate one `link` per group on main with the same paths, either manually:

```text
link --wss --ws-path /track383211 --push-path /ping198287 --name dl/main-g1 ...
```

or with `reverse-ssh-links.yml`, which reads `rssh_domain`, `rssh_ws_path`,
`rssh_push_path`, and `rssh_download_path_prefix` from each ready host.

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
- optional auto-detected `vps_internal_ip` from the main logger's observed
  source IP probe;
- shared `main_internal_ip`, `edge_forward_token`, transport paths.

## Prerequisites (before Ansible)

On **each VPS**:

1. Ubuntu with root SSH access.
2. SoftEther/VPN client configured and connected to main.
3. Route to main works: the VPS can reach
   `http://<main_internal_ip>:8080/edge/source-ip/<EDGE_FORWARD_TOKEN>`. If the
   probe is unavailable, deployment still works, but trusted proxy CIDR output
   is skipped and central event correlation falls back to observed
   `forwarder_ip`.
4. DNS `A` record: `<rssh_domain>` → public IP of this VPS.
5. Inbound `443/tcp` open. Inbound `80/tcp` is required only for the default
   HTTP-01 ACME challenge.

On **main** (`/opt/reverse-logger`):

1. Stack running with `docker-compose.edge-forward.yml`.
2. `.env` contains `EDGE_FORWARD_TOKEN`, `REVERSE_SSH_BIND_IP`, matching `INGRESS_*`
   and `REVERSE_SSH_*` paths.
3. Operator SSH key can connect to the `reverse_ssh` console:
   `ssh -i /root/.ssh/reverse_ssh_operator -p 3232 127.0.0.1`.

## Prepare inventory

From the repo root:

```sh
cp deploy/ansible/inventory.example.ini deploy/ansible/inventory.ini
cp deploy/ansible/group_vars/vps_edge.example.yml deploy/ansible/group_vars/vps_edge.yml
cp deploy/ansible/group_vars/main.example.yml deploy/ansible/group_vars/main.yml
```

Or use YAML inventory:

```sh
cp deploy/ansible/inventory.example.yml deploy/ansible/inventory.yml
```

When using YAML inventory, pass it explicitly with `-i inventory.yml`; local
`ansible.cfg` defaults to `inventory.ini`.

Both inventory files are gitignored once copied.
Real `group_vars/main.yml`, `group_vars/vps_edge.yml`, and
`group_vars/edge_group_N.yml` files are also gitignored.

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

The optional `[main]` group is used by `reverse-ssh-links.yml`:

```ini
[main]
main1 ansible_host=192.0.2.10

[main:vars]
ansible_user=root
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

### Per-host random public paths

Enable this when each VPS should get its own persisted random public paths for
WSS, HTTPS polling, and downloads:

```yaml
rssh_random_paths_enabled: true
rssh_random_path_min_length: 6
rssh_random_path_max_length: 15
rssh_random_path_chars: ascii_lowercase,digits
```

On first run, Ansible creates one local state file per host:

```text
deploy/ansible/.generated-paths/<inventory_hostname>.yml
```

Example generated state:

```yaml
rssh_random_ws_path: /k4v9spq
rssh_random_push_path: /m27xqqn18
rssh_random_download_path_prefix: /a8f3kz
```

That directory is gitignored. Keep it with your private deployment state or
back it up separately; deleting a host file makes Ansible generate new paths on
the next run.

By default, generated values replace only the legacy defaults `/ws`, `/push`,
and `/dl`. Explicit non-default values in inventory or group vars are kept. To
force generated values even when custom paths already exist, set:

```yaml
rssh_random_paths_force: true
```

To intentionally rotate paths, set this for one run:

```sh
ansible-playbook edge-and-links.yml -e rssh_random_paths_regenerate=true
```

Rotation changes nginx paths and generated client callbacks. Existing clients
and old links will keep using the old paths, so rotate together with
`reverse_ssh_link_force_rotate=true` when replacing generated binaries:

```sh
ansible-playbook edge-and-links.yml \
  -e rssh_random_paths_regenerate=true \
  -e reverse_ssh_link_force_rotate=true
```

`reverse-ssh-links.yml` only reads the persisted state; it does not regenerate
paths by itself. Regenerate through `vps-edge.yml` or `edge-and-links.yml` so
nginx, the forwarder env, and generated client links stay aligned. If random
paths are enabled but the persisted state file is missing, link generation
fails instead of silently creating a new client callback path.

### Main link generation vars

`deploy/ansible/group_vars/main.yml` controls automated client link generation
on the main host:

```yaml
reverse_ssh_console_host: 127.0.0.1
reverse_ssh_console_port: 3232
reverse_ssh_console_user: root
reverse_ssh_console_key: /root/.ssh/reverse_ssh_operator

reverse_ssh_link_transports:
  - wss
  # - https

reverse_ssh_link_platforms:
  - goos: windows
    goarch: amd64

reverse_ssh_link_garble: true
reverse_ssh_link_auto_proxy: true
reverse_ssh_link_use_kerberos: true
reverse_ssh_link_force_rotate: false
```

The generated command uses the per-host edge values:

```text
link --wss -s <rssh_domain>:443 --ws-path <rssh_ws_path> --push-path <rssh_push_path> --name <generated-name> --goos <goos> --goarch <goarch> --garble --auto-proxy --use-kerberos
```

Names are rendered with `reverse_ssh_link_name_template`, default:
`{vps_host}-{transport}-{goos}-{goarch}`.

### Timeweb DNS-01 certificates

Default certificate issuance uses HTTP-01:

```yaml
nginx_edge_acme_challenge: http-01
```

When `timewebcloud_auth_token` is set, the playbook can automatically fall
back to Timeweb DNS-01 if HTTP-01 fails, for example when public `80/tcp` is
blocked:

```yaml
nginx_edge_acme_http01_fallback_to_dns_timeweb: true
timewebcloud_auth_token: <Timeweb Cloud API token>
timewebcloud_propagation_timeout: 120
timewebcloud_polling_interval: 5
```

With this setting, HTTP-01 is still attempted first. If it succeeds, DNS-01 is
not used. If it fails and the token is valid, the playbook re-runs certbot with
`dns-multi` and the Timeweb provider. If the token is missing, the playbook
fails with the original HTTP-01 stderr and a message explaining how to enable
fallback.

Before it calls certbot for HTTP-01, the playbook creates a real preflight file
under `{{ nginx_edge_acme_webroot }}/.well-known/acme-challenge/` and fetches it
from the control node through `http://{{ rssh_domain }}/...`. If this check
does not return the expected body, certbot HTTP-01 is skipped and the playbook
either falls back to Timeweb DNS-01 or fails without consuming another Let's
Encrypt authorization attempt.

```yaml
nginx_edge_acme_http01_preflight_enabled: true
nginx_edge_acme_http01_preflight_timeout: 10
```

For Timeweb DNS-01, switch the challenge mode and provide a Timeweb Cloud API
token that can edit DNS records for the domain zone:

```yaml
nginx_edge_acme_challenge: dns-timeweb
timewebcloud_auth_token: <Timeweb Cloud API token>
timewebcloud_propagation_timeout: 300
timewebcloud_polling_interval: 5
nginx_edge_acme_dns_multi_propagation_seconds: 300
nginx_edge_acme_dns_multi_nameservers: "1.1.1.1:53,8.8.8.8:53"
```

The playbook installs `certbot-dns-multi`, writes credentials to
`/etc/letsencrypt/dns-multi.ini` with mode `0600`, and runs certbot with the
`dns-multi` authenticator. In this mode the CA validates
`_acme-challenge.<domain>` in DNS, so the VPS does not need inbound `80/tcp`
for certificate issuance.

`nginx_edge_acme_dns_multi_propagation_seconds` is passed directly to certbot's
`--dns-multi-propagation-seconds` flag. Keep it at `300` for Timeweb zones that
publish TXT records slowly; certbot's plugin default is only 60 seconds.

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

ACME-managed nginx certificate paths are tied to a certbot lineage name:

```yaml
nginx_edge_acme_cert_name: "{{ rssh_domain }}"
tls_cert_path: "/etc/letsencrypt/live/{{ nginx_edge_acme_cert_name }}/fullchain.pem"
tls_key_path: "/etc/letsencrypt/live/{{ nginx_edge_acme_cert_name }}/privkey.pem"
```

When `nginx_edge_acme_enabled: true`, do not point `tls_cert_path` or
`tls_key_path` at arbitrary files; certbot writes the lineage selected by
`nginx_edge_acme_cert_name`, and the playbook validates that nginx uses that
same lineage.

If you are migrating an already-issued HTTP-01 certificate and need to force an
immediate DNS-01 reissue once, set:

```yaml
nginx_edge_acme_force_renewal: true
```

Set it back to `false` after the migration.

### Network retries and partial reruns

The VPS playbook retries transient network operations with bounded backoff:
package installs, backend reachability checks, optional source-IP probes, HTTP-01
preflight checks, Snap Store operations, GitHub checkout, Go module downloads,
and DNS-01 certbot runs.

```yaml
nginx_edge_network_retries: 5
nginx_edge_network_delay: 10
nginx_edge_snap_retries: 4
nginx_edge_snap_delay: 15
nginx_edge_certbot_install_method: snap
nginx_edge_certbot_snap_fallback_to_pip: true
nginx_edge_certbot_venv_path: /opt/certbot
nginx_edge_acme_retries: 2
nginx_edge_acme_delay: 30
```

Snap retries are shorter than the generic network retry budget because a
persistent Snap Store failure can fall back to the pip/venv Certbot path.

The target-side Go build avoids Go's automatic toolchain download during
deployment:

```yaml
reverse_logger_go_min_version: "1.23"
reverse_logger_go_toolchain: local
reverse_logger_go_proxy: direct
reverse_logger_go_sumdb: "off"
reverse_logger_go_retries: 3
reverse_logger_go_delay: 10
```

`GOTOOLCHAIN=local` prevents `go mod download` from fetching a newer compiler
from `proxy.golang.org` when target DNS is flaky. The module download uses the
committed `go.sum`; set `reverse_logger_go_proxy` / `reverse_logger_go_sumdb`
back to public Go defaults if the target environment should use the public Go
proxy and checksum database.

Snap installs first check whether `core`, `certbot`, and `certbot-dns-multi`
are already installed. The playbook only checks `api.snapcraft.io` DNS before a
missing snap must actually be installed, so a later run is not blocked by Snap
Store DNS if the needed snaps are already present.

When Snap remains unavailable after all retries and
`nginx_edge_certbot_snap_fallback_to_pip: true`, the playbook falls back to a
Python virtualenv at `nginx_edge_certbot_venv_path`. It removes only Certbot
snap state created by the current run: `/usr/bin/certbot` when it points at
`/snap/bin/certbot`, plus `certbot` / `certbot-dns-multi` snaps that were not
installed before the run. It does not remove `snapd`, `core`, or unrelated snap
packages.

The pip fallback installs `certbot` and, only when Timeweb DNS-01 can be used,
`certbot-dns-multi`. The effective `nginx_edge_certbot_pip_packages` list is
derived by the playbook from the ACME mode and token availability. Both Snap and
pip paths expose the same
`/usr/bin/certbot` command, so the ACME issuance commands do not change.

To retry just Certbot bootstrap after a Snap Store or DNS flake:

```sh
ansible-playbook vps-edge.yml --limit edge1 --tags snap
```

ACME retries are intentionally conservative because repeated real validation
failures can consume Let's Encrypt failed-validation limits. Fix DNS, public
`80/tcp`, or DNS-provider credentials before increasing `nginx_edge_acme_retries`.

### Auto-derived per VPS

| Variable | Source |
|----------|--------|
| `backend_reverse_ssh_url` | `https://<main_internal_ip>:3232` |
| `edge_forward_url` | `http://<main_internal_ip>:8080/ingress-events` |
| `vps_name` | inventory hostname |
| `vps_public_ip` | `ansible_host` |
| `vps_internal_ip` | optional `/edge/source-ip` response from main logger |
| `nginx_edge_acme_email` | `admin@<rssh_domain>` |

## Run

From `deploy/ansible` (uses local `ansible.cfg`):

```sh
cd deploy/ansible
ansible-playbook vps-edge.yml
```

If the target already has `reverse_logger_src_dir` cloned but cannot reach
GitHub over `443/tcp`, keep the existing checkout and skip fetches:

```sh
ansible-playbook vps-edge.yml -e reverse_logger_repo_update=false
```

Deploy VPS edges and then immediately generate links on main:

```sh
ansible-playbook edge-and-links.yml
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

When using `edge-and-links.yml` with `--limit`, include the main host too so
the second play can connect to the `reverse_ssh` console. Link generation uses
only the selected edge hosts from the first play:

```sh
ansible-playbook edge-and-links.yml --limit edge1,main1
```

For `reverse-ssh-links.yml`, pass explicit target edges when you intentionally
want to bypass the first `vps_edge` selection play:

```sh
ansible-playbook reverse-ssh-links.yml \
  -e 'reverse_ssh_target_edges=["edge1"]'
```

Staging certificates first:

```sh
ansible-playbook vps-edge.yml -e nginx_edge_acme_staging=true
```

Check SSH before the full run:

```sh
ansible vps_edge -m ping
```

## Generate reverse_ssh links

Run this after `vps-edge.yml`, or use `edge-and-links.yml` to do both in one
Ansible invocation:

```sh
ansible-playbook reverse-ssh-links.yml
```

Before creating anything, the playbook checks each `vps_edge` host:

- `nginx -t` succeeds;
- `nginx` and `nginx-edge-forwarder` are active;
- `/etc/reverse-logger/nginx-edge-forwarder.env` exists;
- `RSSH_WS_PATH` and `RSSH_PUSH_PATH` match the host/group variables;
- certificate files exist when `reverse_ssh_link_check_tls_files: true`.

Hosts that fail readiness are skipped and do not get links. If no host is ready,
the playbook fails without creating anything.

Idempotency is name-based. The helper runs `link -l` in the main
`reverse_ssh` console and skips exact names that already exist:

```sh
ansible-playbook reverse-ssh-links.yml
```

To rotate existing links, set:

```sh
ansible-playbook reverse-ssh-links.yml -e reverse_ssh_link_force_rotate=true
```

This runs `link -r <name>` before creating the replacement link. Use a dry run
to render the exact commands without entering the `reverse_ssh` console:

```sh
ansible-playbook reverse-ssh-links.yml -e reverse_ssh_link_execute=false
```

Generated artifacts on main:

```text
/opt/reverse-logger/generated-links/spec.json
/opt/reverse-logger/generated-links/commands.sh
/opt/reverse-logger/generated-links/result.json
```

`result.json` includes the public download URL for each generated link, using
the host's resolved `rssh_download_path_prefix`.

## After deploy

The playbook prints a summary per host and a combined line for main:

```text
REVERSE_SSH_TRUSTED_PROXY_CIDR=192.0.2.98/32,192.0.2.99/32
```

Add that to main `.env` when it is printed. If no `vps_internal_ip` was
detected by the main-observed source IP probe, keep the existing trusted proxy
value and rely on central `forwarder_ip` correlation fallback.

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

Verify generated links on main:

```sh
ssh -i /root/.ssh/reverse_ssh_operator -p 3232 127.0.0.1
link -l
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
- Per-host generated paths are local deployment state. Preserve
  `deploy/ansible/.generated-paths/` if you want repeated runs to keep the same
  public paths.
- `/dl/` uses `proxy_buffering off` for large client binaries.
- Mirror capture requires `$rssh_mirror_path` in the nginx template (already rendered).
- Do not run `certbot --nginx`; the playbook uses `certbot certonly` with the
  configured ACME challenge.
