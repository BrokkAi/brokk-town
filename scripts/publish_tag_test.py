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
            "RELEASE_TAG": "v9.9.9-town",
            "RELEASE_COMMIT": self.sha,
            "GITHUB_REF": "refs/tags/v9.9.9-town",
        })
        self.env.start()
        self.addCleanup(self.env.stop)

    def test_context_accepts_matching_tag_and_commit(self):
        self.assertEqual(publish_tag.context(), ("v9.9.9-town", self.sha))

    def test_context_refuses_wrong_ref(self):
        with mock.patch.dict(os.environ, {"GITHUB_REF": "refs/heads/master"}):
            with self.assertRaises(ValueError):
                publish_tag.context()

    def test_context_refuses_moved_checkout(self):
        with mock.patch.dict(os.environ, {"RELEASE_COMMIT": "0" * 40}):
            with self.assertRaises(ValueError):
                publish_tag.context()

    def test_make_latest(self):
        self.assertEqual(publish_tag.make_latest("v9.9.9-town"), "true")
        self.assertEqual(publish_tag.make_latest("v9.9.9-rc.1-town"), "false")

    def test_finds_draft_omitted_by_tag_endpoint(self):
        draft = {"id": 7, "tag_name": "v9.9.9-town", "draft": True}
        with mock.patch.object(publish_tag, "api", side_effect=[None, [draft]]) as api:
            self.assertEqual(publish_tag.find_release("v9.9.9-town"), draft)
        self.assertEqual(api.call_args_list, [
            mock.call("releases/tags/v9.9.9-town", missing=True),
            mock.call("releases?per_page=100&page=1"),
        ])

    def test_draft_lookup_paginates_and_rejects_duplicates(self):
        draft = {"id": 7, "tag_name": "v9.9.9-town", "draft": True}
        full_page = [{"tag_name": f"v1.0.{n}-town"} for n in range(100)]
        with mock.patch.object(publish_tag, "api", side_effect=[None, full_page, [draft]]):
            self.assertEqual(publish_tag.find_release("v9.9.9-town"), draft)
        with mock.patch.object(publish_tag, "api", side_effect=[None, [draft, dict(draft, id=8)]]):
            with self.assertRaisesRegex(ValueError, "duplicate drafts"):
                publish_tag.find_release("v9.9.9-town")

    def test_release_lookup_handles_published_and_new_tags(self):
        published = {"tag_name": "v9.9.9-town", "draft": False}
        with mock.patch.object(publish_tag, "api", return_value=published) as api:
            self.assertEqual(publish_tag.find_release("v9.9.9-town"), published)
            api.assert_called_once()
        with mock.patch.object(publish_tag, "api", side_effect=[None, []]):
            self.assertIsNone(publish_tag.find_release("v9.9.9-town"))

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
