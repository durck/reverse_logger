# Changelog

## [Unreleased]

- Fix Ansible link generation limits, persisted random path reuse, SSH failure
  detection, exact existing-link matching, and idempotent edge forwarder builds.
- Add an Ansible HTTP-01 preflight check before certbot to avoid burning ACME
  authorization attempts when DNS, port 80, or nginx webroot routing is wrong.
- Fix Ansible play defaults overriding `group_vars/vps_edge.yml` edge settings.
- Fix play-level Timeweb token default overriding vaulted group vars.
- Fix Timeweb DNS-01 ACME fallback availability checks for vaulted tokens.
- Add automatic Ansible HTTP-01 to Timeweb DNS-01 ACME fallback when HTTP
  certificate issuance fails and a Timeweb token is configured.
- Add optional persisted per-host random WSS, HTTPS polling, and download
  public paths for Ansible-managed VPS edges.
- Add Ansible automation for main-side `reverse_ssh link` generation after
  ready VPS edge deployments, including existing-link checks and force rotation.
- Add Timeweb DNS-01 ACME support to Ansible and manual VPS deployment docs.
- Harden ingress/webhook correlation with observed `forwarder_ip`, dual timestamp matching, configurable windows, fallback methods, and diagnostics.
- Fix nginx mirror capture to preserve original transport paths for edge forwarder spooling.
- Add public `/dl/` nginx download proxy (plain HTTP to backend) with prefix stripping.
- Document ingress correlation, mirror troubleshooting, and `VPS_INTERNAL_IP` matching.
- Add Docker listener environment variables for custom `reverse_ssh` WSS and HTTPS polling paths.
- Document the operator connection, client generation, and OpenSSH jump workflow for `reverse_ssh`.
- Add Let's Encrypt HTTP-01 automation for Ansible-managed nginx edge VPS hosts.
- Add Ansible playbook and deployment documentation for clean VPS nginx WSS/HTTPS edge rollout.
- Add nginx WSS/HTTPS VPS ingress forwarding with central raw ingress and enriched event journals.
- Add local `reverse_ssh` image build helper for clone-or-manual source workflows.
- Document clean-Ubuntu deployment steps for main and VPS hosts, with SoftEther provisioning left to external automation.
- Sanitize public examples for GitHub publication.
- Switch deployment docs and forwarding examples to SoftEther with VPS `443/tcp` kernel DNAT as the default path.
- Harden main VPN/Docker firewalling, VPS forwarding, edge proxy runtime limits, edge-event validation, and durable event storage consistency.
- Add deploy-ready Go implementation for central `rssh-logger`, optional VPS `edge-logger`, Docker Compose, firewall/proxy examples, and manual deployment docs.
