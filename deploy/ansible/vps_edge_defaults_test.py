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

    def test_safe_network_and_supply_chain_defaults(self) -> None:
        self.assertIn(
            "reverse_logger_go_sumdb: \"{{ reverse_logger_go_sumdb | default('sum.golang.org', true) }}\"",
            self.playbook,
        )
        self.assertIn(
            "reverse_logger_go_download_trace: \"{{ reverse_logger_go_download_trace | default(false) }}\"",
            self.playbook,
        )
        self.assertIn(
            "nginx_edge_fix_resolv_conf: \"{{ nginx_edge_fix_resolv_conf | default(false) }}\"",
            self.playbook,
        )

    def test_rollout_starts_with_a_canary_and_stops_on_failure(self) -> None:
        self.assertIn(
            "serial: \"{{ edge_rollout_serial | default([1, '25%', '100%']) }}\"",
            self.playbook,
        )
        self.assertIn(
            "max_fail_percentage: \"{{ edge_rollout_max_fail_percentage | default(0) }}\"",
            self.playbook,
        )

    def test_builds_are_guarded_by_per_binary_commit_markers(self) -> None:
        for component in ("nginx-edge-forwarder", "edge-health"):
            self.assertIn(
                f'path: "{{{{ reverse_logger_build_state_dir }}}}/{component}.version"',
                self.playbook,
            )
        self.assertIn("when: reverse_logger_go_build_required | bool", self.playbook)
        self.assertIn("when: reverse_logger_forwarder_build_required | bool", self.playbook)
        self.assertIn("reverse_logger_edge_health_build_required | bool", self.playbook)

    def test_handlers_flush_before_health_registration(self) -> None:
        flush_index = self.playbook.index("ansible.builtin.meta: flush_handlers")
        registration_index = self.playbook.index("name: Register expected VPS health node")
        self.assertLess(flush_index, registration_index)
        self.assertNotIn("name: Reload systemd after unit installation", self.playbook)

    def test_controller_artifact_mode_skips_target_build_dependencies(self) -> None:
        self.assertIn(
            "reverse_logger_install_mode in ['target_source', 'controller_artifact']",
            self.playbook,
        )
        self.assertIn("name: Install controller-built reverse_logger artifacts", self.playbook)
        self.assertIn(
            "(reverse_logger_install_mode == 'target_source')",
            self.playbook,
        )

    def test_artifact_promotes_only_after_health_and_registration(self) -> None:
        verify_index = self.playbook.index("name: Run edge service and public endpoint health gate")
        registration_index = self.playbook.index("name: Register expected VPS health node")
        promotion_index = self.playbook.index("name: Promote verified controller artifact")
        self.assertLess(verify_index, registration_index)
        self.assertLess(registration_index, promotion_index)

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
