# Operations

## Health Checks

Central logger:

```sh
docker compose exec rssh-logger wget -qO- http://127.0.0.1:8080/healthz
```

VPS edge health overview, when `DASHBOARD_TOKEN` is set:

```sh
set -a
. /opt/reverse-logger/.env
set +a

curl -H "Authorization: Bearer ${DASHBOARD_TOKEN}" \
  "http://${LOGGER_BIND_IP:-127.0.0.1}:${LOGGER_BIND_PORT:-8080}/dashboard/api/edge-health"
```

On each VPS:

```sh
systemctl status edge-health --no-pager
journalctl -u edge-health -n 100 --no-pager
```

Dashboard API, when `DASHBOARD_TOKEN` is set:

```sh
set -a
. /opt/reverse-logger/.env
set +a

curl -H "Authorization: Bearer ${DASHBOARD_TOKEN}" \
  "http://${LOGGER_BIND_IP:-127.0.0.1}:${LOGGER_BIND_PORT:-8080}/dashboard/api/overview?window=24h"
```

Browser access uses:

```text
http://<LOGGER_BIND_IP>:<LOGGER_BIND_PORT>/dashboard/
```

Use HTTP Basic Auth with any username and `DASHBOARD_TOKEN` as the password.
If `LOGGER_BIND_IP=127.0.0.1`, open the page through an SSH tunnel.

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
systemctl status rssh-error-forwarder
journalctl -u rssh-error-forwarder -n 100 --no-pager
systemctl status edge-health
journalctl -u edge-health -n 100 --no-pager
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
  'select correlation_status, correlation_method, status, reverse_ssh_id, real_client_ip, transport, forwarder_ip, received_at from enriched_events order by id desc limit 20;'
```

Failed `reverse_ssh` connection attempts forwarded from journals:

```sh
tail -n 20 /opt/reverse-logger/data/logger/reverse_ssh_errors.jsonl
sqlite3 /opt/reverse-logger/data/logger/events.db \
  'select severity, reason, remote_addr, message, observed_at from reverse_ssh_errors order by id desc limit 20;'
```

The dashboard `/dashboard/connections` page shows these rows together with raw
ingress events. Use the page filters to hide `info` noise; the API endpoint is
`/dashboard/api/system-events?window=24h&severity=not_info`.

`correlation_method` explains how an ingress event was selected:

- `vps-or-forwarder-ip`: webhook `ip_raw` matched either `vps_internal_ip`,
  `vps_public_ip`, or the central logger's observed `forwarder_ip`.
- `trusted-proxy`: webhook `ProxySourceIP` matched a VPS/forwarder address and
  webhook `IP` matched ingress `client_ip`.
- `trusted-proxy-client-ip-fallback`: VPS address metadata was missing or
  wrong, but exactly one ingress candidate matched the trusted-proxy real
  `client_ip` inside the configured window.
- `unique-time-fallback`: VPS address metadata was unusable or contradicted
  webhook proxy metadata, but exactly one unclaimed ingress candidate existed
  in the configured time window.
- `connected-history`: a `disconnected` webhook inherited the matched ingress
  metadata from the previous `connected` event with the same `reverse_ssh_id`.

When several ingress candidates match the same method, the logger prefers a
candidate whose timestamp is clearly nearest to the webhook timestamp. It keeps
`ambiguous` when candidates are close enough that selecting one would be a
guess.

The logger records `forwarder_ip` from the HTTP source address of the
`/ingress-events` request. This often recovers correlation when
`VPS_INTERNAL_IP` is empty or set to the wrong VPS-side address.
From the VPS, call `/edge/source-ip/<EDGE_FORWARD_TOKEN>` on the main logger to
detect the source IP that main actually observes.

## Backup

Stop writes briefly or use SQLite online backup tooling, then copy:

```text
/opt/reverse-logger/data/logger/events.db
/opt/reverse-logger/data/logger/events.jsonl
/opt/reverse-logger/data/logger/edge_events.jsonl
/opt/reverse-logger/data/logger/ingress_events.jsonl
/opt/reverse-logger/data/logger/reverse_ssh_errors.jsonl
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

Large `link` download stops early (`curl: (18)`, partial file size):

1. Confirm `/dl/` sets `proxy_buffering off` and uses plain HTTP upstream:
   `proxy_pass http://<main>:3232/;`.
2. Compare direct backend size with the public URL:
   `curl -o /tmp/main http://<main>:3232/<name>` versus
   `curl -o /tmp/main2 https://<rssh_domain>/dl/<name>`.
3. Check `/var/log/nginx/error.log` for
   `upstream prematurely closed connection while reading upstream`.

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
2. Check `correlation_method` and `forwarder_ip` in `enriched_events`.
3. Set forwarder `VPS_INTERNAL_IP` to the address main sees in webhook
   `ip_raw` (SoftEther/VPN source), not the VPS private LAN IP. From the VPS,
   call `/edge/source-ip/<EDGE_FORWARD_TOKEN>` on the main logger to detect it.
   If this value is missing or wrong, central `forwarder_ip` is tried
   automatically.
4. Keep `RSSH_WS_PATH` / `RSSH_PUSH_PATH` aligned across nginx, forwarder,
   main `INGRESS_*`, and `REVERSE_SSH_*`.
5. HTTPS polling has a wider built-in match window because the `HEAD` ingress
   probe can arrive minutes before the reverse_ssh webhook. If clocks or
   forwarding are delayed beyond that, increase:

```text
CORRELATION_WEBHOOK_MATCH_BEFORE=5m
CORRELATION_WEBHOOK_MATCH_AFTER=1m
CORRELATION_INGRESS_RECONCILE_BEFORE=1m
CORRELATION_INGRESS_RECONCILE_AFTER=5m
```

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

Dashboard not reachable:

1. A `404` on `/dashboard/` means `DASHBOARD_TOKEN` is empty or the running
   container was not recreated after `.env` changed.
2. A `401` means the dashboard is enabled but the browser/API did not send the
   correct token. Browser password is `DASHBOARD_TOKEN`; curl may use
   `Authorization: Bearer <DASHBOARD_TOKEN>`.
3. Confirm the port is published with `docker compose ps`. Host access requires
   `docker-compose.edge-forward.yml`; inside the Compose network, use
   `http://rssh-logger:8080/dashboard/`.
4. If `LOGGER_BIND_IP` is not `127.0.0.1`, confirm firewall allowlists include
   your operator IP/VPN source and required VPS edge sources.
5. Do not route `/dashboard` through the public VPS nginx reverse_ssh endpoint.

Dashboard shows stale active sessions:

1. `Active sessions` is derived from `reverse_ssh` connect/disconnect
   webhooks. If a disconnect webhook is never delivered, the session is treated
   as active until it becomes stale.
2. The default stale cutoff is `DASHBOARD_ACTIVE_SESSION_MAX_AGE=1h`.
   Increase it for intentionally long-lived sessions, or set `0s` to keep the
   legacy "active until disconnected" behavior.
3. Recreate `rssh-logger` after changing this value.

No Telegram alert:

1. For session webhooks, confirm the event status is `connected` or
   `disconnected`.
2. For failed attempt alerts, confirm `rssh-error-forwarder` is running and
   `reverse_ssh_errors.jsonl` receives rows.
3. For edge health, a `degraded` Telegram alert is sent only after two
   consecutive degraded reports from the same VPS. A single failed
   `logger_health` check is kept in the dashboard but does not alert.
4. Check `TELEGRAM_ENABLED=true`.
5. Check that `TELEGRAM_BOT_TOKEN` and `TELEGRAM_CHAT_IDS` are non-empty.
6. If `TELEGRAM_ALERT_MODE=rich`, confirm the upstream Bot API supports
   `sendRichMessage`; unsupported-method responses fall back to HTML alerts.
7. Smoke-test `getMe` through `TELEGRAM_PROXY_URL`.
8. Smoke-test `sendMessage` to the first configured chat ID; `getMe` does not
   prove the bot can write to that chat. Use a temporary `curl --config` file
   as shown in `telegram-proxy.md` so tokens and proxy credentials are not
   exposed in process arguments.
9. Check `docker compose logs rssh-logger` and
   `journalctl -u rssh-error-forwarder -n 100 --no-pager`.

No VPS health alert:

1. Confirm `EDGE_HEALTH_TOKEN` is set on main and matches the VPS
   `/etc/reverse-logger/edge-health.env`.
2. Confirm the VPS service is active:
   `systemctl is-active edge-health`.
3. From the VPS, test the main logger health endpoint:
   `curl http://<main_bind_ip>:8080/healthz`.
4. From the VPS, test the reverse_ssh listener:
   `nc -vz <main_bind_ip> <reverse_ssh_port>`.
5. Check `/dashboard/api/edge-health` for `status`, `failed_checks`,
   `last_seen_at`, and `stale_after`.

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
