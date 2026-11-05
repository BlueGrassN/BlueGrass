#!/usr/bin/env python3
"""Build a self-contained, ad-hoc signed macOS development test package."""

import argparse
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import shutil
import subprocess
import sys


ROOT = Path(__file__).resolve().parents[2]
APP_ROOT = ROOT / 'Mac/app'
CORE = ROOT / 'Windows/native/core'
XRAY_VERSION = 'v1.260327.1-0.20261017152356-92ccc939aaf4'
REQUIRED_PAYLOAD = (
    'Contents/Info.plist',
    'Contents/MacOS/BlueGrass',
    'Contents/MacOS/BlueGrass.Native',
    'Contents/MacOS/BlueGrass.Diagnostics',
    'Contents/MacOS/BlueGrass.ProxySettings',
    'Contents/Frameworks/FlutterMacOS.framework/FlutterMacOS',
    'Contents/Frameworks/App.framework/App',
    'Contents/Frameworks/App.framework/Resources/flutter_assets/assets/branding/wordmark-light.png',
    'Contents/Frameworks/App.framework/Resources/flutter_assets/assets/branding/wordmark-dark.png',
    'Contents/Frameworks/App.framework/Resources/flutter_assets/assets/LICENSE.Xray-core',
)


def go_arch(value):
    try:
        return {'arm64': 'arm64', 'x86_64': 'amd64'}[value]
    except KeyError as error:
        raise ValueError('Unsupported architecture') from error


def validate_version(value):
    if re.fullmatch(r'[0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9.]+)?', value) is None:
        raise ValueError('Invalid version')
    return value


def validate_payload(app):
    for relative in REQUIRED_PAYLOAD:
        path = app / relative
        if not path.is_file() or path.stat().st_size == 0:
            raise ValueError(f'Missing or empty payload: {relative}')
    for path in app.rglob('*'):
        name = path.name.lower()
        if path.suffix.lower() in {'.exe', '.p12', '.p8', '.mobileprovision'} or (
            name.startswith('.env') or name == 'testaccount.txt' or name.endswith('.private.json')
        ):
            raise ValueError(f'Forbidden payload: {path.relative_to(app)}')


def run(args, *, cwd=ROOT, env=None, capture=False):
    print('+', ' '.join(str(value) for value in args), flush=True)
    result = subprocess.run(
        [str(value) for value in args], cwd=cwd, env=env, check=True,
        text=True, stdout=subprocess.PIPE if capture else None,
    )
    return result.stdout.strip() if capture else None


def sha256(path):
    digest = hashlib.sha256()
    with path.open('rb') as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b''):
            digest.update(chunk)
    return digest.hexdigest()


def build(args):
    if sys.platform != 'darwin':
        raise SystemExit('macOS + Xcode are required. Use the macOS development build GitHub workflow.')
    version = validate_version(args.version)
    target = go_arch(args.arch)
    if args.build_number < 1:
        raise ValueError('Build number must be positive')
    if platform.machine() != args.arch:
        raise ValueError('Run each build on a runner matching its target architecture')

    run([sys.executable, ROOT / 'Mac/tools/sync_assets.py'])
    run(['flutter', 'pub', 'get'], cwd=APP_ROOT)
    run(['flutter', 'config', '--enable-macos-desktop', '--no-enable-macos-arm64-only'])
    environment = dict(os.environ)
    environment.update({
        'FLUTTER_XCODE_ARCHS': args.arch,
        'FLUTTER_XCODE_ONLY_ACTIVE_ARCH': 'NO',
        'FLUTTER_XCODE_CODE_SIGNING_ALLOWED': 'NO',
        'FLUTTER_XCODE_MACOSX_DEPLOYMENT_TARGET': '13.0',
    })
    run([
        'flutter', 'build', 'macos', '--release',
        f'--build-name={version.split("-", 1)[0]}', f'--build-number={args.build_number}',
        f'--dart-define=BLUEGRASS_VERSION={version}',
        f'--dart-define=BLUEGRASS_BUILD={args.build_number}',
    ], cwd=APP_ROOT, env=environment)
    app = APP_ROOT / 'build/macos/Build/Products/Release/BlueGrass.app'
    if not app.is_dir():
        raise ValueError(f'Built app was not found: {app}')
    executables = app / 'Contents/MacOS'
    environment.update({'GOOS': 'darwin', 'GOARCH': target, 'CGO_ENABLED': '0'})
    for command, filename in (
        ('bluegrass-native', 'BlueGrass.Native'),
        ('bluegrass-diagnostics', 'BlueGrass.Diagnostics'),
    ):
        run(['go', 'build', '-trimpath', '-ldflags=-s -w', '-o', executables / filename,
             './cmd/' + command], cwd=CORE, env=environment)

    swift_package = ROOT / 'Mac/native/ProxySettings'
    run(['swift', 'test', '--package-path', swift_package])
    swift_build = ['swift', 'build', '--package-path', swift_package, '-c', 'release', '--arch', args.arch]
    run(swift_build)
    swift_bin = Path(run([*swift_build, '--show-bin-path'], capture=True))
    shutil.copy2(swift_bin / 'BlueGrass.ProxySettings', executables / 'BlueGrass.ProxySettings')

    resources = app / 'Contents/Resources'
    licenses = resources / 'ThirdPartyNotices'
    licenses.mkdir(parents=True, exist_ok=True)
    for name in ('LICENSE.Xray-core', 'LICENSE.sing'):
        shutil.copy2(ROOT / 'Windows/app/assets' / name, licenses / name)
    shutil.copytree(CORE / 'routingpolicy/assets/licenses', licenses / 'routing-data', dirs_exist_ok=True)
    for name in ('NOTICE.md', 'metadata.json'):
        shutil.copy2(CORE / 'routingpolicy/assets' / name, licenses / ('routing-' + name))
    for name in ('go.mod', 'go.sum'):
        shutil.copy2(CORE / name, licenses / name)

    validate_payload(app)
    helper_info = run(['go', 'version', '-m', executables / 'BlueGrass.Native'], capture=True)
    if XRAY_VERSION not in helper_info or 'github.com/sagernet/sing-box' in helper_info:
        raise ValueError('Packaged native host does not match the required Xray dependency')
    for relative in REQUIRED_PAYLOAD:
        path = app / relative
        if path.parent == executables or path.name in {'FlutterMacOS', 'App'}:
            run(['xcrun', 'lipo', '-verify_arch', args.arch, path])
    for path in executables.iterdir():
        if path.is_file():
            path.chmod(0o755)
    # Sign nested code before the outer app. Do not label ad-hoc signatures as Developer ID.
    for path in sorted((app / 'Contents/Frameworks').rglob('*.dylib')):
        run(['codesign', '--force', '--sign', '-', '--timestamp=none', path])
    for path in sorted((app / 'Contents/Frameworks').rglob('*.framework'), key=lambda p: -len(p.parts)):
        run(['codesign', '--force', '--sign', '-', '--timestamp=none', path])
    for filename in ('BlueGrass.Native', 'BlueGrass.Diagnostics', 'BlueGrass.ProxySettings'):
        run(['codesign', '--force', '--sign', '-', '--timestamp=none', executables / filename])
    run(['codesign', '--force', '--sign', '-', '--timestamp=none', '--entitlements',
         APP_ROOT / 'macos/Runner/Release.entitlements', app])
    run(['codesign', '--verify', '--deep', '--strict', '--verbose=2', app])

    output = ROOT / 'Mac/dist' / args.arch
    output.mkdir(parents=True, exist_ok=True)
    archive = output / f'BlueGrass-macOS-{args.arch}-{version}+{args.build_number}.zip'
    if archive.exists():
        raise ValueError('Refusing to overwrite an existing package; use a new build number or output directory')
    # ditto preserves .app symlinks, executable permissions and macOS metadata.
    run(['ditto', '-c', '-k', '--sequesterRsrc', '--keepParent', app, archive])
    shutil.copy2(ROOT / 'Mac/docs/TESTING.md', output / 'TESTING.md')
    metadata = {
        'application': 'BlueGrass', 'version': version, 'build': args.build_number,
        'architecture': args.arch, 'minimum_macos': '13.0',
        'commit': run(['git', 'rev-parse', 'HEAD'], capture=True),
        'built_at_utc': datetime.now(timezone.utc).isoformat(),
        'signing': 'ad-hoc development; not notarized',
        'tun_available': False, 'system_proxy_device_test': 'pending user testing',
        'archive': archive.name, 'sha256': sha256(archive),
        'flutter': json.loads(run(['flutter', '--version', '--machine'], capture=True)),
        'go': run(['go', 'version'], capture=True),
        'xcode': run(['xcodebuild', '-version'], capture=True),
        'native_modules': helper_info,
    }
    (output / 'BUILD.json').write_text(json.dumps(metadata, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
    (output / 'SHA256SUMS.txt').write_text(f'{metadata["sha256"]}  {archive.name}\n', encoding='utf-8')
    print(f'Package verified: {archive}', flush=True)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--arch', choices=['arm64', 'x86_64'], required=True)
    parser.add_argument('--version', default='0.1.0-dev')
    parser.add_argument('--build-number', type=int, default=1)
    build(parser.parse_args())
