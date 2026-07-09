# Documentation

Start with the architecture document when you need to understand how the pieces
fit together. Use the runbooks when you are applying or troubleshooting a
specific deployment step.

## Map

| Document | Purpose |
| --- | --- |
| [Architecture](architecture.md) | Component map, network surfaces, data flows, correlation rules, storage model, and trust boundaries. |
| [Manual deployment](manual-deploy.md) | End-to-end clean Ubuntu deployment for main and VPS hosts. |
| [Operations](operations.md) | Health checks, durable logs, backup list, and troubleshooting. |
| [Nginx WSS/HTTPS entrypoint](nginx-wss-https-entrypoint.md) | Public VPS nginx transport, mirror capture, downloads, TLS, and forwarder behavior. |
| [reverse_ssh webhook](reverse-ssh-webhook.md) | Webhook registration, payload shapes, event normalization, and failed-attempt forwarding. |
| [SoftEther entrypoint](softether-entrypoint.md) | Raw DNAT/SNAT fallback notes. |
| [Telegram proxy](telegram-proxy.md) | Squid proxy example and Telegram smoke tests. |
| [Ansible VPS edge](../deploy/ansible/README.md) | Automated VPS edge rollout and generated client links. |
| [Timeweb edge Terraform](../deploy/terraform/timeweb-edge/README.md) | Optional VPS provisioning helpers. |

## Reading Order

1. Read [Architecture](architecture.md) to understand the normal topology and
   trust boundaries.
2. Use [Manual deployment](manual-deploy.md) for a first deployment or
   [Ansible VPS edge](../deploy/ansible/README.md) for automated VPS rollout.
3. Use [Operations](operations.md) for health checks, dashboard/API checks,
   backup, and troubleshooting.
4. Use the focused documents for the subsystem you are changing: nginx,
   webhook, SoftEther/DNAT, Telegram proxy, or Terraform provisioning.
