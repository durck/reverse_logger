#!/usr/bin/env python3
"""One-command, health-gated Ansible rollout for the VPS edge fleet."""

from __future__ import annotations

import argparse
import os
from pathlib import Path
import subprocess
import sys
from typing import Callable, Sequence


ANSIBLE_DIR = Path(__file__).resolve().parent
ROLLOUT_PLAYBOOK = Path("playbooks/edge-rollout.yml")
LINKS_PLAYBOOK = Path("playbooks/links-publish.yml")


class DeploymentLockError(RuntimeError):
    """Raised when another deployment owns the controller lock."""


class DeploymentLock:
    """Advisory cross-platform lock held for the complete deployment pipeline."""

    def __init__(self, path: Path) -> None:
        self.path = path
        self._file = None

    def __enter__(self) -> "DeploymentLock":
        self.path.parent.mkdir(parents=True, exist_ok=True)
        if not self.path.exists() or self.path.stat().st_size == 0:
            with self.path.open("ab") as initializer:
                initializer.write(b"\0")
        self._file = self.path.open("r+b")

        try:
            if os.name == "nt":
                import msvcrt

                msvcrt.locking(self._file.fileno(), msvcrt.LK_NBLCK, 1)
            else:
                import fcntl

                fcntl.flock(self._file.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
        except (OSError, BlockingIOError) as exc:
            self._file.close()
            self._file = None
            raise DeploymentLockError(
                f"another edge deployment holds {self.path}"
            ) from exc

        self._file.seek(0)
        self._file.truncate()
        self._file.write(f"pid={os.getpid()}\n".encode("ascii"))
        self._file.flush()
        return self

    def __exit__(self, exc_type, exc, traceback) -> None:  # type: ignore[no-untyped-def]
        if self._file is None:
            return
        self._file.seek(0)
        if os.name == "nt":
            import msvcrt

            msvcrt.locking(self._file.fileno(), msvcrt.LK_UNLCK, 1)
        else:
            import fcntl

            fcntl.flock(self._file.fileno(), fcntl.LOCK_UN)
        self._file.close()
        self._file = None


def build_commands(args: argparse.Namespace) -> tuple[list[str], list[str] | None]:
    """Build rollout and publication commands from validated CLI arguments."""
    common = [args.ansible_playbook]
    if args.inventory:
        common.extend(["--inventory", args.inventory])
    if args.diff:
        common.append("--diff")
    for extra_var in args.extra_vars:
        common.extend(["--extra-vars", extra_var])
    common.extend(args.ansible_args)

    rollout_limit = ["--limit", f"{args.limit},localhost"] if args.limit else []
    links_limit = ["--limit", f"{args.limit},main"] if args.limit else []
    rollout = [*common, *rollout_limit, str(ROLLOUT_PLAYBOOK)]
    links = (
        None
        if args.skip_links
        else [*common, *links_limit, str(LINKS_PLAYBOOK)]
    )
    return rollout, links


def run_pipeline(
    args: argparse.Namespace,
    runner: Callable[..., subprocess.CompletedProcess] = subprocess.run,
) -> int:
    """Run rollout first and publish links only after a zero exit status."""
    if args.check or "--check" in args.ansible_args:
        print(
            "Full --check is intentionally unsupported; use --syntax-check and "
            "a disposable inventory. No playbook was started.",
            file=sys.stderr,
        )
        return 2

    rollout, links = build_commands(args)
    lock_path = Path(args.lock_file).resolve()
    try:
        with DeploymentLock(lock_path):
            rollout_result = runner(rollout, cwd=ANSIBLE_DIR, check=False)
            if rollout_result.returncode != 0:
                print(
                    "Edge rollout failed; link publication was not started.",
                    file=sys.stderr,
                )
                return int(rollout_result.returncode)

            if links is None:
                return 0

            links_result = runner(links, cwd=ANSIBLE_DIR, check=False)
            return int(links_result.returncode)
    except DeploymentLockError as exc:
        print(f"Deployment blocked: {exc}", file=sys.stderr)
        return 3


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description=(
            "Build edge artifacts once, roll them out in health-gated batches, "
            "and publish links only after fleet success."
        )
    )
    parser.add_argument("-i", "--inventory", help="Ansible inventory path")
    parser.add_argument("-l", "--limit", help="Ansible host limit expression")
    parser.add_argument(
        "-e",
        "--extra-vars",
        action="append",
        default=[],
        help="Ansible extra vars; repeat for multiple values",
    )
    parser.add_argument("--diff", action="store_true", help="Show Ansible diffs")
    parser.add_argument(
        "--skip-links",
        action="store_true",
        help="Roll out and verify edges without publishing main-side links",
    )
    parser.add_argument(
        "--check",
        action="store_true",
        help="Rejected explicitly because the full deployment is not check-safe",
    )
    parser.add_argument(
        "--ansible-playbook",
        default="ansible-playbook",
        help="ansible-playbook executable or wrapper",
    )
    parser.add_argument(
        "--lock-file",
        default=str(ANSIBLE_DIR / ".deploy.lock"),
        help="Controller deployment lock path",
    )
    parser.add_argument(
        "ansible_args",
        nargs=argparse.REMAINDER,
        help="Arguments after -- are forwarded to both playbooks",
    )
    return parser


def main(argv: Sequence[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    if args.ansible_args and args.ansible_args[0] == "--":
        args.ansible_args = args.ansible_args[1:]
    return run_pipeline(args)


if __name__ == "__main__":
    raise SystemExit(main())
