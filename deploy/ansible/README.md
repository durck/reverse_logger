# Ansible VPS Edge Deployment

This playbook installs the nginx WSS/HTTPS entrypoint and
`nginx-edge-forwarder` on a clean Ubuntu VPS.

Repository references:

- `reverse_logger`: <https://github.com/durck/reverse_logger>
- `reverse_ssh`: <https://github.com/durck/reverse_ssh>

The playbook owns the VPS edge layer only:

- installs nginx, Go, git, and runtime dependencies;
- clones `reverse_logger`;
- builds and installs `cmd/nginx-edge-forwarder`;
- renders `/etc/reverse-logger/nginx-edge-forwarder.env`;
- renders nginx with custom WSS and HTTPS polling paths;
- enables and starts nginx and `nginx-edge-forwarder`.

It does not provision SoftEther accounts, DNS records, production ACME
certificates, or the main `reverse_ssh` server. Those values must exist before
running the playbook. A self-signed certificate option is available only for
initial smoke testing.

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
- `tls_cert_path` and `tls_key_path`, unless using the self-signed smoke mode;
- `rssh_ws_path` and `rssh_push_path`, kept aligned with nginx, forwarder,
  central `INGRESS_*`, and `reverse_ssh` server/client flags.

Custom paths must be absolute base paths without a trailing slash, for example
`/ws`, `/rssh-ws`, `/push`, or `/rssh-push`.

## Run

```sh
ansible-playbook -i deploy/ansible/inventory.ini deploy/ansible/vps-edge.yml
```

For a temporary smoke certificate:

```sh
ansible-playbook -i deploy/ansible/inventory.ini deploy/ansible/vps-edge.yml \
  -e nginx_edge_create_self_signed_cert=true \
  -e tls_cert_path=/etc/reverse-logger/tls/fullchain.pem \
  -e tls_key_path=/etc/reverse-logger/tls/privkey.pem
```

## Verify

```sh
ssh <vps> 'systemctl status nginx nginx-edge-forwarder --no-pager'
ssh <vps> 'journalctl -u nginx-edge-forwarder -n 100 --no-pager'
ssh <vps> 'tail -n 20 /var/log/nginx/reverse_ssh_ingress.json'
```

The main server must run `rssh-logger` with
`docker-compose.edge-forward.yml`, and `reverse_ssh` should trust only the VPS
nginx source when real-IP headers are enabled:

```sh
docker compose -f docker-compose.yml -f docker-compose.edge-forward.yml up -d
reverse_ssh --ws-path /ws --push-path /push --trusted-proxy-cidr <vps_internal_ip>/32
```

Use the same custom paths in generated clients:

```text
link --ws-path /ws --push-path /push
```

## Rollback

```sh
ssh <vps> 'sudo systemctl disable --now nginx-edge-forwarder'
ssh <vps> 'sudo rm -f /etc/systemd/system/nginx-edge-forwarder.service'
ssh <vps> 'sudo rm -f /etc/nginx/sites-enabled/rssh-entrypoint.conf /etc/nginx/sites-available/rssh-entrypoint.conf'
ssh <vps> 'sudo systemctl reload nginx'
```
