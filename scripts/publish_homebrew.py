#!/usr/bin/env python3
"""Point the local Homebrew tap formula at a published release, verify it, and push."""

import argparse
import subprocess
import tempfile
import time
from pathlib import Path

from build_release import release_version
from update_homebrew_formula import parse_checksums, updated_formula

DEFAULT_TAP = "jachy-h/jachy-h"
DEFAULT_FORMULA = "lancopy"
DEFAULT_ASSET = "Lan-Copy-macOS-Apple-Silicon.tar.gz"
DEFAULT_REPO = "jachy-h/lan-copy"
POLL_SECONDS = 15


def execute(args, cwd=None):
    return subprocess.run(args, cwd=cwd, capture_output=True, text=True)


def emit(message):
    print(message, flush=True)


def failure(result):
    return (result.stderr or result.stdout or "").strip()


def git(tap_dir, *args, run=execute):
    return run(["git", "-C", str(tap_dir), *args])


def require_git(tap_dir, *args, run=execute, message=""):
    """Run a git command in the tap and return stripped stdout."""
    result = git(tap_dir, *args, run=run)
    if result.returncode:
        raise ValueError(message or failure(result) or f"git {args[0]} failed")
    return result.stdout.strip()


def formula_path(tap_dir, formula):
    return Path(tap_dir) / "Formula" / f"{formula}.rb"


def prepare_tap(tap_dir, branch, run, echo):
    """Check the tap checkout is clean and fast-forward it to origin/branch."""
    if not (Path(tap_dir) / ".git").exists():
        raise ValueError(f"{tap_dir} 不是 Homebrew Tap 的 git clone，先执行 brew tap {DEFAULT_TAP}")
    current = require_git(
        tap_dir, "symbolic-ref", "--quiet", "--short", "HEAD", run=run,
        message=f"Tap 仓库处于分离 HEAD 状态，请切换到 {branch} 分支",
    )
    if current != branch:
        raise ValueError(f"Tap 仓库当前在 {current} 分支，请切换到 {branch} 分支")
    status = require_git(tap_dir, "status", "--porcelain", "--untracked-files=all", run=run)
    if status:
        raise ValueError("Tap 仓库有未提交改动，请先处理后再更新公式：\n" + status)
    require_git(tap_dir, "fetch", "--quiet", "origin", run=run, message="无法从 origin 获取 Tap 仓库")
    remote = require_git(tap_dir, "rev-parse", f"origin/{branch}", run=run)
    if require_git(tap_dir, "rev-parse", "HEAD", run=run) != remote:
        require_git(
            tap_dir, "merge", "--quiet", "--ff-only", f"origin/{branch}", run=run,
            message=f"Tap 仓库无法快进到 origin/{branch}，请手动同步后再试",
        )
        echo(f"Tap 仓库已快进到 origin/{branch}")


def release_published(repo, tag, asset, run=execute):
    result = run([
        "gh", "release", "view", tag, "--repo", repo,
        "--json", "assets", "--jq", ".assets[].name",
    ])
    return result.returncode == 0 and asset in result.stdout.split()


def release_checksums(repo, tag, asset, wait, run=execute, sleep=time.sleep, echo=emit):
    """Return the release SHA256SUMS, waiting up to `wait` seconds for the asset."""
    waited = 0
    while not release_published(repo, tag, asset, run=run):
        if waited >= wait:
            raise ValueError(
                f"Release {tag} 还没有 {asset} 附件（等待 {wait} 秒）。"
                "确认 GitHub Actions 发布完成后再执行 make homebrew，"
                "或用 HOMEBREW_WAIT=900 让它继续等待。"
            )
        if waited % 60 == 0:
            echo(f"等待 Release {tag} 上线 {asset}…（已等待 {waited} 秒，上限 {wait} 秒）")
        step = min(POLL_SECONDS, wait - waited)
        sleep(step)
        waited += step
    with tempfile.TemporaryDirectory(prefix="lancopy-homebrew-") as directory:
        result = run([
            "gh", "release", "download", tag, "--repo", repo,
            "--pattern", "SHA256SUMS", "--dir", directory, "--clobber",
        ])
        if result.returncode:
            raise ValueError(f"下载 {tag} 的 SHA256SUMS 失败：{failure(result)}")
        checksums = Path(directory) / "SHA256SUMS"
        if not checksums.is_file():
            raise ValueError(f"{tag} 的 Release 附件缺少 SHA256SUMS")
        return checksums.read_text(encoding="utf-8")


def verify_formula(tap, formula, path, run=execute, echo=emit, online=True):
    audit = ["brew", "audit", "--strict"]
    if online:
        audit.append("--online")
    audit.extend(["--formula", f"{tap}/{formula}"])
    checks = (
        ("ruby -c", ["ruby", "-c", str(path)]),
        ("brew style", ["brew", "style", str(path)]),
        ("brew audit --strict" + (" --online" if online else ""), audit),
    )
    for label, command in checks:
        result = run(command)
        if result.returncode:
            raise ValueError(f"{label} 未通过：\n{failure(result)}")
        echo(f"{label} 通过")


def push_hint(tap_dir):
    tap = str(tap_dir)
    return (
        "推送 Tap 仓库失败，提交已保留在本地。HTTPS remote 需要凭据，两种修法任选：\n"
        f"  1) gh auth setup-git（让 git 复用 GitHub CLI 的凭据）\n"
        f"  2) git -C {tap} remote set-url origin git@github.com:jachy-h/homebrew-jachy-h.git（改用 SSH）\n"
        f"然后重试：git -C {tap} push origin HEAD:main"
    )


def commit_and_push(tap_dir, branch, formula, version, run=execute, echo=emit, push=True):
    relative = f"Formula/{formula}.rb"
    require_git(tap_dir, "add", "--", relative, run=run, message=f"无法暂存 {relative}")
    require_git(
        tap_dir, "commit", "-m", f"chore: update {formula} v{version}", run=run,
        message=f"提交 {relative} 失败",
    )
    echo(f"已提交：chore: update {formula} v{version}")
    if not push:
        echo("已跳过推送（--no-push）。")
        return
    require_git(
        tap_dir, "push", "--quiet", "origin", f"HEAD:{branch}", run=run,
        message=push_hint(tap_dir),
    )
    echo(f"已推送到 Tap 仓库 {branch} 分支")


def publish(args, run=execute, sleep=time.sleep, echo=emit):
    version = release_version(args.version)
    path = formula_path(args.tap_dir, args.formula)
    if not path.is_file():
        raise ValueError(f"未找到 {path}；先执行 brew tap {args.tap} 并确认公式名 {args.formula}")
    if not args.dry_run:
        prepare_tap(args.tap_dir, args.branch, run=run, echo=echo)
    if args.checksums:
        text = Path(args.checksums).read_text(encoding="utf-8")
    else:
        text = release_checksums(
            args.repo, f"v{version}", args.asset, args.wait, run=run, sleep=sleep, echo=echo,
        )
    digest = parse_checksums(text, args.asset)
    original = path.read_text(encoding="utf-8")
    updated = updated_formula(original, version, args.asset, digest)
    if updated == original:
        echo(f"{args.formula} 已指向 v{version}（sha256 {digest[:12]}…），无需更新")
        return 0
    if args.dry_run:
        echo(f"[dry run] {path} 将指向 v{version} {args.asset}（sha256 {digest}）")
        return 0
    path.write_text(updated, encoding="utf-8")
    try:
        if not args.no_verify:
            verify_formula(args.tap, args.formula, path, run=run, echo=echo, online=not args.no_online_audit)
    except Exception:
        # Never leave a formula that failed validation in the working tree.
        path.write_text(original, encoding="utf-8")
        raise
    commit_and_push(
        args.tap_dir, args.branch, args.formula, version,
        run=run, echo=echo, push=not args.no_push,
    )
    return 0


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--tap-dir", type=Path, required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--tap", default=DEFAULT_TAP)
    parser.add_argument("--formula", default=DEFAULT_FORMULA)
    parser.add_argument("--asset", default=DEFAULT_ASSET)
    parser.add_argument("--repo", default=DEFAULT_REPO)
    parser.add_argument("--branch", default="main")
    parser.add_argument("--checksums", type=Path, help="use a local SHA256SUMS instead of the release")
    parser.add_argument("--wait", type=int, default=0, help="seconds to wait for the release asset")
    parser.add_argument("--dry-run", action="store_true")
    parser.add_argument("--no-verify", action="store_true")
    parser.add_argument("--no-online-audit", action="store_true", help="skip the network URL checks in brew audit")
    parser.add_argument("--no-push", action="store_true")
    args = parser.parse_args()
    try:
        return publish(args)
    except (OSError, ValueError) as error:
        parser.exit(1, f"Homebrew error: {error}\n")


if __name__ == "__main__":
    raise SystemExit(main())
