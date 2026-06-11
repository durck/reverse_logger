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
