# Changelog

## [Unreleased]

- Add VPS edge health push-agent monitoring with central health state,
  Telegram transition alerts, dashboard status, systemd units, and Ansible
  deployment wiring.
- Add Timeweb Terraform edge provisioning with floating IP output, API-based
  floating IP discovery, and Ansible inventory documentation.
- Harden Telegram alerts with fail-fast configuration validation, token-safe
  error logging, transient send retries, delivery outbox dedupe, stable
  alert IDs, and chat delivery smoke-test docs.
- Add a token-protected read-only `rssh-logger` dashboard for enriched event
  journal summaries, filters, and recent sessions.
- Show ingress host/domain and VPS IP metadata in dashboard recent sessions.
- Add current active-session count/list and active-session history to the
  dashboard timeline.
- Redesign the dashboard layout with primary metrics, compact tables, and a
  step/area active-session chart with event markers.
- Improve the active-session chart with sub-day buckets for long windows,
  peak-active aggregation, activity focus, and a full-window overview strip.
- Render session event bars in the dashboard timeline so closed sessions do
  not leave an empty active-session chart.
- Add end-of-bucket active-session values to the dashboard timeline so the
  latest chart edge reflects the current active-session count while preserving
  bucket peak markers.
- Separate dashboard event bars from the live-session scale and hide peak
  markers for the current bucket to avoid reading historical peaks as current
  active sessions.
- Add separate `LOGGER_BIND_IP` host publishing for the central logger and
  dashboard instead of reusing `REVERSE_SSH_BIND_IP`.
- Add a pip virtualenv Certbot fallback when Snap Store setup fails during
  Ansible VPS edge deployment.
- Skip Certbot install tasks on VPS edge reruns when an existing executable
  already satisfies the selected ACME challenge requirements.
- Reduce the default Snap retry budget now that Certbot can fall back to
  pip/venv.
- Avoid Go auto-toolchain downloads during Ansible VPS edge builds and lower
  the module Go directive to the dependency-required 1.23 line.
- Add a shorter dedicated retry budget for target-side Go module downloads.
- Use a non-Google Go module proxy by default during VPS edge deployment to
  avoid target DNS failures against `golang.org` vanity imports.
- Probe Go module proxy candidates from the VPS before downloading modules and
  run the module download with async polling/trace output.
- Shorten default VPS edge retry and timeout budgets so broken target-side
  network paths fail faster during interactive deploys.
- Link `/etc/resolv.conf` to systemd-resolved early in the VPS edge playbook so
  later apt, Snap, and Go DNS checks use the runtime resolver.
- Add bounded retries and Snap Store preflight checks to Ansible VPS edge
  deployment network tasks.
- Add a main-observed source IP probe for Ansible VPS deployments and stop
  treating the VPS route source address as `VPS_INTERNAL_IP`.
- Pass explicit dns-multi propagation settings for Timeweb DNS-01 ACME and
  allow skipping target-side GitHub fetches when a checkout already exists.
- Harden Ansible ACME issuance around preflight fallback, cert/key validation,
  certbot lineage paths, and nginx reloads after renewal.
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
