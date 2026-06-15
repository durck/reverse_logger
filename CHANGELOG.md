# Changelog

## [Unreleased]

- Add Let's Encrypt HTTP-01 automation for Ansible-managed nginx edge VPS hosts.
- Add Ansible playbook and deployment documentation for clean VPS nginx WSS/HTTPS edge rollout.
- Add nginx WSS/HTTPS VPS ingress forwarding with central raw ingress and enriched event journals.
- Add local `reverse_ssh` image build helper for clone-or-manual source workflows.
- Document clean-Ubuntu deployment steps for main and VPS hosts, with SoftEther provisioning left to external automation.
- Sanitize public examples for GitHub publication.
- Switch deployment docs and forwarding examples to SoftEther with VPS `443/tcp` kernel DNAT as the default path.
- Harden main VPN/Docker firewalling, VPS forwarding, edge proxy runtime limits, edge-event validation, and durable event storage consistency.
- Add deploy-ready Go implementation for central `rssh-logger`, optional VPS `edge-logger`, Docker Compose, firewall/proxy examples, and manual deployment docs.
