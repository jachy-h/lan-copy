#!/usr/bin/env python3
"""Create a ZIP package with portable UTF-8 paths, including on Windows."""

from pathlib import Path, PurePosixPath
import sys
from zipfile import ZIP_DEFLATED, ZipFile


def main() -> None:
    if len(sys.argv) != 4:
        raise SystemExit("usage: package_zip.py SOURCE_DIR OUTPUT_ZIP ARCHIVE_ROOT")

    source = Path(sys.argv[1])
    output = Path(sys.argv[2])
    archive_root = PurePosixPath(sys.argv[3])
    output.parent.mkdir(parents=True, exist_ok=True)
    with ZipFile(output, "w", compression=ZIP_DEFLATED, compresslevel=9) as archive:
        for path in sorted(source.rglob("*")):
            if path.is_file():
                relative = PurePosixPath(path.relative_to(source).as_posix())
                archive.write(path, str(archive_root / relative))


if __name__ == "__main__":
    main()
