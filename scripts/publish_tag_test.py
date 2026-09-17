#!/usr/bin/env python3
"""Tests for the tag-triggered release publisher. No network or uploads."""

import os
import tempfile
import unittest
from pathlib import Path
from unittest import mock

import publish_tag


class PublishTagTest(unittest.TestCase):
    def setUp(self):
        self.sha = publish_tag.release.commit()
        self.env = mock.patch.dict(os.environ, {
            "RELEASE_TAG": "v9.9.9",
            "RELEASE_COMMIT": self.sha,
            "GITHUB_REF": "refs/tags/v9.9.9",
        })
        self.env.start()
        self.addCleanup(self.env.stop)

    def test_context_accepts_matching_tag_and_commit(self):
        self.assertEqual(publish_tag.context(), ("v9.9.9", self.sha))

    def test_context_refuses_wrong_ref(self):
        with mock.patch.dict(os.environ, {"GITHUB_REF": "refs/heads/master"}):
            with self.assertRaises(ValueError):
                publish_tag.context()

    def test_context_refuses_bad_tag(self):
        with mock.patch.dict(os.environ, {"RELEASE_TAG": "9.9.9"}):
            with self.assertRaises(ValueError):
                publish_tag.context()

    def test_context_refuses_moved_checkout(self):
        with mock.patch.dict(os.environ, {"RELEASE_COMMIT": "0" * 40}):
            with self.assertRaises(ValueError):
                publish_tag.context()

    def test_make_latest(self):
        self.assertEqual(publish_tag.make_latest("v9.9.9"), "true")

    def test_missing_assets(self):
        with tempfile.TemporaryDirectory() as temp:
            native = Path(temp)
            (native / "a.tar.gz").write_bytes(b"a")
            (native / "b.tar.gz").write_bytes(b"b")
            self.assertEqual(publish_tag.missing_assets(native, {"a.tar.gz"}), ["b.tar.gz"])
            self.assertEqual(publish_tag.missing_assets(native, set()),
                             ["a.tar.gz", "b.tar.gz"])
            with self.assertRaises(ValueError):
                publish_tag.missing_assets(native, {"a.tar.gz", "surprise.tar.gz"})


if __name__ == "__main__":
    unittest.main()
