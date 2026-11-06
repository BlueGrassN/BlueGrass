import json
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


SCRIPTS = Path(__file__).resolve().parents[1]
PWSH = shutil.which('pwsh')


@unittest.skipUnless(PWSH, 'PowerShell is required')
class ReleaseVersionTests(unittest.TestCase):
    def test_direct_build_injects_root_version_before_packaging(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            scripts = root / 'Windows/scripts'
            scripts.mkdir(parents=True)
            (root / 'Windows/app').mkdir()
            for name in ('build.ps1', 'release-config.ps1'):
                if (SCRIPTS / name).exists():
                    shutil.copy2(SCRIPTS / name, scripts / name)
            (root / 'release.json').write_text('{"version":"2.3.4"}', encoding='utf-8')
            (root / 'Windows/release.json').write_text('{"buildBase":22}', encoding='utf-8')
            (scripts / 'build-native.ps1').write_text('', encoding='utf-8')
            (scripts / 'toolchain.ps1').write_text("$Flutter = 'Test-Flutter'", encoding='utf-8')
            wrapper = root / 'exercise.ps1'
            wrapper.write_text("""
$ErrorActionPreference = 'Stop'
function Test-Flutter {
  $global:LASTEXITCODE = 0
  if ($args[0] -eq 'build') {
    ConvertTo-Json -InputObject @($args) -Compress
    throw 'STOP_AFTER_FLUTTER'
  }
}
try { & "$PSScriptRoot/Windows/scripts/build.ps1" }
catch { if ($_.Exception.Message -ne 'STOP_AFTER_FLUTTER') { throw } }
""", encoding='utf-8')
            result = subprocess.run([PWSH, '-NoProfile', '-NonInteractive', '-File', str(wrapper)],
                                    capture_output=True, text=True, encoding='utf-8')
            self.assertEqual(result.returncode, 0, result.stderr)
            args = json.loads(result.stdout)
            self.assertIn('--build-name=2.3.4', args)
            self.assertIn('--dart-define=BLUEGRASS_VERSION=2.3.4', args)
            self.assertIn('--build-number=22', args)
            self.assertIn('--dart-define=BLUEGRASS_BUILD=22', args)

    def test_installer_rejects_version_override_before_signing(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            scripts = root / 'Windows/scripts'
            scripts.mkdir(parents=True)
            (root / 'Windows/installer').mkdir()
            for name in ('build-installer.ps1', 'release-config.ps1'):
                if (SCRIPTS / name).exists():
                    shutil.copy2(SCRIPTS / name, scripts / name)
            (root / 'release.json').write_text('{"version":"2.3.4"}', encoding='utf-8')
            (root / 'Windows/release.json').write_text('{"buildBase":22}', encoding='utf-8')
            (root / 'Windows/installer/signing.ps1').write_text("throw 'SIGNING_TOUCHED'", encoding='utf-8')
            result = subprocess.run([PWSH, '-NoProfile', '-NonInteractive', '-File',
                                     str(scripts / 'build-installer.ps1'), '-Version', '0.1.0', '-Build', '22'],
                                    capture_output=True, text=True, encoding='utf-8')
            self.assertNotEqual(result.returncode, 0)
            self.assertIn('Version must match root release.json', result.stderr)
            self.assertNotIn('SIGNING_TOUCHED', result.stderr)

    def package(self, version='0.1.0', override=None, build=None):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            scripts = root / 'Windows/scripts'
            scripts.mkdir(parents=True)
            for name in ('package.ps1', 'release-config.ps1'):
                if (SCRIPTS / name).exists():
                    shutil.copy2(SCRIPTS / name, scripts / name)
            (root / 'release.json').write_text(json.dumps({'version': version}), encoding='utf-8')
            (root / 'Windows/release.json').write_text('{"version":"9.8.7","buildBase":22}', encoding='utf-8')
            (root / 'Windows/dist').mkdir()
            (root / 'Windows/dist/BlueGrass-Windows-x64-dev.zip').write_bytes(b'archive fixture')
            (scripts / 'toolchain.ps1').write_text("function Get-BlueGrassPython { 'python' }", encoding='utf-8')
            (scripts / 'check.ps1').write_text('', encoding='utf-8')
            (scripts / 'build.ps1').write_text('param($Version, $Build)\n@{version=$Version;build=$Build} | ConvertTo-Json | Set-Content -Encoding utf8 "$PSScriptRoot/compiled.json"', encoding='utf-8')
            wrapper = root / 'exercise.ps1'
            wrapper.write_text("""
$ErrorActionPreference = 'Stop'
function python { $global:LASTEXITCODE = 0 }
function git { $global:LASTEXITCODE = 0; if ($args -contains 'rev-parse') { 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' } }
& "$PSScriptRoot/Windows/scripts/package.ps1" @args
""", encoding='utf-8')
            command = [PWSH, '-NoProfile', '-NonInteractive', '-File', str(wrapper)]
            if override is not None:
                command += ['-Version', override]
            if build is not None:
                command += ['-Build', str(build)]
            result = subprocess.run(command, capture_output=True, text=True, encoding='utf-8')
            metadata = root / 'Windows/dist/release/build-info.json'
            compiled = scripts / 'compiled.json'
            return (result, json.loads(metadata.read_text(encoding='utf-8-sig')) if metadata.exists() else None,
                    json.loads(compiled.read_text(encoding='utf-8-sig')) if compiled.exists() else None)

    def test_package_uses_root_version_and_preserves_build_base(self):
        for version, override, number in [('0.1.0', None, None), ('0.1.0', '0.1.0', 45), ('2.3.4', None, 46)]:
            with self.subTest(version=version, override=override, number=number):
                result, metadata, compiled = self.package(version=version, override=override, build=number)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(metadata['version'], version)
                self.assertEqual(metadata['build'], number or 22)
                self.assertEqual(compiled, {'version': version, 'build': number or 22})
                self.assertEqual(metadata['archive'], 'BlueGrass-Windows-x64.zip')

    def test_package_rejects_invalid_version_override_and_build_before_compiling(self):
        for version, override, number in [('01.2.3', None, None), ('0.1.0-beta.1', None, None),
                                          ('0.1.0\n', None, None), (123, None, None),
                                          ('65536.1.0', None, None), ('0.1.0', '0.2.0', None),
                                          ('0.1.0', None, 21)]:
            with self.subTest(version=version, override=override, number=number):
                result, metadata, compiled = self.package(version, override, number)
                self.assertNotEqual(result.returncode, 0)
                self.assertIsNone(compiled)
                self.assertIsNone(metadata)

    def test_package_rejects_unicode_digits_in_root_version(self):
        for version in ('1\u0660.2.3', '1.2\uff10.3', '1.2.3\u0660'):
            with self.subTest(version=version):
                result, metadata, compiled = self.package(version=version)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn('version must be X.X.X', result.stderr)
                self.assertIsNone(compiled)
                self.assertIsNone(metadata)


if __name__ == '__main__':
    unittest.main()
