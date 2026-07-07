# Timeweb Edge Terraform

This directory provisions the Timeweb Cloud VPS edge fleet used by the Ansible
VPS edge deployment.

The configuration creates `twc_server.edge` instances for `vps13` through
`vps24`, prepares root SSH access through cloud-init, and exposes an
`inventory_hosts` output that can be copied into `deploy/ansible/inventory.ini`
or a YAML inventory.

## Floating IP model

The module supports two floating IP flows:

- Terraform-managed IPs: set `create_floating_ip_hosts` for host names that
  Terraform should create as `twc_floating_ip.edge`.
- Panel/API-created IPs: set `floating_ip_ids` to existing Timeweb floating IP
  IDs. These are read with `data "twc_floating_ip"` and passed to
  `twc_server.edge[*].floating_ip_id`, but Terraform does not own or delete
  the IP resource.

`inventory_hosts[*].ansible_host` is resolved in this order:

1. Terraform-managed `twc_floating_ip.edge[*].ip`
2. existing `data.twc_floating_ip.manual[*].ip`
3. server `main_ipv4`

This keeps the Ansible inventory stable even when a host has a floating IP.

Timeweb's provider also supports binding a floating IP with a nested
`resource { type = "server" }` block, but the server-level `floating_ip_id`
attribute is the better fit here because cloud-init performs package downloads
during server creation.

## Secrets and local state

Do not commit:

- `terraform.tfstate` or backups
- `*.tfvars`
- `plan.out` / `*.tfplan`
- Timeweb API tokens

The state can include sensitive generated passwords because
`is_root_password_required = true` is enabled. Keep state local/private, or move
it to a protected remote backend before sharing this workflow.

## First run

From this directory:

```sh
export TWC_TOKEN='<Timeweb Cloud API token>'
export TF_VAR_ssh_public_key="$(cat ~/.ssh/id_rsa.pub)"

terraform init
terraform fmt
terraform validate
terraform plan -out plan.out
terraform apply plan.out
```

By default, `create_floating_ip_hosts` is empty. That means the first run creates
servers only and falls back to `main_ipv4` in `inventory_hosts`. This avoids
accidentally consuming the daily floating IP limit.

To let Terraform create floating IPs for selected hosts:

```sh
cat > floating-ips.auto.tfvars <<'EOF'
create_floating_ip_hosts = [
  "vps13",
  "vps14",
]
EOF

terraform plan -out plan.out
terraform apply plan.out
```

## Use floating IPs created in the panel

Create floating IPs in the Timeweb panel and add comments that include the host
name, for example `reverse_logger vps13`. If the IP is already attached to a
server, the helper can also match by the server ID stored in Terraform state.

Then run:

```sh
export TWC_TOKEN='<Timeweb Cloud API token>'
python3 scripts/get-floating-ip-ids.py

terraform plan -out plan.out
terraform apply plan.out
```

The helper calls `GET /api/v1/floating-ips`, writes
`floating-ip-ids.auto.tfvars`, and prints the matched host/IP table. The tfvars
file is ignored by git.

If you prefer to create the file manually:

```hcl
floating_ip_ids = {
  vps13 = "968769c4-06ac-4b37-8e21-1fc8ff6c5a9b"
  vps14 = "..."
}
```

Inspect the plan before applying. If the provider needs to replace an existing
server when adding `floating_ip_id`, stop and decide whether to recreate that
host or keep using `main_ipv4`.

## Regions and presets

`preset_id = 4799` is used only for `msk-1` hosts. Non-Moscow hosts use
`data.twc_configurator.edge` with `preset_type = "premium"` because the same
preset ID may be unavailable in `spb-3` or `nsk-1`.

Location mapping used in this file:

| Zone | Location |
| --- | --- |
| `spb-3` | `ru-1` |
| `nsk-1` | `ru-2` |
| `msk-1` | `ru-3` |

If Timeweb returns `No free node`, change the host's `zone` / `location` pair
or reduce the fleet and retry. If the error is `not enough money`, do not rerun
until the account balance is fixed; repeated runs will only create partial
state.

## Inventory output

Print the Terraform output:

```sh
terraform output -json inventory_hosts > /tmp/timeweb-edge-hosts.json
```

Minimal INI form:

```ini
[vps_edge:children]
edge_group_1
edge_group_2
edge_group_3
edge_group_4

[edge_group_1]
vps13 ansible_host=<from output> rssh_domain=vps13.example.invalid
```

Replace `*.example.invalid` placeholders with real DNS names before requesting
ACME certificates through Ansible.

The output also includes `server_id`, `floating_ip_id`, `vpn_ip`, and `zone` so
you can audit the panel against Terraform without reading state.
