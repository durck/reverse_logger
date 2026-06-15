# reverse_ssh Monitoring Stack

Deploy-ready repository for monitoring `reverse_ssh` session events without performing production rollout automatically.

It contains:

- `cmd/rssh-logger`: central webhook receiver for `reverse_ssh`.
- `cmd/edge-logger`: optional VPS TCP proxy logger for entrypoint logging.
- Docker Compose for main-server services.
- Local `reverse_ssh` image build helper that can clone a repository or use a
  manually prepared checkout.
- systemd, SoftEther/DNAT, iptables, Ansible, and Telegram proxy examples.
- nginx WSS/HTTPS VPS entrypoint with central ingress correlation.
- manual deployment and operations documentation.

Start with [docs/manual-deploy.md](docs/manual-deploy.md).

Default deployment target:

```text
client -> VPS:443 -> SoftEther on VPS -> main internal address -> reverse_ssh
```

The main server is treated as an internal target and does not need a SoftEther
interface.

For HTTPS-looking public entrypoints, see
[docs/nginx-wss-https-entrypoint.md](docs/nginx-wss-https-entrypoint.md).
For automated nginx edge rollout on a clean VPS, see
[deploy/ansible/README.md](deploy/ansible/README.md).
