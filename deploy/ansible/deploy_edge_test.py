from __future__ import annotations

import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest import mock

import deploy_edge


class DeployEdgeTest(unittest.TestCase):
    def parse(self, *arguments: str):
        args = deploy_edge.build_parser().parse_args(arguments)
        if args.ansible_args and args.ansible_args[0] == "--":
            args.ansible_args = args.ansible_args[1:]
        args.lock_file = str(Path(tempfile.gettempdir()) / "reverse-logger-test.lock")
        return args

    def test_rollout_failure_blocks_link_publication(self) -> None:
        args = self.parse("-i", "inventory.yml")
        runner = mock.Mock(
            return_value=subprocess.CompletedProcess([], returncode=7)
        )

        self.assertEqual(deploy_edge.run_pipeline(args, runner=runner), 7)
        self.assertEqual(runner.call_count, 1)
        self.assertIn("edge-rollout.yml", str(runner.call_args.args[0]))

    def test_success_runs_rollout_then_links(self) -> None:
        args = self.parse("-i", "inventory.yml", "-e", "release=abc")
        runner = mock.Mock(
            side_effect=[
                subprocess.CompletedProcess([], returncode=0),
                subprocess.CompletedProcess([], returncode=0),
            ]
        )

        self.assertEqual(deploy_edge.run_pipeline(args, runner=runner), 0)
        self.assertEqual(runner.call_count, 2)
        self.assertIn("edge-rollout.yml", str(runner.call_args_list[0].args[0]))
        self.assertIn("links-publish.yml", str(runner.call_args_list[1].args[0]))

    def test_skip_links_runs_only_rollout(self) -> None:
        args = self.parse("--skip-links")
        runner = mock.Mock(
            return_value=subprocess.CompletedProcess([], returncode=0)
        )

        self.assertEqual(deploy_edge.run_pipeline(args, runner=runner), 0)
        self.assertEqual(runner.call_count, 1)

    def test_check_mode_starts_no_playbook(self) -> None:
        args = self.parse("--check")
        runner = mock.Mock()

        self.assertEqual(deploy_edge.run_pipeline(args, runner=runner), 2)
        runner.assert_not_called()

    def test_forwarded_check_mode_starts_no_playbook(self) -> None:
        args = self.parse("--", "--check")
        runner = mock.Mock()

        self.assertEqual(deploy_edge.run_pipeline(args, runner=runner), 2)
        runner.assert_not_called()

    def test_common_arguments_are_forwarded_to_both_phases(self) -> None:
        args = self.parse(
            "-i",
            "inventory.yml",
            "--limit",
            "edge1,main1",
            "--diff",
            "--",
            "--vault-id",
            "prod@prompt",
        )

        rollout, links = deploy_edge.build_commands(args)
        self.assertIsNotNone(links)
        self.assertIn("edge1,main1,localhost", rollout)
        self.assertIn("edge1,main1,main", links)
        for command in (rollout, links):
            self.assertIn("inventory.yml", command)
            self.assertIn("--vault-id", command)
            self.assertIn("prod@prompt", command)

    def test_deployment_lock_blocks_a_second_owner(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            lock_path = Path(directory) / "deploy.lock"
            with deploy_edge.DeploymentLock(lock_path):
                with self.assertRaises(deploy_edge.DeploymentLockError):
                    with deploy_edge.DeploymentLock(lock_path):
                        self.fail("the second deployment unexpectedly acquired the lock")


if __name__ == "__main__":
    unittest.main()
