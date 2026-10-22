#!/usr/bin/env python3
"""Use the committed release version in CI without incrementing it."""

import os
from pathlib import Path
import sys

from build_release import release_version


def ci_version(version_file, ref_type, ref_name):
    version = release_version(version_file.read_text().strip())
    if ref_type == "tag" and ref_name != f"v{version}":
        raise ValueError(f"release tag must be v{version} to match VERSION (got {ref_name})")
    return version


def main():
    try:
        print(ci_version(Path("VERSION"), os.environ.get("GITHUB_REF_TYPE", "branch"),
                         os.environ.get("GITHUB_REF_NAME", "main")))
    except (OSError, ValueError) as error:
        print(f"Release version error: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
