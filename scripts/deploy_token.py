#!/usr/bin/env python3
"""Generate the bearer token used by the local deployment entry."""
import argparse
import os
from pathlib import Path
import secrets
import sys

ROOT = Path(__file__).resolve().parent.parent
DEFAULT_PATH = ROOT / ".config" / "deploy.token"


def generate(path, rotate=False):
    if path.exists() and not rotate:
        raise ValueError(
            f"部署令牌已存在：{path}\n"
            "如需重新生成并替换，请执行 make deploytoken ROTATE=1。"
        )
    token = secrets.token_hex(32)
    path.parent.mkdir(parents=True, exist_ok=True)
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    try:
        with os.fdopen(fd, "w") as output:
            output.write(token + "\n")
            output.flush()
            os.fsync(output.fileno())
    except Exception:
        path.unlink(missing_ok=True)
        raise
    os.chmod(path, 0o600)
    return token


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--rotate", action="store_true", help="replace an existing token")
    parser.add_argument("--output", type=Path, default=DEFAULT_PATH, help=argparse.SUPPRESS)
    args = parser.parse_args()
    try:
        token = generate(args.output, args.rotate)
    except (OSError, ValueError) as error:
        print(f"生成部署令牌失败：{error}", file=sys.stderr)
        return 1
    try:
        display = args.output.resolve().relative_to(ROOT)
    except ValueError:
        display = args.output
    print("部署令牌已生成：")
    print()
    print(f"  {token}")
    print()
    print(f"已保存到 {display}（权限 0600，已由 .gitignore 忽略）。")
    print("请将上面的令牌粘贴到目标机：设置 → 部署 → 保存令牌。")
    print(f'本机部署前设置：export LAN_COPY_DEPLOY_TOKEN="$(cat {display})"')
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
