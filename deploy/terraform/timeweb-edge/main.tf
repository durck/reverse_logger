terraform {
  required_providers {
    twc = {
      source = "tf.timeweb.cloud/timeweb-cloud/timeweb-cloud"
    }
  }
  required_version = ">= 1.4.4"
}

provider "twc" {}

variable "project_id" {
  type    = number
  default = 2696703
}

variable "preset_id" {
  type    = number
  default = 4799
}

variable "os_id" {
  type    = number
  default = 109
}

variable "ssh_public_key" {
  type = string
}

variable "create_floating_ip_hosts" {
  type        = set(string)
  default     = []
  description = "Host names for which Terraform should create and manage floating IPs."
}

variable "floating_ip_ids" {
  type        = map(string)
  default     = {}
  description = "Existing Timeweb floating IP IDs keyed by host name. These are read through data sources and are not destroyed by this module."
}

locals {
  edge_hosts = {
    vps13 = { group = "edge_group_1", zone = "msk-1", vpn_ip = "192.168.30.82", location = "ru-3", domain = "vps13.example.invalid" }
    vps14 = { group = "edge_group_1", zone = "msk-1", vpn_ip = "192.168.30.83", location = "ru-3", domain = "vps14.example.invalid" }
    vps15 = { group = "edge_group_1", zone = "nsk-1", vpn_ip = "192.168.30.84", location = "ru-2", domain = "vps15.example.invalid" }

    vps16 = { group = "edge_group_2", zone = "msk-1", vpn_ip = "192.168.30.85", location = "ru-3", domain = "vps16.example.invalid" }
    vps17 = { group = "edge_group_2", zone = "msk-1", vpn_ip = "192.168.30.86", location = "ru-3", domain = "vps17.example.invalid" }
    vps18 = { group = "edge_group_2", zone = "nsk-1", vpn_ip = "192.168.30.87", location = "ru-2", domain = "vps18.example.invalid" }

    vps19 = { group = "edge_group_3", zone = "msk-1", vpn_ip = "192.168.30.88", location = "ru-3", domain = "vps19.example.invalid" }
    vps20 = { group = "edge_group_3", zone = "msk-1", vpn_ip = "192.168.30.89", location = "ru-3", domain = "vps20.example.invalid" }
    vps21 = { group = "edge_group_3", zone = "nsk-1", vpn_ip = "192.168.30.90", location = "ru-2", domain = "vps21.example.invalid" }

    vps22 = { group = "edge_group_4", zone = "msk-1", vpn_ip = "192.168.30.91", location = "ru-3", domain = "vps22.example.invalid" }
    vps23 = { group = "edge_group_4", zone = "msk-1", vpn_ip = "192.168.30.92", location = "ru-3", domain = "vps23.example.invalid" }
    vps24 = { group = "edge_group_4", zone = "nsk-1", vpn_ip = "192.168.30.93", location = "ru-2", domain = "vps24.example.invalid" }
  }

  managed_floating_ip_hosts = {
    for name in var.create_floating_ip_hosts : name => local.edge_hosts[name]
  }
}

data "twc_configurator" "edge" {
  for_each = {
    for name, cfg in local.edge_hosts : name => cfg
    if cfg.zone != "msk-1"
  }

  location    = each.value.location
  preset_type = "premium"
}

data "twc_floating_ip" "manual" {
  for_each = var.floating_ip_ids

  id = each.value
}

resource "twc_floating_ip" "edge" {
  for_each = local.managed_floating_ip_hosts

  availability_zone = each.value.zone
  ddos_guard        = false
  comment           = "reverse_logger ${each.key}"
}

resource "twc_server" "edge" {
  for_each                  = local.edge_hosts
  name                      = each.key
  project_id                = var.project_id
  os_id                     = var.os_id
  availability_zone         = each.value.zone
  is_root_password_required = true
  cloud_init                = templatefile("${path.module}/cloud-init.sh.tftpl", { ssh_public_key = var.ssh_public_key })
  floating_ip_id            = try(twc_floating_ip.edge[each.key].id, data.twc_floating_ip.manual[each.key].id, null)
  preset_id                 = each.value.zone == "msk-1" ? var.preset_id : null

  dynamic "configuration" {
    for_each = each.value.zone == "msk-1" ? [] : [each.key]

    content {
      configurator_id = data.twc_configurator.edge[configuration.value].id
      disk            = 1024 * 15
      cpu             = 1
      ram             = 1024
    }
  }
}

output "inventory_hosts" {
  value = {
    for name, cfg in local.edge_hosts : name => {
      group = cfg.group
      ansible_host = coalesce(
        try(twc_floating_ip.edge[name].ip, null),
        try(data.twc_floating_ip.manual[name].ip, null),
        try(twc_server.edge[name].main_ipv4, null)
      )
      floating_ip_id = try(twc_floating_ip.edge[name].id, data.twc_floating_ip.manual[name].id, null)
      rssh_domain    = cfg.domain
      server_id      = twc_server.edge[name].id
      vpn_ip         = cfg.vpn_ip
      zone           = cfg.zone
    }
  }
}
