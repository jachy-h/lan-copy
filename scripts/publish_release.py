#!/usr/bin/env python3
"""Build a release, commit VERSION, and push its branch and tag atomically."""

import argparse
from pathlib import Path
import shlex
import subprocess
import sys

from build_release import build_lock, make_preview, next_version, release_version


def git(root, *args, check=True):
    result = subprocess.run(["git", *args], cwd=root, capture_output=True, text=True)
    if check and result.returncode:
        raise ValueError(result.stderr.strip() or result.stdout.strip() or f"git {args[0]} failed")
    return result


def require_clean(root):
    status = git(root, "status", "--porcelain", "--untracked-files=all").stdout
    if status:
        raise ValueError("存在未提交的代码或文件，请先 git commit，再执行 make publish。\n" + status.rstrip())


def confirm_publish(version):
    while True:
        try:
            answer = input(f"请确认是否发布 {version} 版本 [y/n]: ").strip().lower()
        except (EOFError, KeyboardInterrupt):
            print()
            return False
        if answer in ("y", "n"):
            return answer == "y"
        print("请输入 y 或 n。")


def publish(root, version_file, override, remote, branch, command):
    root = root.resolve()
    version_file = version_file.resolve()
    if version_file != root / "VERSION":
        raise ValueError("make publish 必须使用项目根目录的 VERSION，以匹配 GitHub Actions。")
    if make_preview():
        version = next_version(version_file, override)
        print(f"Would build {version}, commit VERSION, create v{version}, and atomically push {branch} and its tag to {remote}.")
        return 0

    # Separate from build.lock: the nested make build acquires that lock itself.
    with build_lock(root / ".config" / "publish.lock"):
        require_clean(root)
        current_branch = git(root, "symbolic-ref", "--quiet", "--short", "HEAD").stdout.strip()
        if current_branch != branch:
            raise ValueError(f"请先切换到 {branch} 分支再发布，当前分支是 {current_branch}。")
        git(root, "check-ref-format", f"refs/heads/{branch}")
        git(root, "remote", "get-url", remote)
        git(root, "ls-files", "--error-unmatch", "VERSION")
        before = git(root, "rev-parse", "HEAD").stdout.strip()
        current = release_version(version_file.read_text().strip())
        version = next_version(version_file, override)
        if tuple(map(int, version.split("."))) <= tuple(map(int, current.split("."))):
            raise ValueError(f"发布版本 {version} 必须大于当前版本 {current}。")
        tag = f"v{version}"
        if git(root, "show-ref", "--verify", "--quiet", f"refs/tags/{tag}", check=False).returncode == 0:
            raise ValueError(f"本地标签 {tag} 已存在，请检查版本或已有发布。")
        remote_tags = git(root, "ls-remote", "--tags", remote, f"refs/tags/{tag}").stdout
        if remote_tags.strip():
            raise ValueError(f"远端标签 {tag} 已存在，请检查版本或已有发布。")
        git(root, "fetch", "--no-tags", remote, f"refs/heads/{branch}")
        remote_head = git(root, "rev-parse", "FETCH_HEAD").stdout.strip()
        if git(root, "merge-base", "--is-ancestor", remote_head, before, check=False).returncode:
            raise ValueError(f"本地 {branch} 落后于远端或已分叉，请先同步并提交，再执行 make publish。")
        require_clean(root)
        if git(root, "rev-parse", "HEAD").stdout.strip() != before:
            raise ValueError("预检查期间 HEAD 发生变化，请重新执行 make publish。")

        if not confirm_publish(version):
            print("已取消发布。", flush=True)
            return 0
        require_clean(root)
        if git(root, "rev-parse", "HEAD").stdout.strip() != before:
            raise ValueError("确认期间 HEAD 发生变化，请重新执行 make publish。")

        print(f"Publishing Lan Copy {version}: building signed packages…", flush=True)
        result = subprocess.run([*command, f"VERSION={version}"], cwd=root, close_fds=False)
        if result.returncode:
            print("构建失败，未创建版本提交、标签或推送。", flush=True)
            return result.returncode
        if version_file.read_text().strip() != version:
            raise ValueError("构建后的 VERSION 与目标版本不一致，停止发布。")
        if git(root, "rev-parse", "HEAD").stdout.strip() != before:
            raise ValueError("构建期间 HEAD 发生变化，停止发布；请检查本地 VERSION。")
        # Commit only VERSION; never sweep unrelated edits made during the build.
        status = git(root, "status", "--porcelain", "--untracked-files=all").stdout.splitlines()
        if status != [" M VERSION"]:
            raise ValueError("构建期间出现其他未提交改动，停止发布，请先检查并 commit。\n" + "\n".join(status))
        git(root, "commit", "--only", "-m", f"chore: release {tag}", "--", "VERSION")
        commit = git(root, "rev-parse", "HEAD").stdout.strip()
        if (git(root, "rev-parse", f"{commit}^").stdout.strip() != before
                or git(root, "show", f"{commit}:VERSION").stdout.strip() != version
                or git(root, "diff-tree", "--no-commit-id", "--name-only", "-r", commit).stdout.strip() != "VERSION"):
            raise ValueError("版本提交内容与预期不一致，停止创建标签和推送，请检查 Git hooks 或并发提交。")
        require_clean(root)
        git(root, "tag", "-a", tag, commit, "-m", f"Lan Copy {version}")
        # Explicit refs prevent another branch or tag being sent accidentally.
        push = ["git", "push", "--atomic", "--no-follow-tags", remote,
                f"{commit}:refs/heads/{branch}", f"refs/tags/{tag}:refs/tags/{tag}"]
        result = subprocess.run(push, cwd=root)
        if result.returncode:
            print(f"推送失败，本地版本提交 {commit[:7]} 和标签 {tag} 已保留。", flush=True)
            print("修复网络、权限或远端冲突后，重试以下命令（不必再次递增版本）：", flush=True)
            print(shlex.join(push), flush=True)
            return result.returncode
        print(f"\nPublished tag: {tag}\nCommit: {commit}\nRemote: {remote}\nBranch: {branch}")
        print("GitHub Actions 将执行测试、构建并发布 GitHub Release；最终结果请查看仓库 Actions 页面。")
        return 0


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version-file", type=Path, default=Path("VERSION"))
    parser.add_argument("--version", default="")
    parser.add_argument("--remote", default="origin")
    parser.add_argument("--branch", default="main")
    parser.add_argument("command", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    command = args.command[1:] if args.command[:1] == ["--"] else args.command
    if not command:
        parser.error("a build command is required after --")
    try:
        # The release workflow is configured for main; don't silently publish a
        # branch the workflow never tests as a branch push.
        if args.branch != "main":
            raise ValueError("当前发布工作流仅支持 main 分支。")
        return publish(Path.cwd(), args.version_file, args.version, args.remote, args.branch, command)
    except (OSError, ValueError) as error:
        parser.exit(1, f"Publish error: {error}\n")


if __name__ == "__main__":
    raise SystemExit(main())
