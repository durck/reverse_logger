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

The URL must use the Docker service name `rssh-logger`; do not expose the logger endpoint publicly.

## Remove

```text
webhook --off http://rssh-logger:8080/hooks/<WEBHOOK_TOKEN>
webhook -l
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
