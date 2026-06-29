#!/usr/bin/env python3
"""Regression tests for reverse_ssh link generation helpers."""

from __future__ import annotations

import importlib.util
import unittest
from pathlib import Path


SCRIPT_PATH = Path(__file__).with_name("reverse_ssh_generate_links.py")
SPEC = importlib.util.spec_from_file_location("reverse_ssh_generate_links", SCRIPT_PATH)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError(f"cannot import {SCRIPT_PATH}")
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class ExistingNameParsingTest(unittest.TestCase):
    def test_exact_name_matches_as_token(self) -> None:
        listing = "Name edge1-wss-windows-amd64 Url https://entry/dl/edge1-wss-windows-amd64"
        self.assertEqual(
            MODULE.existing_names_from_listing(listing, {"edge1-wss-windows-amd64"}),
            {"edge1-wss-windows-amd64"},
        )

    def test_substring_name_does_not_match(self) -> None:
        listing = "Name old-edge1-wss-test Url https://entry/dl/old-edge1-wss-test"
        self.assertEqual(MODULE.existing_names_from_listing(listing, {"edge1-wss"}), set())

    def test_download_url_suffix_matches_exact_name(self) -> None:
        listing = "Url https://entry.example.com/dl/edge1-wss"
        self.assertEqual(MODULE.existing_names_from_listing(listing, {"edge1-wss"}), {"edge1-wss"})

    def test_slash_name_matches_full_url_path(self) -> None:
        listing = "Url https://entry.example.com/dl/main-g1"
        self.assertEqual(MODULE.existing_names_from_listing(listing, {"dl/main-g1"}), {"dl/main-g1"})


if __name__ == "__main__":
    unittest.main()
