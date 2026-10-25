#!/usr/bin/env python3
"""Build signed release artifacts using a selected version."""

import argparse
from contextlib import contextmanager
from datetime import datetime
import os
from pathlib import Path
import re
import subprocess
import tempfile
import time


def release_version(value):
    if not re.fullmatch(r"(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)", value):
        raise ValueError("version must be MAJOR.MINOR.PATCH, for example 0.1.0")
    return value


def next_version(version_file, override=""):
    if override:
        return release_version(override)
    current = release_version(version_file.read_text().strip() if version_file.exists() else "0.1.0")
    major, minor, patch = current.split(".")
    return f"{major}.{minor}.{int(patch) + 1}"


def build_version(version_file, override=""):
    """Return the requested version or the saved build version without incrementing it."""
    if override:
        return release_version(override)
    current = version_file.read_text().strip() if version_file.exists() else "0.1.0"
    return release_version(current)


@contextmanager
def build_lock(lock_file):
    lock_file.parent.mkdir(parents=True, exist_ok=True)
    with lock_file.open("a+b") as lock:
        if os.name == "nt":
            import msvcrt
            lock.seek(0, os.SEEK_END)
            if lock.tell() == 0:
                lock.write(b"\0")
                lock.flush()
            lock.seek(0)
            # Fail clearly instead of sharing a version/output directory.
            try:
                msvcrt.locking(lock.fileno(), msvcrt.LK_NBLCK, 1)
            except OSError as error:
                raise ValueError("another release build is running") from error
        else:
            import fcntl
            fcntl.flock(lock, fcntl.LOCK_EX)
        try:
            yield
        finally:
            if os.name == "nt":
                lock.seek(0)
                msvcrt.locking(lock.fileno(), msvcrt.LK_UNLCK, 1)
            else:
                fcntl.flock(lock, fcntl.LOCK_UN)


def save_version(version_file, version):
    version_file.parent.mkdir(parents=True, exist_ok=True)
    fd, name = tempfile.mkstemp(prefix=".version-", dir=version_file.parent)
    try:
        with os.fdopen(fd, "w") as output:
            output.write(version + "\n")
            output.flush()
            os.fsync(output.fileno())
        os.chmod(name, 0o644)
        os.replace(name, version_file)
    finally:
        Path(name).unlink(missing_ok=True)


def make_preview():
    flags = os.environ.get("MAKEFLAGS", "").split()
    short = flags[0].lstrip("-") if flags and not flags[0].startswith("--") and "=" not in flags[0] else ""
    return bool(set(short) & set("nqt")) or any(
        flag in {"--dry-run", "--just-print", "--recon", "--question", "--touch"} for flag in flags
    )


def build(version_file, override, lock_file, command, artifacts):
    # Recursive Make recipes also run under -n/-q/-t; never save a preview version.
    if make_preview():
        version = build_version(version_file, override)
        return subprocess.run([*command, f"VERSION={version}"], close_fds=False).returncode
    with build_lock(lock_file):
        version = build_version(version_file, override)
        started = time.monotonic()
        print(f"Building Lan Copy {version}…", flush=True)
        # Preserve GNU Make's inherited jobserver descriptors for make -j.
        result = subprocess.run([*command, f"VERSION={version}"], close_fds=False)
        if result.returncode:
            print("Build failed; saved version unchanged.", flush=True)
            return result.returncode
        missing = [str(p) for p in artifacts if not p.is_file()]
        if missing:
            raise ValueError("build produced no artifact: " + ", ".join(missing))
        save_version(version_file, version)
        print("\nLan Copy build complete", flush=True)
        print(f"  Version:   {version}")
        print(f"  Finished:  {datetime.now().astimezone().isoformat(timespec='seconds')}")
        print(f"  Duration:  {time.monotonic() - started:.1f}s")
        print("  Signing:   Ed25519 update packages")
        for artifact in artifacts:
            print(f"  Artifact:  {artifact.resolve()} ({artifact.stat().st_size:,} bytes)")
        return 0


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version-file", type=Path, default=Path("VERSION"))
    parser.add_argument("--version", default="")
    parser.add_argument("--lock-file", type=Path, default=Path(".config/build.lock"))
    parser.add_argument("--artifact", action="append", type=Path, default=[])
    parser.add_argument("command", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    command = args.command[1:] if args.command[:1] == ["--"] else args.command
    if not command:
        parser.error("a build command is required after --")
    try:
        return build(args.version_file, args.version, args.lock_file, command, args.artifact)
    except (OSError, ValueError) as error:
        parser.exit(1, f"Release build error: {error}\n")


if __name__ == "__main__":
    raise SystemExit(main())
