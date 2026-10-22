import contextlib
import io
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

from publish_release import publish


class PublishReleaseTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.base = Path(self.directory.name)
        self.repo = self.base / "repo"
        self.remote = self.base / "origin.git"
        self.repo.mkdir()
        self.command("git", "init", "--initial-branch=main", str(self.repo))
        self.command("git", "init", "--bare", "--initial-branch=main", str(self.remote))
        self.git("config", "user.name", "Release Test")
        self.git("config", "user.email", "release@example.invalid")
        self.git("config", "commit.gpgsign", "false")
        self.git("config", "tag.gpgsign", "false")
        (self.repo / ".gitignore").write_text("/.config/\n/release/\n")
        (self.repo / "VERSION").write_text("1.2.3\n")
        (self.repo / "code.txt").write_text("committed source\n")
        self.git("add", ".")
        self.git("commit", "-m", "initial")
        self.initial = self.git("rev-parse", "HEAD")
        self.git("remote", "add", "origin", str(self.remote))
        self.git("push", "origin", "main")
        self.builder = self.base / "builder.py"
        self.builder.write_text(
            "from pathlib import Path\nimport sys\n"
            "version=sys.argv[-1].split('=',1)[1]\n"
            "Path('VERSION').write_text(version+'\\n')\n"
            "Path('.config/built').write_text(version)\n"
        )

    def command(self, *args, cwd=None):
        result = subprocess.run(args, cwd=cwd, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        return result.stdout.strip()

    def git(self, *args):
        return self.command("git", *args, cwd=self.repo)

    def run_publish(self, override="", env=None, answers=None):
        with patch.dict(os.environ, {"MAKEFLAGS": "", **(env or {})}), contextlib.redirect_stdout(io.StringIO()) as output, patch(
                "builtins.input", side_effect=answers, return_value="y") as prompt:
            self.prompt = prompt
            status = publish(self.repo, self.repo / "VERSION", override, "origin", "main",
                             [sys.executable, str(self.builder)])
        return status, output.getvalue()

    def assert_not_built(self):
        self.assertFalse((self.repo / ".config/built").exists())
        self.assertEqual((self.repo / "VERSION").read_text(), "1.2.3\n")
        self.assertEqual(self.git("rev-parse", "HEAD"), self.initial)

    def test_success_commits_only_version_and_pushes_annotated_tag(self):
        status, output = self.run_publish()
        self.assertEqual(status, 0)
        self.assertEqual((self.repo / "VERSION").read_text(), "1.2.4\n")
        self.assertEqual(self.git("status", "--porcelain"), "")
        self.assertEqual(self.git("diff-tree", "--no-commit-id", "--name-only", "-r", "HEAD"), "VERSION")
        self.assertEqual(self.git("cat-file", "-t", "v1.2.4"), "tag")
        head = self.git("rev-parse", "HEAD")
        self.assertEqual(self.git("rev-parse", "v1.2.4^{}"), head)
        self.assertEqual(self.command("git", "--git-dir", str(self.remote), "rev-parse", "main"), head)
        self.assertEqual(self.command("git", "--git-dir", str(self.remote), "rev-parse", "v1.2.4^{}"), head)
        self.assertIn("GitHub Actions", output)
        self.assertIn("v1.2.4", output)

    def test_does_not_push_other_tags_from_global_follow_tags_setting(self):
        self.git("config", "push.followTags", "true")
        self.git("tag", "-a", "old-local-tag", "-m", "Unrelated local tag")
        self.assertEqual(self.run_publish()[0], 0)
        self.assertEqual(self.command("git", "--git-dir", str(self.remote), "tag", "--list"), "v1.2.4")

    def test_unstaged_changes_require_commit(self):
        (self.repo / "code.txt").write_text("modified")
        with self.assertRaisesRegex(ValueError, "先 git commit"):
            self.run_publish()
        self.assert_not_built()

    def test_staged_changes_require_commit(self):
        (self.repo / "code.txt").write_text("modified")
        self.git("add", "code.txt")
        with self.assertRaisesRegex(ValueError, "先 git commit"):
            self.run_publish()
        self.assert_not_built()

    def test_untracked_changes_require_commit(self):
        (self.repo / "new.txt").touch()
        with self.assertRaisesRegex(ValueError, "先 git commit"):
            self.run_publish()
        self.assert_not_built()

    def test_wrong_branch_stops_before_build(self):
        self.git("checkout", "-b", "feature")
        with self.assertRaisesRegex(ValueError, "切换到 main"):
            self.run_publish()
        self.assert_not_built()

    def test_existing_local_tag_stops_before_build(self):
        self.git("tag", "v1.2.4")
        with self.assertRaisesRegex(ValueError, "本地标签"):
            self.run_publish()
        self.assert_not_built()

    def test_existing_remote_tag_stops_before_build(self):
        self.git("tag", "v1.2.4")
        self.git("push", "origin", "v1.2.4")
        self.git("tag", "-d", "v1.2.4")
        with self.assertRaisesRegex(ValueError, "远端标签"):
            self.run_publish()
        self.assert_not_built()

    def test_build_failure_creates_no_commit_or_tag(self):
        self.builder.write_text("raise SystemExit(2)\n")
        status, _ = self.run_publish()
        self.assertEqual(status, 2)
        self.assert_not_built()
        self.assertEqual(self.git("tag", "--list"), "")

    def test_edits_during_build_are_not_committed(self):
        with self.builder.open("a") as file:
            file.write("Path('code.txt').write_text('concurrent edit')\n")
        with self.assertRaisesRegex(ValueError, "其他未提交改动"):
            self.run_publish()
        self.assertEqual(self.git("rev-parse", "HEAD"), self.initial)
        self.assertEqual(self.git("tag", "--list"), "")
        self.assertEqual((self.repo / "code.txt").read_text(), "concurrent edit")

    def test_failed_atomic_push_preserves_local_release(self):
        hook = self.remote / "hooks/update"
        hook.write_text('#!/bin/sh\ncase "$1" in refs/tags/*) exit 1;; esac\nexit 0\n')
        hook.chmod(0o755)
        status, output = self.run_publish()
        self.assertNotEqual(status, 0)
        self.assertEqual((self.repo / "VERSION").read_text(), "1.2.4\n")
        self.assertEqual(self.git("cat-file", "-t", "v1.2.4"), "tag")
        self.assertIn("git push --atomic", output)
        self.assertEqual(self.command("git", "--git-dir", str(self.remote), "rev-parse", "main"), self.initial)
        self.assertEqual(self.command("git", "--git-dir", str(self.remote), "tag", "--list"), "")

    def test_dry_run_does_not_publish(self):
        status, output = self.run_publish(env={"MAKEFLAGS": "n"})
        self.assertEqual(status, 0)
        self.assertIn("Would build 1.2.4", output)
        self.assert_not_built()
        self.assertFalse((self.repo / ".config").exists())
        self.prompt.assert_not_called()

    def test_cancel_does_not_build_commit_tag_or_push(self):
        for answer in ("n", EOFError(), KeyboardInterrupt()):
            with self.subTest(answer=answer):
                status, output = self.run_publish(answers=[answer])
                self.assertEqual(status, 0)
                self.assertIn("已取消发布", output)
                self.prompt.assert_called_once_with("请确认是否发布 1.2.4 版本 [y/n]: ")
                self.assert_not_built()
                self.assertEqual(self.git("tag", "--list"), "")
                self.assertEqual(self.command("git", "--git-dir", str(self.remote), "rev-parse", "main"), self.initial)
                self.assertEqual(self.command("git", "--git-dir", str(self.remote), "tag", "--list"), "")

    def test_invalid_answer_prompts_again(self):
        status, output = self.run_publish(answers=["", "yes", " Y "])
        self.assertEqual(status, 0)
        self.assertEqual(self.prompt.call_count, 3)
        self.assertEqual(output.count("请输入 y 或 n"), 2)
        self.assertEqual(self.git("rev-parse", "v1.2.4^{}"), self.git("rev-parse", "HEAD"))

    def test_edits_during_confirmation_stop_before_build(self):
        def edit_before_confirming(_prompt):
            (self.repo / "code.txt").write_text("edit during confirmation")
            return "y"

        with self.assertRaisesRegex(ValueError, "先 git commit"):
            self.run_publish(answers=edit_before_confirming)
        self.assert_not_built()

    def test_explicit_version_and_reject_non_increasing_version(self):
        with self.assertRaisesRegex(ValueError, "必须大于"):
            self.run_publish("1.2.3")
        self.assert_not_built()
        self.assertEqual(self.run_publish("2.0.0")[0], 0)
        self.prompt.assert_called_once_with("请确认是否发布 2.0.0 版本 [y/n]: ")
        self.assertEqual(self.git("rev-parse", "v2.0.0^{}"), self.git("rev-parse", "HEAD"))

    def test_remote_ahead_stops_before_build(self):
        other = self.base / "other"
        self.command("git", "clone", str(self.remote), str(other))
        self.command("git", "config", "user.name", "Other Test", cwd=other)
        self.command("git", "config", "user.email", "other@example.invalid", cwd=other)
        self.command("git", "config", "commit.gpgsign", "false", cwd=other)
        (other / "new.txt").write_text("remote change")
        self.command("git", "add", ".", cwd=other)
        self.command("git", "commit", "-m", "remote change", cwd=other)
        self.command("git", "push", "origin", "main", cwd=other)
        with self.assertRaisesRegex(ValueError, "落后于远端或已分叉"):
            self.run_publish()
        self.assert_not_built()


if __name__ == "__main__":
    unittest.main()
