# Operations

## Health Checks

Central logger:

```sh
docker compose exec rssh-logger wget -qO- http://127.0.0.1:8080/healthz
```

Compose:

```sh
docker compose ps
docker compose logs --tail=100 rssh-logger
docker compose logs --tail=100 reverse_ssh
```

systemd:

```sh
systemctl status rssh-monitor
journalctl -u rssh-monitor -n 100 --no-pager
```

## Durable Logs

Central session events:

```sh
tail -n 20 /opt/reverse-logger/data/logger/events.jsonl
sqlite3 /opt/reverse-logger/data/logger/events.db \
  'select status, reverse_ssh_id, host_name, ip_raw, received_at from events order by id desc limit 20;'
```

Optional VPS edge events:

```sh
tail -n 20 /var/lib/reverse-logger/edge_events.jsonl
```

Nginx ingress and enriched central events:

```sh
tail -n 20 /opt/reverse-logger/data/logger/ingress_events.jsonl
tail -n 20 /opt/reverse-logger/data/logger/enriched_events.jsonl
sqlite3 /opt/reverse-logger/data/logger/events.db \
  'select correlation_status, status, reverse_ssh_id, real_client_ip, transport, received_at from enriched_events order by id desc limit 20;'
```

## Backup

Stop writes briefly or use SQLite online backup tooling, then copy:

```text
/opt/reverse-logger/data/logger/events.db
/opt/reverse-logger/data/logger/events.jsonl
/opt/reverse-logger/data/logger/edge_events.jsonl
/opt/reverse-logger/data/logger/ingress_events.jsonl
/opt/reverse-logger/data/logger/enriched_events.jsonl
```

The JSONL files are append-only durable audit trails. SQLite is for querying
and dedupe.

## Troubleshooting

Cannot connect to the `reverse_ssh` catcher console:

1. Confirm the private key matches the public key in `SEED_AUTHORIZED_KEYS`.
2. Confirm the stack exposes `REVERSE_SSH_BIND_IP:REVERSE_SSH_BIND_PORT`.
3. From the main server, test the internal listener with:

```sh
cd /opt/reverse-logger
set -a
. ./.env
set +a
ssh -i ~/.ssh/reverse_ssh_operator -p "${REVERSE_SSH_BIND_PORT:-3232}" "$REVERSE_SSH_BIND_IP"
```

4. Do not test the operator console by running OpenSSH against the public
   WSS/HTTPS nginx endpoint; generated clients use that endpoint, not the
   operator console.

`link` download URL redirects to the decoy site:

1. Confirm nginx proxies the download prefix with a trailing slash on
   `proxy_pass`, normally `location ^~ /dl/ { proxy_pass http://<main>:3232/; }`.
   Downloads use plain HTTP to the internal multiplexed listener even when
   transport paths use `https://` to the same port.
2. Generate links with `link --name <filename>`; fetch them at
   `/dl/<filename>`.
3. Test from outside:

```sh
curl -I https://<rssh_domain>/dl/<name>
```

A missing link should return a fake nginx 404 from `reverse_ssh`, not a
`Location:` header to the decoy redirect target.

`nginx-edge-forwarder` fails with `226/NAMESPACE`:

1. Create the spool state directory:
   `sudo mkdir -p /var/lib/reverse-logger/nginx-edge-spool`
2. Install the current `deploy/systemd/nginx-edge-forwarder.service`
   (it declares `StateDirectory=` for the same paths).
3. `sudo systemctl daemon-reload && sudo systemctl restart nginx-edge-forwarder`

`ingress_events.jsonl` is empty but webhooks work:

1. On the VPS, confirm `nginx-edge-forwarder` is active and
   `curl http://127.0.0.1:18080/capture` returns 202.
2. From the VPS, confirm the main ingress URL is reachable:
   `curl http://<main_bind_ip>:8080/healthz`.
3. On main, include `docker-compose.edge-forward.yml` and set matching
   `INGRESS_WS_PATH` / `INGRESS_PUSH_PATH` / `EDGE_FORWARD_TOKEN`.
4. Confirm nginx mirror capture preserves the original transport path with
   `$rssh_mirror_path` (see `docs/nginx-wss-https-entrypoint.md`).
5. During a real client connect, check
   `/var/lib/reverse-logger/nginx-edge-spool/` on the VPS for short-lived
   `.json` files.

`enriched_events` stays `unmatched`:

1. Confirm `ingress_events.jsonl` receives events for the same session.
2. Set forwarder `VPS_INTERNAL_IP` to the address main sees in webhook
   `ip_raw` (SoftEther/VPN source), not the VPS private LAN IP.
3. Keep `RSSH_WS_PATH` / `RSSH_PUSH_PATH` aligned across nginx, forwarder,
   main `INGRESS_*`, and `REVERSE_SSH_*`.

Generated clients fail with `Unable to connect WS: bad status`:

1. Confirm `REVERSE_SSH_WS_PATH` / `REVERSE_SSH_PUSH_PATH` reach the
   `reverse_ssh` container as `RSSH_WS_PATH` / `RSSH_PUSH_PATH`
   (`docker compose config | grep RSSH_`).
2. Recreate `reverse_ssh` after `.env` changes.
3. Confirm nginx `location` paths match the baked client paths.

Webhook not received:

1. Check `webhook -l` inside `reverse_ssh`.
2. Confirm `http://rssh-logger:8080/healthz` works inside the Compose network.
3. Confirm `WEBHOOK_TOKEN` in `.env` matches the registered webhook URL.

No Telegram alert:

1. Confirm the event status is `connected` or `disconnected`.
2. Check `TELEGRAM_ENABLED=true`.
3. Smoke-test `getMe` through `TELEGRAM_PROXY_URL`.
4. Check `docker compose logs rssh-logger`.

VPS cannot forward clients:

1. Check the SoftEther session is connected.
2. From the VPS, test `REVERSE_SSH_BIND_IP:REVERSE_SSH_BIND_PORT`.
3. Confirm `deploy/iptables/vps-forward.sh` was run with the real
   `PUBLIC_IFACE`, `VPN_IFACE`, `RSSH_TARGET_IP`, and `RSSH_TARGET_PORT`.
4. Check the managed chains:

```sh
sudo iptables -t nat -S RSSH_VPS_PREROUTING
sudo iptables -t nat -S RSSH_VPS_POSTROUTING
sudo iptables -S RSSH_VPS_FORWARD
```

Webhook `ip_raw` is not the real client IP:

1. Confirm `SNAT_SOURCE_IP` is not set in the VPS forwarding command.
2. Confirm the target has a return route through the VPS for external clients.
3. If return routing is impossible, enable `SNAT_SOURCE_IP` and accept that
   `reverse_ssh` will see the VPS/VPN source address.
4. If real client IP is still required, use optional VPS-side connection logs.

Optional main ingress guard check:

```sh
sudo iptables -S RSSH_INGRESS_GUARD
sudo iptables -S INPUT | grep RSSH_INGRESS_GUARD
sudo iptables -S DOCKER-USER | grep RSSH_INGRESS_GUARD
```

The main server does not need a SoftEther interface; this guard attaches to the
normal internal ingress interface. In DNAT-only mode, do not use
`ALLOWED_SOURCE_IPS` unless SNAT is enabled.

Compromised VPS:

1. Disable or remove its SoftEther account/session.
2. Rerun the main ingress guard without that VPS if source allowlists are used.
3. Remove VPS DNAT/firewall rules.
4. Rotate `EDGE_FORWARD_TOKEN` if it was used by that VPS.
5. Rotate proxy credentials if that VPS hosted the Telegram proxy.
