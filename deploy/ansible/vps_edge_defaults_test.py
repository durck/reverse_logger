#!/usr/bin/env python3
"""Regression tests for VPS edge playbook defaults."""

from __future__ import annotations

import re
import unittest
from pathlib import Path


PLAYBOOK_PATH = Path(__file__).with_name("vps-edge.yml")


class EdgeHealthDefaultsTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.playbook = PLAYBOOK_PATH.read_text(encoding="utf-8")

    def test_empty_vpn_iface_disables_vpn_check(self) -> None:
        self.assert_default_preserves_falsey("edge_health_vpn_iface")

    def test_empty_local_services_disables_systemd_checks(self) -> None:
        self.assert_default_preserves_falsey("edge_health_local_services")

    def assert_default_preserves_falsey(self, variable: str) -> None:
        match = re.search(rf"^\s*{variable}:\s*\"(?P<expr>.+)\"", self.playbook, re.MULTILINE)
        self.assertIsNotNone(match, f"{variable} default is missing")
        expr = match.group("expr")
        self.assertIn(" | default(", expr)
        self.assertNotRegex(
            expr,
            r"default\([^)]*,\s*true\s*\)",
            f"{variable} must preserve explicit empty values from group_vars",
        )


if __name__ == "__main__":
    unittest.main()
