#!/usr/bin/env python3
"""Generate reverse_ssh links from an Ansible-produced JSON specification."""

from __future__ import annotations

import argparse
import json
import os
import re
import shlex
import subprocess
import sys
import time
from pathlib import Path
from typing import Any


SAFE_TRANSPORTS = {"wss", "https"}
SAFE_TOKEN = re.compile(r"^[A-Za-z0-9_.:@/+={}\-]+$")


def fail(message: str) -> None:
    print(f"ERROR: {message}", file=sys.stderr)
    raise SystemExit(2)


def read_spec(path: Path) -> dict[str, Any]:
    with path.open("r", encoding="utf-8") as fh:
        data = json.load(fh)
    if not isinstance(data, dict):
        fail("spec root must be a JSON object")
    return data


def require_string(value: Any, field: str) -> str:
    if not isinstance(value, str) or not value.strip():
        fail(f"{field} must be a non-empty string")
    return value.strip()


def optional_string(value: Any, field: str, default: str = "") -> str:
    if value is None:
        return default
    if not isinstance(value, str):
        fail(f"{field} must be a string")
    return value.strip()


def shell_quote_args(args: list[str]) -> str:
    return shlex.join(args)


def console_quote(value: str) -> str:
    if SAFE_TOKEN.match(value):
        return value
    return shlex.quote(value)


def build_link_name(template: str, context: dict[str, str]) -> str:
    try:
        name = template.format(**context)
    except KeyError as exc:
        fail(f"unknown placeholder in reverse_ssh_link_name_template: {exc}")
    name = name.strip()
    if not name:
        fail("generated link name is empty")
    if not SAFE_TOKEN.match(name):
        fail(f"generated link name contains unsafe characters: {name!r}")
    return name


def build_create_command(
    edge: dict[str, Any],
    transport: str,
    platform: dict[str, Any],
    options: dict[str, Any],
) -> tuple[str, str]:
    if transport not in SAFE_TRANSPORTS:
        fail(f"unsupported transport {transport!r}; expected one of {sorted(SAFE_TRANSPORTS)}")

    domain = require_string(edge.get("domain"), "edge.domain")
    ws_path = require_string(edge.get("ws_path"), "edge.ws_path")
    push_path = require_string(edge.get("push_path"), "edge.push_path")
    vps_host = require_string(edge.get("vps_host"), "edge.vps_host")
    vps_name = optional_string(edge.get("vps_name"), "edge.vps_name", vps_host)
    goos = require_string(platform.get("goos"), "platform.goos")
    goarch = require_string(platform.get("goarch"), "platform.goarch")
    external_port = int(options.get("external_port", 443))
    name_template = require_string(options.get("name_template"), "options.name_template")

    context = {
        "domain": domain,
        "transport": transport,
        "goos": goos,
        "goarch": goarch,
        "vps_host": vps_host,
        "vps_name": vps_name,
    }
    name = build_link_name(name_template, context)

    args = [
        "link",
        f"--{transport}",
        "-s",
        f"{domain}:{external_port}",
        "--ws-path",
        ws_path,
        "--push-path",
        push_path,
        "--name",
        name,
        "--goos",
        goos,
        "--goarch",
        goarch,
    ]
    if options.get("garble", True):
        args.append("--garble")
    if options.get("auto_proxy", True):
        args.append("--auto-proxy")
    if options.get("use_kerberos", True):
        args.append("--use-kerberos")

    extra_args = options.get("extra_args", [])
    if not isinstance(extra_args, list) or not all(isinstance(item, str) for item in extra_args):
        fail("options.extra_args must be a list of strings")
    args.extend(extra_args)

    return name, shell_quote_args(args)


def ssh_base_command(console: dict[str, Any]) -> list[str]:
    host = require_string(console.get("host"), "console.host")
    port = int(console.get("port", 22))
    user = optional_string(console.get("user"), "console.user")
    key = optional_string(console.get("key"), "console.key")
    target = f"{user}@{host}" if user else host

    command = [
        "ssh",
        "-tt",
        "-o",
        "BatchMode=yes",
        "-o",
        "StrictHostKeyChecking=accept-new",
        "-p",
        str(port),
    ]
    if key:
        command.extend(["-i", key])
    command.append(target)
    return command


def run_console_commands(
    console: dict[str, Any],
    commands: list[str],
    timeout: int,
    command_delay: float,
) -> str:
    try:
        import pty
        import select
    except ImportError as exc:
        fail(f"interactive SSH execution requires a Unix-like system with pty/select: {exc}")

    if not commands:
        return ""

    master_fd, slave_fd = pty.openpty()
    proc = subprocess.Popen(
        ssh_base_command(console),
        stdin=slave_fd,
        stdout=slave_fd,
        stderr=slave_fd,
        close_fds=True,
    )
    os.close(slave_fd)
    output = bytearray()
    deadline = time.time() + timeout

    def read_available(duration: float) -> None:
        end = min(time.time() + duration, deadline)
        while time.time() < end:
            ready, _, _ = select.select([master_fd], [], [], 0.2)
            if master_fd not in ready:
                if proc.poll() is not None:
                    break
                continue
            try:
                chunk = os.read(master_fd, 8192)
            except OSError:
                break
            if not chunk:
                break
            output.extend(chunk)

    try:
        read_available(1.0)
        for command in commands:
            os.write(master_fd, command.encode("utf-8") + b"\n")
            read_available(command_delay)
        os.write(master_fd, b"exit\n")
        while time.time() < deadline:
            read_available(0.5)
            if proc.poll() is not None:
                break
        if proc.poll() is None:
            proc.kill()
            fail(f"reverse_ssh console did not exit within {timeout}s")
    finally:
        try:
            os.close(master_fd)
        except OSError:
            pass

    return output.decode("utf-8", errors="replace")


def existing_names_from_listing(listing: str, planned_names: set[str]) -> set[str]:
    existing: set[str] = set()
    lines = listing.splitlines()
    for name in planned_names:
        if any(name in line for line in lines):
            existing.add(name)
    return existing


def write_artifacts(output_dir: Path, commands: list[str], result: dict[str, Any]) -> None:
    output_dir.mkdir(parents=True, exist_ok=True)
    commands_path = output_dir / "commands.sh"
    result_path = output_dir / "result.json"
    commands_path.write_text(
        "# Generated reverse_ssh link commands.\n"
        "# Review before reusing manually.\n"
        + "\n".join(commands)
        + ("\n" if commands else ""),
        encoding="utf-8",
    )
    result_path.write_text(json.dumps(result, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--spec", required=True, type=Path)
    parser.add_argument("--execute", action="store_true")
    args = parser.parse_args()

    spec = read_spec(args.spec)
    console = spec.get("console", {})
    options = spec.get("options", {})
    edges = spec.get("edges", [])
    transports = options.get("transports", ["wss"])
    platforms = options.get("platforms", [{"goos": "windows", "goarch": "amd64"}])
    output_dir = Path(require_string(spec.get("output_dir"), "output_dir"))

    if not isinstance(edges, list):
        fail("edges must be a list")
    if not isinstance(transports, list) or not all(isinstance(item, str) for item in transports):
        fail("options.transports must be a list of strings")
    if not isinstance(platforms, list) or not all(isinstance(item, dict) for item in platforms):
        fail("options.platforms must be a list of objects")

    planned: list[dict[str, str]] = []
    for edge in edges:
        if not isinstance(edge, dict):
            fail("each edge must be an object")
        for transport in transports:
            for platform in platforms:
                name, command = build_create_command(edge, transport, platform, options)
                planned.append({"name": name, "create": command})

    force_rotate = bool(options.get("force_rotate", False))
    timeout = int(console.get("timeout", 60))
    command_delay = float(console.get("command_delay", 1.0))
    planned_names = {item["name"] for item in planned}

    if args.execute:
        listing = run_console_commands(console, ["link -l"], timeout=timeout, command_delay=command_delay)
        existing = existing_names_from_listing(listing, planned_names)
    else:
        listing = ""
        existing = set()

    commands_to_run: list[str] = []
    skipped_existing: list[str] = []
    rotated: list[str] = []
    created: list[str] = []

    for item in planned:
        name = item["name"]
        if name in existing and not force_rotate:
            skipped_existing.append(name)
            continue
        if name in existing and force_rotate:
            commands_to_run.append(f"link -r {console_quote(name)}")
            rotated.append(name)
        commands_to_run.append(item["create"])
        created.append(name)

    transcript = ""
    if args.execute and commands_to_run:
        transcript = run_console_commands(console, commands_to_run, timeout=timeout, command_delay=command_delay)

    result = {
        "execute": args.execute,
        "planned": len(planned),
        "created": len(created) if args.execute else 0,
        "would_create": 0 if args.execute else len(created),
        "rotated": len(rotated) if args.execute else 0,
        "would_rotate": 0 if args.execute else len(rotated),
        "skipped_existing": len(skipped_existing),
        "commands_file": str(output_dir / "commands.sh"),
        "result_file": str(output_dir / "result.json"),
        "skipped_existing_names": skipped_existing,
        "rotated_names": rotated if args.execute else [],
        "would_rotate_names": [] if args.execute else rotated,
        "created_names": created if args.execute else [],
        "would_create_names": [] if args.execute else created,
    }
    if listing:
        result["listing_excerpt"] = listing[-4000:]
    if transcript:
        result["transcript_excerpt"] = transcript[-4000:]

    write_artifacts(output_dir, commands_to_run, result)
    print(json.dumps(result, indent=2, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
