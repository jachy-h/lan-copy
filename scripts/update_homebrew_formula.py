#!/usr/bin/env python3
"""Point the Homebrew formula at a published release asset and checksum."""

import argparse
import os
import re
from pathlib import Path

from build_release import release_version

RELEASE_URL = "https://github.com/jachy-h/lan-copy/releases/download"
URL_LINE = re.compile(r'^(?P<indent>[ \t]*)url "(?P<url>[^"]+)"(?P<eol>\r?\n?)$')
SHA_LINE = re.compile(r'^(?P<indent>[ \t]*)sha256 "(?P<digest>[0-9a-f]{64})"(?P<eol>\r?\n?)$')
CHECKSUM_LINE = re.compile(r'^(?P<digest>[0-9a-fA-F]{64})[ \t]+\*?(?P<name>.+?)$')


def parse_checksums(text, asset):
    """Return the single sha256 for asset from a SHA256SUMS file."""
    digests = []
    for raw in text.splitlines():
        line = raw.strip()
        if not line:
            continue
        matched = CHECKSUM_LINE.match(line)
        if not matched or matched.group("name").strip() != asset:
            continue
        digests.append(matched.group("digest").lower())
    if not digests:
        raise ValueError(f"{asset} is missing from the checksum file")
    if len(digests) > 1:
        raise ValueError(f"{asset} appears more than once in the checksum file")
    return digests[0]


def pipeline_url(version, asset):
    return f"{RELEASE_URL}/v{version}/{asset}"


def updated_formula(text, version, asset, digest):
    """Return formula text pointing at version/asset/digest, or the input when unchanged."""
    lines = text.splitlines(keepends=True)
    release_lines = {}
    for index, line in enumerate(lines):
        matched = URL_LINE.match(line)
        if matched and matched.group("url").startswith(RELEASE_URL):
            release_lines[index] = matched
    if not release_lines:
        raise ValueError("no Lan Copy release URL found in the formula")
    matching = [
        index
        for index, matched in release_lines.items()
        if matched.group("url").endswith(f"/{asset}")
    ]
    if not matching:
        raise ValueError(f"no release URL for {asset} found in the formula")
    if len(matching) > 1:
        raise ValueError(f"more than one release URL for {asset} found in the formula")

    url_index = matching[0]
    url_line = release_lines[url_index]
    sha_line = SHA_LINE.match(lines[url_index + 1]) if url_index + 1 < len(lines) else None
    if sha_line is None:
        raise ValueError(f"line {url_index + 2} must be the sha256 for {asset}")

    target_url = pipeline_url(version, asset)
    if url_line.group("url") == target_url and sha_line.group("digest") == digest:
        return text

    updated = list(lines)
    updated[url_index] = f'{url_line.group("indent")}url "{target_url}"{url_line.group("eol")}'
    updated[url_index + 1] = f'{sha_line.group("indent")}sha256 "{digest}"{sha_line.group("eol")}'
    return "".join(updated)


def update_formula(formula, checksums, version, asset, dry_run=False):
    """Update formula in place; return a short report and whether anything changed."""
    version = release_version(version)
    digest = parse_checksums(checksums.read_text(encoding="utf-8"), asset)
    original = formula.read_text(encoding="utf-8")
    updated = updated_formula(original, version, asset, digest)
    if updated == original:
        return f"{formula} already points at v{version} ({digest[:12]}…)", False
    if not dry_run:
        temporary = formula.with_name(f".{formula.name}.tmp")
        try:
            temporary.write_text(updated, encoding="utf-8")
            os.replace(temporary, formula)
        finally:
            temporary.unlink(missing_ok=True)
    report = f"{formula} -> v{version} {asset} sha256 {digest}"
    return (f"{report} (dry run)" if dry_run else report), True


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--formula", type=Path, required=True)
    parser.add_argument("--checksums", type=Path, required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--asset", required=True)
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args()
    try:
        report, _ = update_formula(
            args.formula, args.checksums, args.version, args.asset, args.dry_run
        )
    except (OSError, ValueError) as error:
        parser.exit(1, f"Homebrew formula error: {error}\n")
    print(report)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
