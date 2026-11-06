#!/usr/bin/env python3
"""Copy canonical desktop assets to the thin launcher; --check never writes.

Run from any directory before Flutter pub get/test/build. No image conversion or
license rewriting is performed. Resolution variants retain their relative keys.
"""

import argparse
from pathlib import Path
import shutil
import sys


ROOT = Path(__file__).resolve().parents[2]
SOURCE = ROOT / "Windows" / "app" / "assets"
DESTINATION = ROOT / "Mac" / "app" / "assets"


def sync_assets(source: Path, destination: Path, *, check: bool = False) -> list[str]:
    files = [source / "LICENSE.Xray-core", source / "LICENSE.sing"]
    files += sorted((source / "branding").rglob("*.png"))
    if not (source / "branding" / "wordmark-light.png").is_file():
        raise FileNotFoundError("Missing canonical light wordmark")
    if not (source / "branding" / "wordmark-dark.png").is_file():
        raise FileNotFoundError("Missing canonical dark wordmark")
    differences = []
    for original in files:
        relative = original.relative_to(source)
        target = destination / relative
        if not target.is_file() or target.read_bytes() != original.read_bytes():
            if check:
                differences.append(relative.as_posix())
            else:
                target.parent.mkdir(parents=True, exist_ok=True)
                shutil.copyfile(original, target)
    return differences


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true", help="Fail on missing or differing copies")
    options = parser.parse_args()
    differences = sync_assets(SOURCE, DESTINATION, check=options.check)
    if differences:
        print("Shared assets need synchronization: " + ", ".join(differences), file=sys.stderr)
        return 1
    print("Shared desktop assets verified." if options.check else "Shared desktop assets synchronized.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
