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


stable_version = re.compile(
    r"(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-build([1-9][0-9]*))?"
)


def release_version(value):
    if not stable_version.fullmatch(value):
        raise ValueError("version must be x.y.z or x.y.z-buildN, for example 0.8.1-build3")
    return value


def parse_version(value):
    """Return ((major, minor, patch), build); a version without a suffix is build 0."""
    matched = stable_version.fullmatch(value)
    if not matched:
        raise ValueError("version must be x.y.z or x.y.z-buildN, for example 0.8.1-build3")
    base = tuple(int(matched.group(index)) for index in range(1, 4))
    return base, int(matched.group(4) or 0)


def format_version(base, build):
    version = ".".join(str(part) for part in base)
    return f"{version}-build{build}" if build else version


def saved_version(version_file):
    value = version_file.read_text().strip() if version_file.exists() else "0.1.0"
    return release_version(value)


def next_build_version(value):
    """Advance the build number, or start a new base version at build 1."""
    base, build = parse_version(release_version(value))
    return format_version(base, build + 1)


def resolve_version(current, override=""):
    """Resolve one build without saving it, keeping build numbers monotonic."""
    current_base, current_build = parse_version(release_version(current))
    if not override:
        return format_version(current_base, current_build + 1)
    requested = release_version(override)
    requested_base, requested_build = parse_version(requested)
    if requested_build:
        candidate = requested
    elif requested_base > current_base:
        # A new base version always starts at its first build.
        candidate = format_version(requested_base, 1)
    elif requested_base == current_base:
        candidate = format_version(current_base, current_build + 1)
    else:
        raise ValueError(f"version {requested} must not be lower than current version {current}")
    if parse_version(candidate) < (current_base, current_build):
        raise ValueError(f"version {candidate} must not be lower than current version {current}")
    return candidate


def build_version(version_file, override=""):
    """Return the version for the next build, or the exact requested version."""
    return resolve_version(saved_version(version_file), override)


def publish_version(version_file, override=""):
    """Return the version for a release, which must be strictly newer."""
    current = saved_version(version_file)
    version = resolve_version(current, override)
    if parse_version(version) <= parse_version(current):
        raise ValueError(f"published version {version} must be greater than current version {current}")
    return version


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
