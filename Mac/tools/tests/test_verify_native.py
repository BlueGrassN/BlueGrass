import importlib.util
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch


spec = importlib.util.spec_from_file_location('verify_native', Path(__file__).parents[1] / 'verify_native.py')
verify = importlib.util.module_from_spec(spec)
spec.loader.exec_module(verify)


class VerifyNativeTests(unittest.TestCase):
    def fixture(self, directory):
        app = Path(directory) / 'BlueGrass.app'
        executables = app / 'Contents/MacOS'
        executables.mkdir(parents=True)
        worker = executables / 'BlueGrass.Tunnel'
        worker.write_bytes(b'final signed worker')
        native = executables / 'BlueGrass.Native'
        native.write_bytes(b'broker')
        return app, worker, native

    def info(self, digest, **overrides):
        value = {'expectedWorkerSHA256': digest, 'protocolVersion': 1}
        value.update(overrides)
        return json.dumps(value) + '\n'

    def test_checks_final_worker_bytes_and_runs_only_print_info(self):
        with tempfile.TemporaryDirectory() as directory:
            app, worker, native = self.fixture(directory)
            digest = verify.sha256(worker)
            result = subprocess.CompletedProcess([], 0, self.info(digest), '')
            with patch.object(verify.subprocess, 'run', return_value=result) as run:
                self.assertEqual(verify.verify_native(app, expected_worker_sha256=digest), digest)
            self.assertEqual(run.call_args.args[0], [str(native), '--tun-worker-info'])
            self.assertEqual(run.call_args.kwargs['timeout'], 10)

    def test_rejects_modified_worker_after_hashing(self):
        with tempfile.TemporaryDirectory() as directory:
            app, worker, native = self.fixture(directory)
            original = verify.sha256(worker)
            worker.write_bytes(worker.read_bytes() + b'resigned')
            result = subprocess.CompletedProcess([], 0, self.info(original), '')
            with patch.object(verify.subprocess, 'run', return_value=result):
                with self.assertRaisesRegex(ValueError, 'worker'):
                    verify.verify_native(app, expected_worker_sha256=original)
                with self.assertRaisesRegex(ValueError, 'worker'):
                    verify.verify_native(app)

    def test_rejects_uninjected_malformed_or_incompatible_broker_info(self):
        with tempfile.TemporaryDirectory() as directory:
            app, worker, native = self.fixture(directory)
            digest = verify.sha256(worker)
            for output in [self.info(''), self.info(digest.upper()), self.info(digest, protocolVersion=2),
                           self.info(digest, protocolVersion=True), self.info(digest, extra='field'),
                           '{"expectedWorkerSHA256":"' + digest + '","expectedWorkerSHA256":"' + digest + '","protocolVersion":1}\n',
                           '{}\n', self.info(digest) + '{}\n', 'not json\n']:
                with self.subTest(output=output):
                    result = subprocess.CompletedProcess([], 0, output, '')
                    with patch.object(verify.subprocess, 'run', return_value=result):
                        with self.assertRaises(ValueError):
                            verify.verify_native(app)

    def test_requires_worker_before_executing_broker(self):
        with tempfile.TemporaryDirectory() as directory:
            app, worker, native = self.fixture(directory)
            worker.unlink()
            with patch.object(verify.subprocess, 'run') as run:
                with self.assertRaises(ValueError):
                    verify.verify_native(app)
            run.assert_not_called()


if __name__ == '__main__':
    unittest.main()
