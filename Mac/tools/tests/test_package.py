import importlib.util
import argparse
from contextlib import ExitStack
import json
from pathlib import Path
import tempfile
import unittest
import sys
import subprocess
from unittest.mock import patch


sys.path.insert(0, str(Path(__file__).parents[1]))
spec = importlib.util.spec_from_file_location('mac_build', Path(__file__).parents[1] / 'build.py')
build = importlib.util.module_from_spec(spec)
spec.loader.exec_module(build)


class PackageTests(unittest.TestCase):
    def exercise_build(self, directory, *, corrupt_worker=False):
        root = Path(directory)
        app_root = root / 'Mac/app'
        core = root / 'Windows/native/core'
        app = app_root / 'build/macos/Build/Products/Release/BlueGrass.app'
        for relative in build.REQUIRED_PAYLOAD:
            path = app / relative
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(b'fixture')
        swift_bin = root / 'swift-bin'
        swift_bin.mkdir()
        (swift_bin / 'BlueGrass.ProxySettings').write_bytes(b'swift helper')
        for relative in ('Mac/docs/TESTING.md', 'Windows/app/assets/LICENSE.Xray-core',
                         'Windows/app/assets/LICENSE.sing', 'Windows/native/core/go.mod',
                         'Windows/native/core/go.sum', 'Windows/native/core/routingpolicy/assets/NOTICE.md',
                         'Windows/native/core/routingpolicy/assets/metadata.json',
                         'Windows/native/core/routingpolicy/assets/licenses/LICENSE'):
            path = root / relative
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(b'fixture')
        events = []
        embedded = None
        outer_signed = False

        def run(args, **kwargs):
            nonlocal embedded, outer_signed
            args = [str(arg) for arg in args]
            events.append(args)
            if args[:2] == ['go', 'build']:
                output = Path(args[args.index('-o') + 1])
                output.write_bytes(b'go binary ' + args[-1].encode())
                if output.name == 'BlueGrass.Native':
                    embedded = next(arg.split('ExpectedWorkerSHA256=', 1)[1] for arg in args if 'ExpectedWorkerSHA256=' in arg)
            elif args[0] == 'codesign' and '--sign' in args:
                path = Path(args[-1])
                if path.is_file():
                    path.write_bytes(path.read_bytes() + b' final signature')
                if path == app:
                    outer_signed = True
                    if corrupt_worker:
                        worker = app / 'Contents/MacOS/BlueGrass.Tunnel'
                        worker.write_bytes(worker.read_bytes() + b' accidentally resigned')
            elif args[:3] == ['go', 'version', '-m']:
                return 'github.com/xtls/xray-core ' + build.XRAY_VERSION
            elif '--show-bin-path' in args:
                return str(swift_bin)
            elif args[0] == 'ditto':
                Path(args[-1]).write_bytes(b'archive fixture')
            elif args[:2] == ['flutter', '--version']:
                return '{}'
            elif args[:2] == ['git', 'rev-parse']:
                return 'fixture-commit'
            return 'fixture version' if kwargs.get('capture') else None

        def worker_info(args, **kwargs):
            self.assertTrue(outer_signed, 'worker-info ran before final app signing')
            events.append(['worker-info'])
            return subprocess.CompletedProcess(args, 0, json.dumps({'expectedWorkerSHA256': embedded, 'protocolVersion': 1}) + '\n', '')

        args = argparse.Namespace(arch='arm64', version='0.1.0-dev', build_number=42)
        with ExitStack() as stack:
            for name, value in [('ROOT', root), ('APP_ROOT', app_root), ('CORE', core)]:
                stack.enter_context(patch.object(build, name, value))
            stack.enter_context(patch.object(build.sys, 'platform', 'darwin'))
            stack.enter_context(patch.object(build.platform, 'machine', return_value='arm64'))
            stack.enter_context(patch.object(build, 'run', side_effect=run))
            stack.enter_context(patch.object(build.subprocess, 'run', side_effect=worker_info))
            build.build(args)
        metadata = json.loads((root / 'Mac/dist/arm64/BUILD.json').read_text(encoding='utf-8'))
        return app, metadata, events

    def test_full_build_seals_worker_and_advertises_development_authorization(self):
        with tempfile.TemporaryDirectory() as directory:
            app, metadata, events = self.exercise_build(directory)
            worker = app / 'Contents/MacOS/BlueGrass.Tunnel'
            self.assertTrue(metadata['tun_available'])
            self.assertEqual(metadata['tun_authorization'], 'administrator-password development')
            self.assertEqual(metadata['tun_worker_sha256'], build.sha256(worker))
            signatures = [args for args in events if args[0] == 'codesign' and '--sign' in args and args[-1] == str(worker)]
            self.assertEqual(len(signatures), 1, 'worker was re-signed after hashing')
            self.assertEqual(sum(args == ['worker-info'] for args in events), 2)

    def test_full_build_rejects_worker_modified_by_outer_app_signing(self):
        with tempfile.TemporaryDirectory() as directory:
            with self.assertRaisesRegex(ValueError, 'worker changed'):
                self.exercise_build(directory, corrupt_worker=True)
            self.assertFalse((Path(directory) / 'Mac/dist/arm64/BUILD.json').exists())

    def test_payload_requires_privileged_worker(self):
        with tempfile.TemporaryDirectory() as directory:
            app = Path(directory) / 'BlueGrass.app'
            for part in build.REQUIRED_PAYLOAD:
                target = app / part
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_bytes(b'payload')
            worker = app / 'Contents/MacOS/BlueGrass.Tunnel'
            worker.unlink(missing_ok=True)
            with self.assertRaisesRegex(ValueError, 'BlueGrass.Tunnel'):
                build.validate_payload(app)

    def test_native_helpers_embed_hash_of_final_signed_worker(self):
        with tempfile.TemporaryDirectory() as directory:
            executables = Path(directory)
            events = []

            def run(args, **kwargs):
                args = [str(arg) for arg in args]
                events.append(args)
                if args[:2] == ['go', 'build']:
                    output = Path(args[args.index('-o') + 1])
                    output.write_bytes(b'built ' + args[-1].encode())
                elif args[0] == 'codesign':
                    path = Path(args[-1])
                    path.write_bytes(path.read_bytes() + b' signed')

            with patch.object(build, 'run', side_effect=run):
                digest = build.build_native_helpers(executables, {'GOARCH': 'arm64'})
            worker = executables / 'BlueGrass.Tunnel'
            self.assertEqual(worker.read_bytes(), b'built ./cmd/bluegrass-tunnel signed')
            self.assertEqual(digest, build.sha256(worker))
            self.assertEqual(events[0][-1], './cmd/bluegrass-tunnel')
            self.assertEqual(events[1][0], 'codesign')
            self.assertEqual(events[1][-1], str(worker))
            native = next(args for args in events if args[-1] == './cmd/bluegrass-native')
            self.assertIn('-ldflags=-s -w -X bluegrass/native/core/mactunipc.ExpectedWorkerSHA256=' + digest, native)
            self.assertEqual(sum(args[0] == 'codesign' for args in events), 1)

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
