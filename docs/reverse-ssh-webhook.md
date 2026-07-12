# reverse_ssh Webhook

`rssh-logger` uses the built-in `reverse_ssh` webhook command as the primary event source.

The logger accepts:

- raw `ClientState` JSON:

```json
{
  "Status": "connected",
  "ID": "client-id",
  "IP": "203.0.113.10:50000",
  "HostName": "user.computer",
  "Version": "v1",
  "Timestamp": "2026-06-09T12:00:00Z"
}
```

- wrapper payload emitted by current `reverse_ssh` webhook code:

```json
{
  "Full": "{\"Status\":\"connected\",\"ID\":\"client-id\",\"IP\":\"203.0.113.10:50000\",\"HostName\":\"user.computer\",\"Version\":\"v1\",\"Timestamp\":\"2026-06-09T12:00:00Z\"}",
  "text": "user.computer client-id v1 connected"
}
```

## Register

Inside the `reverse_ssh` server console:

```text
webhook --on http://rssh-logger:8080/hooks/<WEBHOOK_TOKEN>
webhook -l
```

The URL must use the Docker service name `rssh-logger` and the container port
`8080`. Do not register the main server's public or host-published address here.
A container-to-host public-IP call may fail because of firewall policy or
hairpin routing even while VPS forwarders can reach that address successfully.

The host-published logger address has a different purpose: it accepts ingress
and health reports from allowlisted VPS edges. The lifecycle webhook stays on
the private Compose network:

```text
reverse_ssh container -> http://rssh-logger:8080/hooks/<token>
VPS forwarder         -> http://<main-bind-ip>:8080/ingress-events/<token>
```

Confirm private reachability from the actual caller container:

```sh
docker compose exec reverse_ssh sh -lc \
  'wget -qO- http://rssh-logger:8080/healthz || curl -fsS http://rssh-logger:8080/healthz'
```

Recheck `webhook -l` after every `reverse_ssh` image/container replacement.
Treat registration as runtime state unless the deployed `reverse_ssh` version
explicitly guarantees persistence. A live session snapshot proves that the
agent is connected, but it does not prove that lifecycle webhooks are enabled.

## Remove

```text
webhook --off http://rssh-logger:8080/hooks/<WEBHOOK_TOKEN>
webhook -l
```

## Rotate a Disclosed Token

The token is part of the webhook URL. Redact it from screenshots, shell output,
and tickets. If it is disclosed, generate a new value, update `.env`, recreate
only `rssh-logger` with the complete Compose file set, and replace the
registered URL:

```sh
cd /opt/reverse-logger
openssl rand -hex 32
nano .env

docker compose \
  --env-file .env \
  -f docker-compose.yml \
  -f docker-compose.edge-forward.yml \
  up -d --no-deps --force-recreate rssh-logger
```

Then, in the catcher console:

```text
webhook --off <old-url-from-webhook-list>
webhook --on http://rssh-logger:8080/hooks/<NEW_WEBHOOK_TOKEN>
webhook -l
```

Fully reconnect a test agent after changing the webhook. An already connected
agent does not emit another `connected` event merely because registration was
fixed.

## Verify Delivery and Correlation

An ingress row and a reconciler `live=1` snapshot without a contemporary row
in `events` means the lifecycle webhook did not arrive; the correlation engine
has nothing to match. The dashboard may still show a synthetic `reconciled`
session with empty client/network/ingress fields.

```sh
sqlite3 "${LOGGER_DATA_DIR:-./data/logger}/events.db" <<'SQL'
.headers on
.mode column
SELECT id, status, reverse_ssh_id, host_name, ip_raw, transport, received_at
FROM events ORDER BY id DESC LIMIT 10;

SELECT id, transport, client_ip, vps_name, forwarder_ip, uri, forwarded_at
FROM ingress_events ORDER BY id DESC LIMIT 10;

SELECT id, checked_at, live_count
FROM session_snapshots ORDER BY id DESC LIMIT 10;
SQL
```

When Telegram is enabled, delivery markers distinguish missing webhooks from
Bot API failures:

```sh
sqlite3 "${LOGGER_DATA_DIR:-./data/logger}/events.db" \
  'SELECT event_hash, chat_id, status, attempts, last_error, updated_at FROM telegram_deliveries ORDER BY updated_at DESC LIMIT 10;'
```

## Event Normalization

The logger stores:

- `status`
- `reverse_ssh_id`
- `host_name`
- `user_name`
- `computer_name`
- `ip_raw`
- `ip_addr`
- `ip_port`
- `version`
- `source_ts`
- `received_at`
- `raw_json`

`HostName` is split at the first dot. For `alice.workstation.lab`, `user_name=alice` and `computer_name=workstation.lab`.

## Failed Attempt Journal Events

Some failed connection attempts never become `connected`/`disconnected`
webhook events. Run `rssh-error-forwarder` on the main host to read
`reverse_ssh` journal lines, classify failures such as fingerprint mismatch,
invalid certificate/x509, TLS handshake failure, auth failure, or generic
connection errors, and POST them to:

```text
http://rssh-logger:8080/reverse-ssh-errors/<EDGE_FORWARD_TOKEN>
```

The central logger stores these in `reverse_ssh_errors`, appends
`reverse_ssh_errors.jsonl`, shows them on `/dashboard/connections`, and sends
Telegram alerts when Telegram is enabled.
