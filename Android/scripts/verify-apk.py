"""Verify the installable ARM64 release before exporting distribution files."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import struct
import subprocess
import zipfile


def run(*args):
    return subprocess.check_output([str(a) for a in args], text=True, encoding="utf-8", errors="replace", stderr=subprocess.STDOUT)


def check(condition, message):
    if not condition:
        raise ValueError(message)


def verify(args):
    apk = Path(args.apk).resolve(strict=True)
    tools = Path(args.sdk) / "build-tools" / "36.0.0"
    exe = ".exe" if os.name == "nt" else ""
    aapt = tools / f"aapt{exe}"
    signature = run(tools / ("apksigner.bat" if os.name == "nt" else "apksigner"), "verify", "--verbose", "--print-certs", apk)
    cert = re.search(r"Signer #1 certificate SHA-256 digest: ([a-fA-F0-9]{64})", signature)
    check(cert is not None, "APK has no verified signer certificate")
    badging = run(aapt, "dump", "badging", apk)
    check("package: name='com.bluegrass.client'" in badging, "Unexpected application ID")
    check(f"versionCode='{args.build_number}'" in badging, "Unexpected versionCode")
    check(f"versionName='{args.version}'" in badging, "Unexpected versionName")
    check("sdkVersion:'26'" in badging, "Unexpected minimum Android SDK")
    check("application-debuggable" not in badging, "Debuggable APK cannot be distributed")
    check("native-code: 'arm64-v8a'" in badging, "Expected ARM64 APK")
    run(tools / f"zipalign{exe}", "-c", "-P", "16", "4", apk)
    libraries = []
    with zipfile.ZipFile(apk) as archive:
        check(archive.testzip() is None, "APK ZIP integrity failure")
        names = archive.namelist()
        check(len(names) == len(set(names)), "Duplicate APK ZIP entries")
        native = {name for name in names if name.startswith("lib/") and name.endswith(".so")}
        expected = {f"lib/arm64-v8a/{name}.so" for name in ("libapp", "libflutter", "libbluegrass")}
        check(native == expected, "Unexpected native libraries or non-ARM64 architecture")
        check(not any("kernel_blob.bin" in name or "vm_snapshot_data" in name for name in names), "Debug Dart payload found")
        for name in sorted(native):
            info = archive.getinfo(name)
            check(info.compress_type == zipfile.ZIP_DEFLATED, f"Native library is not compressed: {name}")
            data = archive.read(name)
            check(data[:6] == b"\x7fELF\x02\x01", f"Expected ELF64 little-endian: {name}")
            check(struct.unpack_from("<H", data, 18)[0] == 183, f"Expected AArch64 ELF: {name}")
            offset = struct.unpack_from("<Q", data, 32)[0]
            size, count = struct.unpack_from("<HH", data, 54)
            loads = 0
            for index in range(count):
                header = offset + index * size
                if struct.unpack_from("<I", data, header)[0] == 1:
                    loads += 1
                    alignment = struct.unpack_from("<Q", data, header + 48)[0]
                    check(alignment >= 16384 and alignment % 16384 == 0, f"ELF is not 16KB aligned: {name}")
            check(loads > 0, f"No ELF load segments: {name}")
            libraries.append({"name": name, "bytes": len(data), "sha256": hashlib.sha256(data).hexdigest()})
    output = Path(args.output_dir)
    output.mkdir(parents=True, exist_ok=True)
    filename = f"BlueGrass-Android-{args.version}+{args.build_number}-arm64.apk"
    destination = output / filename
    shutil.copyfile(apk, destination)
    sha256 = hashlib.sha256(destination.read_bytes()).hexdigest()
    metadata = {
        "apk": filename, "version": args.version, "buildNumber": args.build_number,
        "bytes": destination.stat().st_size, "sha256": sha256,
        "abi": "arm64-v8a", "sourceCommit": args.source_commit,
        "sourceDirty": args.source_dirty,
        "signingMode": args.signing_mode, "certificateSha256": cert.group(1).lower(),
        "tag": f"android-v{args.version}-build.{args.build_number}",
        "libraries": libraries,
        "validation": "Signature, ZIP integrity, non-debuggable manifest, ARM64 ABI, ZIP/ELF alignment; not device runtime acceptance",
    }
    (output / "build-info.json").write_text(json.dumps(metadata, indent=2) + "\n", encoding="utf-8")
    (output / "SHA256SUMS").write_text(f"{sha256}  {filename}\n", encoding="utf-8")
    print(json.dumps({"apk": str(destination.resolve()), "bytes": metadata["bytes"], "sha256": sha256}, indent=2))


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    for field in ("apk", "sdk", "output-dir", "version", "source-commit"):
        parser.add_argument(f"--{field}", required=True)
    parser.add_argument("--build-number", required=True, type=int)
    parser.add_argument("--signing-mode", choices=("configured", "local-debug"), required=True)
    parser.add_argument("--source-dirty", action="store_true")
    verify(parser.parse_args())
