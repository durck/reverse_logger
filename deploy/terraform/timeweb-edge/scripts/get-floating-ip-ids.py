#!/usr/bin/env python3
"""Fetch Timeweb floating IP IDs and write a local Terraform tfvars file."""

from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import sys
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any


API_URL = "https://api.timeweb.cloud/api/v1/floating-ips"
HOST_RE = re.compile(r"\bvps\d+\b")
SERVER_ADDRESS_RE = re.compile(r'twc_server\.edge\["([^"]+)"\]')


def normalize_id(value: Any) -> str | None:
    if value is None:
        return None
    if isinstance(value, float) and value.is_integer():
        return str(int(value))
    return str(value)


def load_edge_host_names(root: Path) -> set[str]:
    main_tf = root / "main.tf"
    if not main_tf.exists():
        return set()
    names: set[str] = set()
    for line in main_tf.read_text(encoding="utf-8").splitlines():
        match = re.match(r"\s*(vps\d+)\s*=", line)
        if match:
            names.add(match.group(1))
    return names


def iter_modules(module: dict[str, Any]):
    yield module
    for child in module.get("child_modules", []):
        yield from iter_modules(child)


def load_server_ids(root: Path) -> dict[str, str]:
    result = subprocess.run(
        ["terraform", "show", "-json"],
        cwd=root,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        check=False,
    )
    if result.returncode != 0:
        print("terraform show -json failed; matching by floating IP comments only", file=sys.stderr)
        return {}

    try:
        state = json.loads(result.stdout)
    except json.JSONDecodeError:
        print("terraform show -json returned invalid JSON; matching by comments only", file=sys.stderr)
        return {}

    root_module = state.get("values", {}).get("root_module", {})
    server_ids: dict[str, str] = {}
    for module in iter_modules(root_module):
        for resource in module.get("resources", []):
            if resource.get("type") != "twc_server":
                continue
            address = resource.get("address", "")
            match = SERVER_ADDRESS_RE.search(address)
            if not match:
                continue
            server_id = normalize_id(resource.get("values", {}).get("id"))
            if server_id:
                server_ids[server_id] = match.group(1)
    return server_ids


def request_floating_ips(api_url: str, token: str) -> list[dict[str, Any]]:
    request = urllib.request.Request(
        api_url,
        headers={
            "Accept": "application/json",
            "Authorization": f"Bearer {token}",
        },
    )
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            payload = json.loads(response.read().decode("utf-8"))
    except urllib.error.HTTPError as exc:
        detail = exc.read().decode("utf-8", errors="replace")
        raise SystemExit(f"Timeweb API returned HTTP {exc.code}: {detail}") from exc
    except urllib.error.URLError as exc:
        raise SystemExit(f"Timeweb API request failed: {exc}") from exc

    if isinstance(payload, list):
        return [item for item in payload if isinstance(item, dict)]
    if isinstance(payload, dict):
        for key in ("ips", "floating_ips", "data"):
            value = payload.get(key)
            if isinstance(value, list):
                return [item for item in value if isinstance(item, dict)]
    raise SystemExit("Timeweb API response does not contain a floating IP list")


def floating_ip_id(item: dict[str, Any]) -> str | None:
    return normalize_id(item.get("id") or item.get("floating_ip_id"))


def floating_ip_address(item: dict[str, Any]) -> str:
    return str(item.get("ip") or item.get("address") or "")


def floating_ip_comment(item: dict[str, Any]) -> str:
    return str(item.get("comment") or "")


def floating_ip_resource_id(item: dict[str, Any]) -> str | None:
    return normalize_id(item.get("resource_id") or item.get("resourceId"))


def floating_ip_resource_type(item: dict[str, Any]) -> str:
    return str(item.get("resource_type") or item.get("resourceType") or "").lower()


def floating_ip_zone(item: dict[str, Any]) -> str:
    return str(item.get("availability_zone") or item.get("availabilityZone") or "")


def host_from_comment(comment: str, known_hosts: set[str]) -> str | None:
    for match in HOST_RE.finditer(comment):
        host = match.group(0)
        if not known_hosts or host in known_hosts:
            return host
    return None


def match_floating_ips(
    items: list[dict[str, Any]],
    server_ids: dict[str, str],
    known_hosts: set[str],
) -> dict[str, dict[str, str]]:
    matches: dict[str, dict[str, str]] = {}
    for item in items:
        ip_id = floating_ip_id(item)
        if not ip_id:
            continue

        host = None
        source = ""
        if floating_ip_resource_type(item) == "server":
            host = server_ids.get(floating_ip_resource_id(item) or "")
            source = "server_id"
        if host is None:
            host = host_from_comment(floating_ip_comment(item), known_hosts)
            source = "comment"

        if host is None:
            continue
        if known_hosts and host not in known_hosts:
            continue

        matches[host] = {
            "id": ip_id,
            "ip": floating_ip_address(item),
            "zone": floating_ip_zone(item),
            "source": source,
        }
    return dict(sorted(matches.items()))


def hcl_quote(value: str) -> str:
    return json.dumps(value)


def write_tfvars(path: Path, matches: dict[str, dict[str, str]]) -> None:
    lines = [
        "# Generated by scripts/get-floating-ip-ids.py.",
        "# This file is local runtime state and is ignored by git.",
        "floating_ip_ids = {",
    ]
    for host, data in matches.items():
        lines.append(f"  {host} = {hcl_quote(data['id'])}")
    lines.append("}")
    lines.append("")
    path.write_text("\n".join(lines), encoding="utf-8")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--api-url", default=API_URL)
    parser.add_argument("--out", default="floating-ip-ids.auto.tfvars")
    parser.add_argument("--root", default=".")
    args = parser.parse_args()

    token = os.environ.get("TWC_TOKEN") or os.environ.get("TIMEWEB_CLOUD_TOKEN")
    if not token:
        print("Set TWC_TOKEN or TIMEWEB_CLOUD_TOKEN before running this script.", file=sys.stderr)
        return 2

    root = Path(args.root).resolve()
    known_hosts = load_edge_host_names(root)
    server_ids = load_server_ids(root)
    items = request_floating_ips(args.api_url, token)
    matches = match_floating_ips(items, server_ids, known_hosts)
    if not matches:
        print("No floating IPs matched Terraform edge hosts.", file=sys.stderr)
        return 1

    output_path = root / args.out
    write_tfvars(output_path, matches)

    print(f"Wrote {output_path}")
    print("Matched floating IPs:")
    for host, data in matches.items():
        print(f"  {host}: id={data['id']} ip={data['ip']} zone={data['zone']} source={data['source']}")
    print("")
    print("Next:")
    print("  terraform plan -out plan.out")
    print("  terraform apply plan.out")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
