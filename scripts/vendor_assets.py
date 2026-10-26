#!/usr/bin/env python3
"""Verify offline vendor assets, verify upstream provenance, or explicitly update a pin."""
import argparse
import base64
import hashlib
import io
import json
from pathlib import Path, PurePosixPath
import tarfile
import urllib.request

ROOT = Path(__file__).resolve().parent.parent


def safe_path(root, relative):
    name = PurePosixPath(relative)
    if name.is_absolute() or '..' in name.parts or not name.parts:
        raise ValueError(f'Invalid vendor path: {relative}')
    candidate = root / relative
    if candidate.is_symlink() or not candidate.resolve().is_relative_to(root.resolve()):
        raise ValueError(f'Invalid vendor path: {relative}')
    return candidate


def check(root, manifest):
    vendor = root / 'web/vendor'
    expected = set(manifest['projectFiles'])
    for package in manifest['packages']:
        for item in package['files']:
            if item['path'] in expected:
                raise ValueError(f'Duplicate vendor path: {item["path"]}')
            expected.add(item['path'])
            file = safe_path(vendor, item['path'])
            if hashlib.sha256(file.read_bytes()).hexdigest() != item['sha256']:
                raise ValueError(f'Vendor checksum mismatch: {item["path"]}')
    actual = {p.relative_to(vendor).as_posix() for p in vendor.rglob('*') if p.is_file()}
    if expected != actual:
        raise ValueError(f'Vendor file list differs: missing={sorted(expected-actual)}, extra={sorted(actual-expected)}')


def upstream_files(package):
    if not package['url'].startswith('https://'):
        raise ValueError('Vendor downloads require HTTPS')
    with urllib.request.urlopen(package['url'], timeout=45) as response:
        data = response.read()
    algorithm, expected = package['integrity'].split('-', 1)
    if algorithm != 'sha512' or base64.b64encode(hashlib.sha512(data).digest()).decode() != expected:
        raise ValueError(f'Upstream integrity mismatch: {package["name"]}')
    files = []
    with tarfile.open(fileobj=io.BytesIO(data)) as archive:
        for item in package['files']:
            source = PurePosixPath(item['source'])
            if source.is_absolute() or '..' in source.parts:
                raise ValueError('Invalid archive source path')
            member = archive.getmember('package/' + str(source))
            if not member.isfile():
                raise ValueError(f'Not a regular upstream file: {source}')
            files.append((item, archive.extractfile(member).read()))
    return files


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument('--check', action='store_true', help='offline checksum and file-list check (default)')
    mode.add_argument('--verify-upstream', action='store_true', help='download pinned tarballs and compare every selected file')
    mode.add_argument('--update', metavar='NAME', help='download and install a package after editing its version/URL/integrity pin')
    args = parser.parse_args()
    manifest_path = ROOT / 'frontend/vendor-manifest.json'
    manifest = json.loads(manifest_path.read_text())
    if args.update:
        matches = [p for p in manifest['packages'] if p['name'] == args.update]
        if len(matches) != 1:
            parser.error('Choose an exact package name from vendor-manifest.json')
        package = matches[0]
        # Validate the full upstream archive and all destination paths before writing.
        files = [(item, data, safe_path(ROOT / 'web/vendor', item['path'])) for item, data in upstream_files(package)]
        for item, data, destination in files:
            destination.parent.mkdir(parents=True, exist_ok=True)
            destination.write_bytes(data)
            item['sha256'] = hashlib.sha256(data).hexdigest()
        manifest_path.write_text(json.dumps(manifest, indent=2) + '\n')
    check(ROOT, manifest)
    if args.verify_upstream:
        for package in manifest['packages']:
            for item, data in upstream_files(package):
                if hashlib.sha256(data).hexdigest() != item['sha256']:
                    raise ValueError(f'Upstream file differs: {item["path"]}')
    print('Vendor checks passed' + (' (including upstream tarballs)' if args.verify_upstream else ' (offline)'))


if __name__ == '__main__':
    main()
