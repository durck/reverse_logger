# reverse_ssh Monitoring Stack

Deploy-ready repository for monitoring `reverse_ssh` session events without performing production rollout automatically.

It contains:

- `cmd/rssh-logger`: central webhook receiver for `reverse_ssh`.
- `cmd/edge-logger`: optional VPS TCP proxy logger for entrypoint logging.
- Docker Compose for main-server services.
- systemd, SoftEther/DNAT, iptables, and Telegram proxy examples.
- manual deployment and operations documentation.

Start with [docs/manual-deploy.md](docs/manual-deploy.md).

Default deployment target:

```text
client -> VPS:443 -> SoftEther on VPS -> main internal address -> reverse_ssh
```

The main server is treated as an internal target and does not need a SoftEther
interface.
