import importlib.util
import json
import shutil
import struct
import subprocess
import tempfile
import unittest
import zipfile
from argparse import Namespace
from contextlib import redirect_stdout
from io import StringIO
from pathlib import Path
from unittest.mock import patch


SCRIPTS = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('verify_apk', SCRIPTS / 'verify-apk.py')
verify_apk = importlib.util.module_from_spec(spec)
spec.loader.exec_module(verify_apk)
PWSH = shutil.which('pwsh')


class ApkVersionTests(unittest.TestCase):
    def verify(self, root, version='0.1.0', actual_version=None, root_version='0.1.0'):
        (root / 'release.json').write_text(json.dumps({'version': root_version}), encoding='utf-8')
        apk = root / 'fixture.apk'
        elf = bytearray(120)
        elf[:6] = b'\x7fELF\x02\x01'
        struct.pack_into('<H', elf, 18, 183)
        struct.pack_into('<Q', elf, 32, 64)
        struct.pack_into('<HH', elf, 54, 56, 1)
        struct.pack_into('<I', elf, 64, 1)
        struct.pack_into('<Q', elf, 112, 16384)
        with zipfile.ZipFile(apk, 'w', zipfile.ZIP_DEFLATED) as archive:
            for name in ('libapp', 'libflutter', 'libbluegrass'):
                archive.writestr(f'lib/arm64-v8a/{name}.so', elf)
        args = Namespace(apk=apk, sdk=root, output_dir=root / 'dist', version=version,
                         build_number=100123, source_commit='a' * 40,
                         source_dirty=False, signing_mode='configured')

        def sdk(*command):
            if 'badging' in command:
                return (f"package: name='com.bluegrass.client' versionCode='100123' versionName='{actual_version or version}'\n"
                        "sdkVersion:'26'\nnative-code: 'arm64-v8a'\n")
            return 'Signer #1 certificate SHA-256 digest: ' + 'b' * 64

        with patch.object(verify_apk, 'ROOT', root, create=True), patch.object(verify_apk, 'run', side_effect=sdk), redirect_stdout(StringIO()):
            verify_apk.verify(args)
        return json.loads((root / 'dist/build-info.json').read_text(encoding='utf-8'))

    def test_export_uses_plain_root_version_and_retains_internal_build(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            metadata = self.verify(root)
            self.assertEqual(metadata['apk'], 'BlueGrass-Android-0.1.0-arm64.apk')
            self.assertEqual(metadata['version'], '0.1.0')
            self.assertEqual(metadata['buildNumber'], 100123)
            self.assertEqual((root / 'dist' / metadata['apk']).read_bytes(), (root / 'fixture.apk').read_bytes())
            self.assertIn('  BlueGrass-Android-0.1.0-arm64.apk\n', (root / 'dist/SHA256SUMS').read_text(encoding='utf-8'))

    def test_export_rejects_override_and_manifest_version_mismatch(self):
        for version, actual in [('0.2.0', None), ('01.0.0', None), ('0.1.0-dev', None), ('0.1.0', '0.5.0')]:
            with self.subTest(version=version, actual=actual), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                with self.assertRaises(ValueError):
                    self.verify(root, version=version, actual_version=actual)
                self.assertFalse((root / 'dist').exists())

    def test_export_rejects_unicode_root_even_when_override_matches(self):
        for version in ('1\u0660.2.3', '1.2\uff10.3', '1.2.3\u0660'):
            with self.subTest(version=version), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                with self.assertRaisesRegex(ValueError, 'version must be X.X.X'):
                    self.verify(root, version=version, root_version=version)
                self.assertFalse((root / 'dist').exists())


@unittest.skipUnless(PWSH, 'PowerShell is required')
class BuildVersionTests(unittest.TestCase):
    def run_build(self, version='0.1.0', build=None):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            scripts = root / 'Android/scripts'
            scripts.mkdir(parents=True)
            for name in ('build-release.ps1', 'release-config.ps1'):
                if (SCRIPTS / name).exists():
                    shutil.copy2(SCRIPTS / name, scripts / name)
            (root / 'release.json').write_text(json.dumps({'version': version}), encoding='utf-8')
            (root / 'Android/pubspec.yaml').write_text('version: 9.8.7+9\n', encoding='utf-8')
            (root / 'Android/native/core').mkdir(parents=True)
            (root / 'packages/client').mkdir(parents=True)
            (scripts / 'build-native.ps1').write_text('', encoding='utf-8')
            wrapper = root / 'exercise.ps1'
            wrapper.write_text("""
$ErrorActionPreference = 'Stop'
$global:bluegrassTestCalls = [Collections.Generic.List[object]]::new()
function flutter { $global:bluegrassTestCalls.Add(@($args)); $global:LASTEXITCODE = 0 }
function go { $global:LASTEXITCODE = 0 }
function python { $global:bluegrassTestCalls.Add(@($args)); $global:LASTEXITCODE = 0 }
function git { $global:LASTEXITCODE = 0; 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' }
Get-ChildItem Env:BLUEGRASS_ANDROID_* | Remove-Item
$env:BLUEGRASS_REQUIRE_RELEASE_SIGNING = ''
try {
  & "$PSScriptRoot/Android/scripts/build-release.ps1" -AndroidSdk $PSScriptRoot -JavaHome $PSScriptRoot @args
  ConvertTo-Json -InputObject @($global:bluegrassTestCalls.ToArray()) -Depth 5 -Compress
} catch { throw }
""", encoding='utf-8')
            command = [PWSH, '-NoProfile', '-NonInteractive', '-File', str(wrapper)]
            if build is not None:
                command += ['-BuildNumber', str(build)]
            return subprocess.run(command, capture_output=True, text=True, encoding='utf-8')

    def test_build_injects_root_version_into_manifest_and_dart(self):
        for version, number in [('0.1.0', None), ('0.1.0', 100123), ('2.3.4', 100124)]:
            with self.subTest(version=version, build=number):
                result = self.run_build(version=version, build=number)
                self.assertEqual(result.returncode, 0, result.stderr)
                calls = json.loads(result.stdout)
                flutter = next(args for args in calls if args[:2] == ['build', 'apk'])
                self.assertIn(f'--build-name={version}', flutter)
                self.assertIn(f'--dart-define=BLUEGRASS_VERSION={version}', flutter)
                self.assertIn(f'--dart-define=BLUEGRASS_BUILD={number or 9}', flutter)
                self.assertEqual(flutter[flutter.index('--build-number') + 1], str(number or 9))
                verify = calls[-1]
                self.assertEqual(verify[verify.index('--version') + 1], version)

    def test_build_rejects_invalid_root_or_decreasing_build(self):
        for version, number in [('01.2.3', None), ('0.1.0-dev', None), ('0.1.0\n', None), (None, None), ('0.1.0', 8)]:
            with self.subTest(version=version, number=number):
                result = self.run_build(version=version, build=number)
                self.assertNotEqual(result.returncode, 0)

    def test_build_rejects_unicode_digits_in_root_version(self):
        for version in ('1\u0660.2.3', '1.2\uff10.3', '1.2.3\u0660'):
            with self.subTest(version=version):
                result = self.run_build(version=version)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn('version must be X.X.X', result.stderr)


if __name__ == '__main__':
    unittest.main()
