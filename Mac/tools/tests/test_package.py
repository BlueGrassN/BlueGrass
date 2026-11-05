import importlib.util
from pathlib import Path
import tempfile
import unittest


spec = importlib.util.spec_from_file_location('mac_build', Path(__file__).parents[1] / 'build.py')
build = importlib.util.module_from_spec(spec)
spec.loader.exec_module(build)


class PackageTests(unittest.TestCase):
    def test_architecture_and_version_reject_unusable_package_labels(self):
        self.assertEqual(build.go_arch('arm64'), 'arm64')
        self.assertEqual(build.go_arch('x86_64'), 'amd64')
        with self.assertRaises(ValueError):
            build.go_arch('arm64; anything')
        for value in ['../name', '0.1.0\n', '', 'foo']:
            with self.assertRaises(ValueError):
                build.validate_version(value)

    def test_payload_requires_real_helpers_assets_and_frameworks(self):
        with tempfile.TemporaryDirectory() as directory:
            app = Path(directory) / 'BlueGrass.app'
            with self.assertRaises(ValueError):
                build.validate_payload(app)
            for part in build.REQUIRED_PAYLOAD:
                target = app / part
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_bytes(b'payload')
            build.validate_payload(app)
            (app / 'Contents/MacOS/BlueGrass.Native').unlink()
            with self.assertRaisesRegex(ValueError, 'BlueGrass.Native'):
                build.validate_payload(app)

    def test_windows_binaries_and_credentials_cannot_enter_mac_package(self):
        with tempfile.TemporaryDirectory() as directory:
            app = Path(directory) / 'BlueGrass.app'
            for part in build.REQUIRED_PAYLOAD:
                target = app / part
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_bytes(b'payload')
            unexpected = app / 'Contents/MacOS/BlueGrass.Update.exe'
            unexpected.write_bytes(b'windows executable')
            with self.assertRaisesRegex(ValueError, 'Forbidden'):
                build.validate_payload(app)
            unexpected.unlink()
            secret = app / 'Contents/Resources/testaccount.txt'
            secret.parent.mkdir(parents=True, exist_ok=True)
            secret.write_text('credentials', encoding='utf-8')
            with self.assertRaisesRegex(ValueError, 'Forbidden'):
                build.validate_payload(app)


if __name__ == '__main__':
    unittest.main()
