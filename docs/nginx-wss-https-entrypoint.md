# Nginx WSS/HTTPS VPS Entrypoint

Use this when the public VPS should look like a normal HTTPS endpoint while
forwarding `reverse_ssh` web transports to the main server over the internal
SoftEther path.

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

The central logger validates ingress payloads against these paths and rejects
wrong-path or malformed polling-key events even when the forwarding token is
valid. `INGRESS_WS_PATH` defaults to `/ws`; `INGRESS_PUSH_PATH` defaults to
`/push`.

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
- `/ws` and `/push` paths if using a patched `reverse_ssh` with custom paths

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
sudo mkdir -p /etc/reverse-logger
sudo cp deploy/systemd/nginx-edge-forwarder.env.example /etc/reverse-logger/nginx-edge-forwarder.env
sudo cp deploy/systemd/nginx-edge-forwarder.service /etc/systemd/system/nginx-edge-forwarder.service
sudo nano /etc/reverse-logger/nginx-edge-forwarder.env
sudo systemctl daemon-reload
sudo systemctl enable --now nginx-edge-forwarder
```

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
