import unittest
from unittest import mock
import publish_tag


class DraftReleaseTest(unittest.TestCase):
    def test_finds_draft_omitted_by_tag_endpoint(self):
        draft = {"id": 7, "tag_name": "v9.9.9-bug-bot", "draft": True}
        with mock.patch.object(publish_tag, "api", side_effect=[None, [draft]]) as api:
            self.assertEqual(publish_tag.find_release("v9.9.9-bug-bot"), draft)
        self.assertEqual(api.call_args_list, [
            mock.call("releases/tags/v9.9.9-bug-bot", missing=True),
            mock.call("releases?per_page=100&page=1"),
        ])

    def test_draft_lookup_paginates_and_rejects_duplicates(self):
        draft = {"id": 7, "tag_name": "v9.9.9-bug-bot", "draft": True}
        full_page = [{"tag_name": f"v1.0.{n}-bug-bot"} for n in range(100)]
        with mock.patch.object(publish_tag, "api", side_effect=[None, full_page, [draft]]):
            self.assertEqual(publish_tag.find_release("v9.9.9-bug-bot"), draft)
        with mock.patch.object(publish_tag, "api", side_effect=[None, [draft, dict(draft, id=8)]]):
            with self.assertRaisesRegex(ValueError, "duplicate drafts"):
                publish_tag.find_release("v9.9.9-bug-bot")

    def test_release_lookup_handles_published_and_new_tags(self):
        published = {"tag_name": "v9.9.9-bug-bot", "draft": False}
        with mock.patch.object(publish_tag, "api", return_value=published) as api:
            self.assertEqual(publish_tag.find_release("v9.9.9-bug-bot"), published)
            api.assert_called_once()
        with mock.patch.object(publish_tag, "api", side_effect=[None, []]):
            self.assertIsNone(publish_tag.find_release("v9.9.9-bug-bot"))

