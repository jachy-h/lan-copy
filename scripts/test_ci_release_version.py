from pathlib import Path
import tempfile
import unittest

from ci_release_version import ci_version


class CIVersionTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.version = Path(self.directory.name) / "VERSION"
        self.version.write_text("1.2.3\n")

    def test_branch_keeps_committed_version(self):
        self.assertEqual(ci_version(self.version, "branch", "main"), "1.2.3")
        self.assertEqual(self.version.read_text(), "1.2.3\n")

    def test_matching_release_tag(self):
        self.assertEqual(ci_version(self.version, "tag", "v1.2.3"), "1.2.3")

    def test_mismatched_or_malformed_tag(self):
        for tag in ("v1.2.4", "v1.2.3-beta.1", "1.2.3", "vnext"):
            with self.subTest(tag=tag), self.assertRaises(ValueError):
                ci_version(self.version, "tag", tag)

    def test_missing_or_invalid_version(self):
        self.version.write_text("dev\n")
        with self.assertRaises(ValueError):
            ci_version(self.version, "branch", "main")
        self.version.unlink()
        with self.assertRaises(OSError):
            ci_version(self.version, "branch", "main")
