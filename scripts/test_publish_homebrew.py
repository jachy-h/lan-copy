import argparse
import tempfile
import unittest
from pathlib import Path

from publish_homebrew import commit_and_push, prepare_tap, publish, release_checksums

ASSET = "Lan-Copy-macOS-Apple-Silicon.tar.gz"
OLD_SHA = "fe6cb38a213708405f9051c78f17bbca426053397003862953757eee001c9f37"
NEW_SHA = "1" * 64
VERSION = "0.16.1-build2"
FORMULA = f'''class Lancopy < Formula
  desc "Zero-dependency LAN file and text sharing tool"
  homepage "https://github.com/jachy-h/lan-copy"
  url "https://github.com/jachy-h/lan-copy/releases/download/v0.16.0-build1/{ASSET}"
  sha256 "{OLD_SHA}"
  license "MIT"

  def install
    bin.install "lan-copy" => "lancopy"
  end
end
'''


class Result:
    def __init__(self, returncode=0, stdout="", stderr=""):
        self.returncode = returncode
        self.stdout = stdout
        self.stderr = stderr


class Recorder:
    """Fake subprocess runner that records commands and answers by prefix."""

    def __init__(self, handler=None, responses=None):
        self.commands = []
        self.handler = handler
        self.responses = {"symbolic-ref": Result(0, "main\n")}
        self.responses.update(responses or {})

    def __call__(self, args, cwd=None):
        command = [str(part) for part in args]
        self.commands.append(command)
        if self.handler is not None:
            result = self.handler(command)
            if result is not None:
                return result
        joined = " ".join(command)
        for prefix, result in self.responses.items():
            if prefix in joined:
                return result
        return Result()

    def matching(self, *words):
        return [command for command in self.commands if all(word in command for word in words)]


class TapTestCase(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.tap = self.root / "homebrew-jachy-h"
        (self.tap / ".git").mkdir(parents=True)
        (self.tap / "Formula").mkdir()
        self.formula = self.tap / "Formula" / "lancopy.rb"
        self.formula.write_text(FORMULA, encoding="utf-8")
        self.checksums = self.root / "SHA256SUMS"
        self.checksums.write_text(f"{NEW_SHA}  {ASSET}\n", encoding="utf-8")

    def arguments(self, **overrides):
        defaults = {
            "tap_dir": self.tap,
            "version": VERSION,
            "tap": "jachy-h/jachy-h",
            "formula": "lancopy",
            "asset": ASSET,
            "repo": "jachy-h/lan-copy",
            "branch": "main",
            "checksums": self.checksums,
            "wait": 0,
            "dry_run": False,
            "no_verify": False,
            "no_online_audit": False,
            "no_push": False,
        }
        defaults.update(overrides)
        return argparse.Namespace(**defaults)


class PrepareTapTests(TapTestCase):
    def test_clean_tap_does_not_merge(self):
        run = Recorder()
        prepare_tap(self.tap, "main", run=run, echo=lambda message: None)
        self.assertEqual(run.matching("merge"), [])
        self.assertTrue(run.matching("fetch", "origin"))

    def test_dirty_tap_is_rejected(self):
        run = Recorder(responses={"status --porcelain": Result(0, " M Formula/lancopy.rb\n")})
        with self.assertRaises(ValueError) as caught:
            prepare_tap(self.tap, "main", run=run, echo=lambda message: None)
        self.assertIn("未提交改动", str(caught.exception))

    def test_other_branch_is_rejected(self):
        run = Recorder(responses={"symbolic-ref": Result(0, "master\n")})
        with self.assertRaises(ValueError) as caught:
            prepare_tap(self.tap, "main", run=run, echo=lambda message: None)
        self.assertIn("master", str(caught.exception))

    def test_behind_remote_is_fast_forwarded(self):
        def handler(command):
            if "rev-parse HEAD" in " ".join(command):
                return Result(0, "old\n")
            if command[-1] == "origin/main":
                return Result(0, "new\n")
            return None

        run = Recorder(handler=handler)
        prepare_tap(self.tap, "main", run=run, echo=lambda message: None)
        self.assertEqual(len(run.matching("merge", "--ff-only")), 1)

    def test_missing_git_directory_is_rejected(self):
        with self.assertRaises(ValueError):
            prepare_tap(self.root, "main", run=Recorder(), echo=lambda message: None)


class ReleaseChecksumsTests(unittest.TestCase):
    def test_waits_until_the_asset_is_published(self):
        views = {"count": 0}
        sleeps = []

        def handler(command):
            if command[1:3] == ["release", "view"]:
                views["count"] += 1
                if views["count"] < 3:
                    return Result(1, "", "release not found")
                return Result(0, f"SHA256SUMS\n{ASSET}\n")
            if command[1:3] == ["release", "download"]:
                directory = Path(command[command.index("--dir") + 1])
                (directory / "SHA256SUMS").write_text(f"{NEW_SHA}  {ASSET}\n", encoding="utf-8")
                return Result()
            return None

        text = release_checksums(
            "jachy-h/lan-copy", f"v{VERSION}", ASSET, 90,
            run=Recorder(handler=handler), sleep=sleeps.append, echo=lambda message: None,
        )
        self.assertIn(NEW_SHA, text)
        self.assertEqual(len(sleeps), 2)
        self.assertTrue(all(second <= 15 for second in sleeps))

    def test_times_out_without_waiting(self):
        run = Recorder(responses={"release view": Result(1, "", "release not found")})
        with self.assertRaises(ValueError) as caught:
            release_checksums(
                "jachy-h/lan-copy", f"v{VERSION}", ASSET, 0,
                run=run, sleep=lambda seconds: self.fail("should not sleep"), echo=lambda message: None,
            )
        self.assertIn("还没有", str(caught.exception))

    def test_wait_budget_is_bounded(self):
        sleeps = []
        run = Recorder(responses={"release view": Result(1, "", "release not found")})
        with self.assertRaises(ValueError):
            release_checksums(
                "jachy-h/lan-copy", f"v{VERSION}", ASSET, 20,
                run=run, sleep=sleeps.append, echo=lambda message: None,
            )
        self.assertLessEqual(sum(sleeps), 20)


class PublishTests(TapTestCase):
    def test_updates_verifies_commits_and_pushes(self):
        run = Recorder()
        self.assertEqual(publish(self.arguments(), run=run, echo=lambda message: None), 0)
        self.assertIn(f'url "https://github.com/jachy-h/lan-copy/releases/download/v{VERSION}/{ASSET}"',
                      self.formula.read_text(encoding="utf-8"))
        self.assertEqual(len(run.matching("commit", "-m")), 1)
        self.assertEqual(len(run.matching("push", "HEAD:main")), 1)
        self.assertTrue(run.matching("audit", "--strict", "--online"))

    def test_commit_message_names_the_version(self):
        run = Recorder()
        commit_and_push(self.tap, "main", "lancopy", VERSION, run=run, echo=lambda message: None)
        messages = [command for command in run.commands if "commit" in command]
        self.assertIn(f"chore: update lancopy v{VERSION}", messages[0])

    def test_unchanged_formula_does_not_commit(self):
        self.formula.write_text(
            FORMULA.replace("v0.16.0-build1", f"v{VERSION}").replace(OLD_SHA, NEW_SHA),
            encoding="utf-8",
        )
        run = Recorder()
        self.assertEqual(publish(self.arguments(), run=run, echo=lambda message: None), 0)
        self.assertEqual(run.matching("commit"), [])
        self.assertEqual(run.matching("push"), [])

    def test_failed_verification_restores_the_formula(self):
        run = Recorder(responses={"audit --strict": Result(1, "", "audit failed")})
        with self.assertRaises(ValueError) as caught:
            publish(self.arguments(), run=run, echo=lambda message: None)
        self.assertIn("brew audit", str(caught.exception))
        self.assertEqual(self.formula.read_text(encoding="utf-8"), FORMULA)
        self.assertEqual(run.matching("commit"), [])

    def test_dry_run_writes_nothing(self):
        run = Recorder()
        self.assertEqual(publish(self.arguments(dry_run=True), run=run, echo=lambda message: None), 0)
        self.assertEqual(self.formula.read_text(encoding="utf-8"), FORMULA)
        self.assertEqual(run.matching("commit"), [])
        self.assertEqual(run.matching("fetch"), [])

    def test_no_push_keeps_the_commit_local(self):
        run = Recorder()
        publish(self.arguments(no_push=True), run=run, echo=lambda message: None)
        self.assertEqual(len(run.matching("commit", "-m")), 1)
        self.assertEqual(run.matching("push"), [])

    def test_offline_audit_skips_the_network_check(self):
        run = Recorder()
        publish(self.arguments(no_online_audit=True), run=run, echo=lambda message: None)
        audits = run.matching("audit", "--strict")
        self.assertEqual(len(audits), 1)
        self.assertNotIn("--online", audits[0])

    def test_missing_formula_is_rejected(self):
        self.formula.unlink()
        with self.assertRaises(ValueError) as caught:
            publish(self.arguments(), run=Recorder(), echo=lambda message: None)
        self.assertIn("brew tap jachy-h/jachy-h", str(caught.exception))

    def test_invalid_version_is_rejected(self):
        with self.assertRaises(ValueError):
            publish(self.arguments(version="0.16.1-rc1"), run=Recorder(), echo=lambda message: None)

    def test_checksum_must_cover_the_asset(self):
        self.checksums.write_text(f"{NEW_SHA}  Lan-Copy-Windows-amd64.zip\n", encoding="utf-8")
        with self.assertRaises(ValueError):
            publish(self.arguments(), run=Recorder(), echo=lambda message: None)


if __name__ == "__main__":
    unittest.main()
