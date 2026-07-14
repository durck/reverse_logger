# ADR 0001: Scalable Ansible Edge Rollout

- Status: Accepted
- Date: 2026-07-13
- Owners: reverse_logger maintainers

## Context

The current `vps-edge.yml` playbook owns the complete VPS lifecycle and builds
two Go binaries on every target when the source revision changes. For `N` VPS
hosts and `A` target architectures, this performs `2 * N` builds even though
only `2 * A` distinct binaries are required. The combined edge-and-links
playbook also does not provide a durable success barrier between fleet rollout
and main-side link publication.

The primary non-functional requirement is operational scalability: adding VPS
hosts must only require inventory changes, a normal deployment must have one
entry point, and elapsed time must grow mainly with the slowest batch rather
than with the number of hosts.

## Decision drivers

- One command must build, roll out, verify, and then publish links.
- A source revision is built once per target architecture, never once per VPS.
- A failed canary or batch must stop the rollout and prevent link publication.
- The active release is promoted only after service health checks pass.
- Rollback must switch to an already present release without rebuilding.
- Existing source-on-target deployment remains available temporarily as a
  compatibility path, but it is not the scalable default workflow.
- The design must remain understandable and operable without introducing a
  separate deployment service.

## Considered alternatives

### 1. Continue optimizing the monolithic playbook

Keep source checkout and compilation on every VPS, with more commit markers and
task tags. This is the smallest change, but build and dependency-download work
still grows linearly with fleet size. It also keeps rollout, TLS, service
configuration, verification, and link publication tightly coupled.

### 2. Modular Ansible with controller-built immutable artifacts

Build versioned Linux artifacts once per architecture on the Ansible controller,
copy and checksum them in parallel, switch a content-addressed release symlink,
verify the batch,
and publish links only after the rollout command succeeds. This removes the
dominant `N`-scaled build work while preserving the current operational model.

### 3. Introduce a dedicated deployment controller

Move release state, rollout waves, health approval, and rollback to a separate
service. This could provide richer fleet scheduling later, but it adds another
stateful control plane, authentication surface, and availability dependency.
The present fleet does not justify that complexity.

## Decision

Adopt alternative 2. Keep Ansible as the orchestrator and introduce the
following module boundaries incrementally:

```text
deploy/ansible/
  deploy_edge.py                 # one-command lock and success barrier
  playbooks/
    edge-rollout.yml             # build + rolling VPS deployment
    links-publish.yml            # explicit post-rollout publication
    edge-rollback.yml            # release pointer rollback
  roles/
    reverse_logger_artifact_build/ # controller-only build/cache
    reverse_logger_artifact/       # target release install/promote/rollback
    edge_verify/                   # target service and listener health gate
```

The remaining network, TLS, nginx, forwarder, and health responsibilities will
move out of `vps-edge.yml` in later behavior-preserving slices. Empty placeholder
roles are not created.

## Module contracts

### Artifact build

Inputs:

- repository Go build inputs (`go.mod`, `go.sum`, `cmd/`, and `internal/`);
- an immutable source revision or an explicitly allowed development override;
- a list of Linux architectures such as `amd64` and `arm64`;
- Go checksum/proxy policy.

Outputs:

- one directory per source revision and architecture;
- `nginx-edge-forwarder` and `edge-health` binaries;
- SHA-256 checksums and a JSON manifest;
- facts that let target hosts locate the cached artifact.

The build directory is controller-local deployment state and is not committed.

### Artifact install

Inputs are the artifact version, architecture, local directory, and remote
release root. The role copies into a versioned, binary-digest-addressed release
directory, verifies SHA-256 on the target, writes a `pending.json` manifest,
and atomically switches the `current` release symlink. It does not promote the
release.

### Edge verification

The verifier runs after handlers and service enablement. It checks nginx syntax,
required systemd services, and required local listeners. Success promotes
`pending.json` to `active.json` while preserving the old active manifest as
`previous.json`. Failure switches back to the still-active release when one
exists and fails the batch.

### Orchestrator

The one-command entry point takes a local deployment lock and executes:

```text
artifact build -> canary -> 25% batch -> remaining fleet -> link publication
```

The link playbook starts only when the rollout process exits successfully.
Check mode never publishes links. The same CLI arguments and inventory limit
are forwarded to both phases where applicable.

## Rollout and performance model

The default batch sequence is `[1, "25%", "100%"]` with
`max_fail_percentage: 0`. This keeps a deterministic canary, exposes issues on a
bounded subset, and then finishes the remaining healthy fleet in parallel.
Ansible forks cap simultaneous SSH work; adding hosts does not add controller
builds.

For 100 same-architecture VPS hosts:

| Metric | Previous flow | New flow |
| --- | ---: | ---: |
| Go binary builds per new revision | 200 | 2 |
| Sequential rollout waves | 5 | 3 |
| Target Go toolchains required | 100 | 0 |

Wall-clock timing still depends on SSH, apt, ACME, and network conditions. The
operation-count baseline is reproducible and will be protected by tests; real
fleet p50/p95 deployment time should be recorded by the operator after rollout.

## Rollback

Every successful promotion retains `previous.json` and the corresponding
release directory. `edge-rollback.yml` switches `current` to that release,
restarts the affected services, verifies health, and swaps active/previous
manifests. Artifact garbage collection is intentionally deferred until a safe
retention policy exists.

## Consequences

Positive:

- build and dependency-download cost scales with architectures, not hosts;
- every VPS receives checksum-identical binaries;
- a release can be rolled back without Git, Go, or external module proxies;
- link publication has an explicit fleet-success barrier;
- role boundaries can be extracted gradually from the existing playbook.

Negative:

- the controller requires a supported Go toolchain until CI publishes artifacts;
- controller artifact cache consumes disk and needs a future retention policy;
- the compatibility source-build path remains duplicated during migration;
- first-time provisioning can still be dominated by apt, Snap, and ACME.

## Follow-up decisions

- Move artifact production from the operator workstation to CI with checksums,
  SBOM, and signatures.
- Extract TLS/nginx into transactional roles that validate staged files before
  switching live configuration.
- Define real-fleet deployment-duration SLOs after representative measurements.
