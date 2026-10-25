import contextlib
import io
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

from build_release import build, next_version


class ReleaseBuildTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.version = self.root / "VERSION"
        self.lock = self.root / "build.lock"
        self.artifact = self.root / "release.zip"

    def run_build(self, code, override=""):
        with patch.dict(os.environ, {"MAKEFLAGS": ""}), contextlib.redirect_stdout(io.StringIO()) as output:
            status = build(self.version, override, self.lock,
                           [sys.executable, "-c", code, str(self.artifact)], [self.artifact])
        return status, output.getvalue()

    def test_successive_builds_keep_saved_version_and_summary(self):
        code = "from pathlib import Path; import sys; Path(sys.argv[1]).write_text(sys.argv[2])"
        status, output = self.run_build(code)
        self.assertEqual(status, 0)
        self.assertEqual(self.version.read_text(), "0.1.0\n")
        self.assertEqual(self.artifact.read_text(), "VERSION=0.1.0")
        self.assertIn("Version:   0.1.0", output)
        self.assertIn(str(self.artifact.resolve()), output)
        self.run_build(code)
        self.assertEqual(self.version.read_text(), "0.1.0\n")
        self.assertEqual(self.artifact.read_text(), "VERSION=0.1.0")

    def test_failed_build_does_not_advance(self):
        self.version.write_text("1.2.9\n")
        status, _ = self.run_build("raise SystemExit(2)")
        self.assertEqual(status, 2)
        self.assertEqual(self.version.read_text(), "1.2.9\n")

    def test_missing_artifact_does_not_advance(self):
        self.version.write_text("1.2.9\n")
        with self.assertRaisesRegex(ValueError, "no artifact"):
            self.run_build("pass")
        self.assertEqual(self.version.read_text(), "1.2.9\n")

    def test_explicit_version_becomes_baseline(self):
        code = "from pathlib import Path; import sys; Path(sys.argv[1]).touch()"
        self.run_build(code, "2.3.9")
        self.assertEqual(self.version.read_text(), "2.3.9\n")
        self.assertEqual(next_version(self.version), "2.3.10")
        with self.assertRaises(ValueError):
            self.run_build(code, "dev")

    def test_make_dry_run_does_not_advance(self):
        self.version.write_text("1.2.3\n")
        self.artifact.touch()  # Existing artifacts must not make a preview succeed.
        makefile = self.root / "Makefile"
        marker = self.root / "executed"
        makefile.write_text(f".PHONY: all\nall:\n\ttouch {marker}\n")
        env = os.environ.copy()
        env["MAKEFLAGS"] = "n"
        script = Path(__file__).with_name("build_release.py")
        result = subprocess.run([sys.executable, str(script), "--version-file", str(self.version),
                                 "--artifact", str(self.artifact), "--", "make", "-f", str(makefile)],
                                env=env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.version.read_text(), "1.2.3\n")
        self.assertFalse(marker.exists())
        self.assertNotIn("build complete", result.stdout)


if __name__ == "__main__":
    unittest.main()
