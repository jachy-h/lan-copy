#!/usr/bin/env python3
"""Upload a signed Lan Copy package and wait for the deployed process."""
import argparse
import http.client
import json
import os
from pathlib import Path
import sys
import time
from urllib.parse import urlsplit


def connect(target, timeout):
    cls = http.client.HTTPSConnection if target.scheme == "https" else http.client.HTTPConnection
    return cls(target.hostname, target.port, timeout=timeout)


def deploy(url, package, token, timeout=90):
    target = urlsplit(url)
    if target.scheme not in ("http", "https") or not target.hostname or target.username or target.password or target.query or target.fragment:
        raise ValueError("部署地址必须是 http(s) 服务地址，不能含凭据、查询或片段")
    if not token or token.strip() != token or any(ord(c) < 33 or ord(c) > 126 for c in token):
        raise ValueError("请设置 LAN_COPY_DEPLOY_TOKEN（使用可打印 ASCII 字符且不含空格）")
    base = target.path.rstrip("/")
    # Stream the archive without loading it into memory; never follow redirects with credentials.
    with package.open("rb") as body:
        size = os.fstat(body.fileno()).st_size
        if size <= 0 or size > 128 * 1024 * 1024:
            raise ValueError("更新包必须大于 0 且不超过 128 MiB")
        conn = connect(target, 300)
        try:
            conn.request("POST", base + "/api/deploy", body, {
                "Authorization": "Bearer " + token,
                "Content-Type": "application/octet-stream", "Content-Length": str(size),
            })
            response = conn.getresponse()
            raw = response.read(65536)
            if response.status != 202:
                raise ValueError(f"部署被拒绝（HTTP {response.status}）：{raw.decode('utf-8', errors='replace')}")
            accepted = json.loads(raw)
            if not accepted.get("updateID") or not accepted.get("version"):
                raise ValueError("服务未返回有效的更新标识和版本")
        finally:
            conn.close()
    print(f"更新包已接受，等待 {accepted['version']} 重启…", flush=True)
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        conn = connect(target, min(3, max(0.1, deadline - time.monotonic())))
        try:
            conn.request("GET", base + "/api/app", headers={"Cache-Control": "no-cache"})
            response = conn.getresponse()
            raw = response.read(65536)
            if response.status == 200:
                info = json.loads(raw)
                if info.get("updateID") == "rollback-" + accepted["updateID"]:
                    raise ValueError("新版本启动失败，服务已恢复旧版本")
                if info.get("updateID") == accepted["updateID"] and info.get("version") == accepted["version"]:
                    print(f"部署成功：{info['version']}")
                    return
        except (OSError, http.client.HTTPException, json.JSONDecodeError):
            pass
        finally:
            conn.close()
        time.sleep(1)
    raise ValueError("等待部署确认超时，请检查服务及 lan-copy-update.log；勿直接重复上传")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--url", required=True)
    parser.add_argument("--package", required=True, type=Path)
    parser.add_argument("--timeout", type=float, default=90, help="重启确认等待秒数（默认 90）")
    args = parser.parse_args()
    if not 0 < args.timeout <= 3600:
        parser.error("--timeout 必须在 0 到 3600 秒之间")
    try:
        deploy(args.url, args.package, os.environ.get("LAN_COPY_DEPLOY_TOKEN", ""), args.timeout)
        return 0
    except (OSError, ValueError, http.client.HTTPException) as error:
        print(f"部署失败：{error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
