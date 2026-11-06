#!/usr/bin/env python3
"""Check the final packaged worker against Native's embedded TUN identity."""

import argparse
import hashlib
import json
from pathlib import Path
import re
import subprocess


def sha256(path):
    digest = hashlib.sha256()
    with path.open('rb') as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b''):
            digest.update(chunk)
    return digest.hexdigest()


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError('Duplicate native worker-info field')
        result[key] = value
    return result


def verify_native(app, *, expected_worker_sha256=None):
    executables = Path(app) / 'Contents/MacOS'
    worker = executables / 'BlueGrass.Tunnel'
    native = executables / 'BlueGrass.Native'
    for path in (worker, native):
        if path.is_symlink() or not path.is_file() or path.stat().st_size == 0:
            raise ValueError(f'Missing, empty or symlinked native payload: {path.name}')
    digest = sha256(worker)
    if expected_worker_sha256 is not None and digest != expected_worker_sha256:
        raise ValueError('Packaged worker changed after its final signature/hash')
    result = subprocess.run(
        [str(native), '--tun-worker-info'], check=True, capture_output=True,
        text=True, timeout=10,
    )
    if len(result.stdout) > 4096:
        raise ValueError('Oversized native worker-info response')
    info = json.loads(result.stdout, object_pairs_hook=unique_object)
    if not isinstance(info, dict) or set(info) != {'expectedWorkerSHA256', 'protocolVersion'}:
        raise ValueError('Invalid native worker-info schema')
    if type(info['protocolVersion']) is not int or info['protocolVersion'] != 1:
        raise ValueError('Incompatible native worker-info protocol')
    expected = info['expectedWorkerSHA256']
    if not isinstance(expected, str) or re.fullmatch(r'[0-9a-f]{64}', expected) is None:
        raise ValueError('Native is missing a valid embedded worker SHA-256')
    if expected != digest:
        raise ValueError('Native embedded worker SHA-256 does not match packaged worker')
    return digest


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--app', type=Path, required=True)
    parser.add_argument('--expected-worker-sha256')
    args = parser.parse_args()
    try:
        digest = verify_native(args.app, expected_worker_sha256=args.expected_worker_sha256)
    except (ValueError, OSError, subprocess.SubprocessError) as error:
        raise SystemExit(f'Native verification failed: {error}') from error
    print(json.dumps({'workerSHA256': digest, 'protocolVersion': 1}))


if __name__ == '__main__':
    main()
