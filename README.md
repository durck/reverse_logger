# reverse_ssh Monitoring Stack

Deploy-ready monitoring and edge-entrypoint toolkit for `reverse_ssh` session
events. It does not roll production out automatically; it provides the binaries,
Docker Compose stack, systemd units, nginx templates, Ansible automation, and
runbooks needed to deploy deliberately.

Default WSS/HTTPS topology:

```text
generated client -> VPS nginx :443 -> internal route -> main reverse_ssh :3232
                                      -> nginx mirror -> central rssh-logger :8080
reverse_ssh webhook ------------------------------^
rssh-session-reconciler -> reverse_ssh console -> session snapshots --------^
```

The main server is treated as an internal target and does not need a SoftEther
interface. A VPS accepts the public endpoint, proxies configured transport paths
to the main `reverse_ssh` listener, and forwards ingress metadata to the central
logger for correlation.

## Components

| Component | Purpose |
| --- | --- |
| `cmd/rssh-logger` | Central webhook, ingress, health, dashboard, SQLite/JSONL, and Telegram alert service. |
| `cmd/rssh-session-reconciler` | Docker sidecar that polls `reverse_ssh ls` and posts live session snapshots for accurate dashboard active sessions. |
| `cmd/nginx-edge-forwarder` | VPS loopback receiver for nginx mirror metadata with spool-and-flush forwarding. |
| `cmd/edge-health` | VPS push-agent for main listener, logger, VPN interface, and local service checks. |
| `cmd/rssh-error-forwarder` | Journal or `docker logs` forwarder for failed `reverse_ssh` connection attempts. |
| `cmd/edge-logger` | Optional TCP proxy logger for non-nginx fallback paths. |
| `deploy/nginx/` | Public WSS/HTTPS, download, decoy, and ACME nginx templates. |
| `deploy/ansible/` | Automated clean-VPS nginx edge rollout and generated client links. |
| `deploy/terraform/timeweb-edge/` | Optional Timeweb edge provisioning helpers. |

## Documentation

Start with:

- [Documentation map](docs/README.md)
- [Architecture](docs/architecture.md)
- [Manual deployment](docs/manual-deploy.md)
- [Operations](docs/operations.md)
- [Management brief (RU)](docs/management-brief-ru.md)

Focused guides:

- [Nginx WSS/HTTPS VPS entrypoint](docs/nginx-wss-https-entrypoint.md)
- [reverse_ssh webhook](docs/reverse-ssh-webhook.md)
- [SoftEther/DNAT fallback](docs/softether-entrypoint.md)
- [Main server firewall](docs/firewall.md)
- [Telegram proxy](docs/telegram-proxy.md)
- [Automated nginx edge rollout](deploy/ansible/README.md)

## Quick Start

```sh
cp .env.example .env
docker compose build rssh-logger rssh-session-reconciler
docker compose -f docker-compose.yml -f docker-compose.edge-forward.yml up -d
```

Before using this in a real deployment, fill `.env`, build or provide the
`reverse_ssh` image, create the reconciler console key referenced by
`RSSH_SESSION_CONSOLE_KEY_PATH`, bind the main listener to an internal address,
and protect the published logger port with firewall rules or SSH tunneling. The
full, checked sequence is in [docs/manual-deploy.md](docs/manual-deploy.md).
