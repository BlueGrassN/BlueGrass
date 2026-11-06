import copy
import hashlib
import io
import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch
import zipfile

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import publish_preview as p

REPO = 'BlueGrassN/BlueGrass'
SHA = 'a' * 40


def run(number=2, attempt=1, **changes):
    result = dict(id=200 + number, run_number=number, run_attempt=attempt,
                  head_sha=SHA, head_branch='main', event='push',
                  status='completed', conclusion='success',
                  head_repository={'full_name': REPO})
    result.update(changes)
    return result


def archive(files):
    stream = io.BytesIO()
    with zipfile.ZipFile(stream, 'w') as z:
        for name, value in files.items():
            z.writestr(name, value)
    return stream.getvalue()


def android(info_changes=None, payload=b'apk'):
    name = 'BlueGrass-Android-0.5.0+100002-arm64.apk'
    digest = hashlib.sha256(payload).hexdigest()
    info = dict(version='0.5.0', buildNumber=100002, sourceCommit=SHA,
                sourceDirty=False, signingMode='configured', apk=name, sha256=digest)
    info.update(info_changes or {})
    return archive({name: payload, 'build-info.json': json.dumps(info),
                    'SHA256SUMS': f'{digest}  {name}\n'})


class SelectionTests(unittest.TestCase):
    def test_latest_success_ignores_pr_fork_failed_and_non_main(self):
        runs = [run(2), run(3, event='workflow_dispatch'), run(9, event='pull_request'),
                run(10, head_repository={'full_name': 'elsewhere/fork'}),
                run(11, conclusion='failure'), run(12, head_branch='feature'),
                run(13, status='in_progress')]
        self.assertEqual(p.pick_run(runs, REPO)['run_number'], 3)

    def test_old_run_finishing_late_does_not_replace_newer_run(self):
        self.assertEqual(p.pick_run([run(3), run(2, attempt=9)], REPO)['run_number'], 3)
        self.assertFalse(p.newer(run(2, attempt=9), {'runNumber': 3, 'runAttempt': 1}))
        self.assertTrue(p.newer(run(3, attempt=2), {'runNumber': 3, 'runAttempt': 1}))


class ArtifactTests(unittest.TestCase):
    def test_android_verified_package_gets_unique_names_and_valid_checksums(self):
        entry, files = p.prepare('android', run(), {'android': android()})
        self.assertIn('BlueGrass-Android-0.5.0+100002-r1-arm64.apk', files)
        self.assertEqual(entry['version'], '0.5.0+100002')
        checksum = files[entry['checksums']].decode()
        for line in checksum.splitlines():
            digest, name = line.split('  ')
            self.assertEqual(hashlib.sha256(files[name]).hexdigest(), digest)

    def test_untrusted_metadata_and_changed_package_are_rejected(self):
        for change in ({'sourceCommit': 'b' * 40}, {'sourceDirty': True},
                       {'signingMode': 'debug'}, {'buildNumber': 1},
                       {'apk': '../outside.apk'}, {'sha256': '0' * 64}):
            with self.subTest(change=change), self.assertRaises(ValueError):
                p.prepare('android', run(), {'android': android(change)})

    def test_mac_requires_both_architectures_from_same_run(self):
        def mac(arch, commit=SHA):
            name = f'BlueGrass-macOS-{arch}-0.1.0-dev+2.zip'
            info = dict(version='0.1.0-dev', build=2, architecture=arch, commit=commit,
                        archive=name, sha256=hashlib.sha256(b'mac').hexdigest(),
                        signing='ad-hoc development; not notarized')
            return archive({name: b'mac', 'BUILD.json': json.dumps(info),
                            'SHA256SUMS.txt': f"{info['sha256']}  {name}\n", 'TESTING.md': 'test'})
        pair = {'arm64': mac('arm64'), 'x86_64': mac('x86_64')}
        entry, files = p.prepare('macos', run(), pair)
        self.assertEqual(len(entry['downloads']), 2)
        self.assertEqual(len(files), 6)
        for bad in ({'arm64': mac('arm64')},
                    {'arm64': mac('arm64'), 'x86_64': mac('x86_64', 'b' * 40)}):
            with self.assertRaises(ValueError):
                p.prepare('macos', run(), bad)

    def test_windows_preserves_reported_source_state_and_checks_package_identity(self):
        def windows(changes=None):
            info = dict(version='0.1.0-beta.1', build=27, sourceCommit=SHA,
                        sourceDirty=False, archive='BlueGrass-Windows-x64.zip',
                        sha256=hashlib.sha256(b'win').hexdigest(), bytes=3, signed=False,
                        githubRunId='202', githubRunAttempt='1')
            info.update(changes or {})
            meta = json.dumps(info).encode()
            return archive({'BlueGrass-Windows-x64.zip': b'win', 'build-info.json': meta,
                            'SHA256SUMS.txt': f"{info['sha256']}  BlueGrass-Windows-x64.zip\n"
                            f"{hashlib.sha256(meta).hexdigest()}  build-info.json\n"})
        entry, _ = p.prepare('windows', run(), {'windows': windows()})
        self.assertEqual(entry['version'], '0.1.0-beta.1+27')
        _, retained = p.prepare('windows', run(), {'windows': windows({'sourceDirty': True})})
        metadata = next(data for name, data in retained.items() if name.endswith('build-info.json'))
        self.assertTrue(json.loads(metadata)['sourceDirty'])
        for change in ({'bytes': 8}, {'githubRunId': '999'}):
            with self.assertRaises(ValueError):
                p.prepare('windows', run(), {'windows': windows(change)})


class FakeGitHub:
    def __init__(self, state, files, fail=False):
        self.body = p.render_notes(state, REPO)
        self.assets = {name: {'id': i, 'name': name, 'size': len(data),
                             'digest': 'sha256:' + hashlib.sha256(data).hexdigest()}
                       for i, (name, data) in enumerate(files.items(), 1)}
        self.fail = fail
        self.events = []

    def release(self):
        return {'id': 42, 'body': self.body, 'assets': list(self.assets.values()), 'draft': False}

    def upload(self, path):
        if self.fail:
            raise RuntimeError('upload interrupted')
        data = path.read_bytes()
        self.assets[path.name] = dict(id=100 + len(self.assets), name=path.name,
                                      size=len(data), digest='sha256:' + hashlib.sha256(data).hexdigest())
        self.events.append('upload')

    def edit(self, body):
        self.body = body
        self.events.append('edit')

    def delete_asset(self, asset):
        self.assets.pop(asset['name'])
        self.events.append('delete')


class PublicationTests(unittest.TestCase):
    def test_draft_release_is_found_by_listing_when_tag_endpoint_returns_404(self):
        draft = {'id': 42, 'tag_name': 'preview', 'draft': True, 'assets': [],
                 'body': p.render_notes({}, REPO)}
        def api(path, *args):
            if path == 'releases/tags/preview':
                raise RuntimeError('HTTP 404: Not Found')
            if path == 'releases?per_page=100':
                return [[draft]]
            if path == 'releases/42':
                return draft
            raise AssertionError(path)
        with tempfile.TemporaryDirectory() as tmp:
            client = p.GitHub(REPO, Path(tmp))
            with patch.object(client, 'api', side_effect=api):
                self.assertEqual(client.release(), draft)

    def test_uncommitted_empty_starter_upload_is_recovered(self):
        entry, files = p.prepare('android', run(), {'android': android()})
        name = next(iter(files))
        api = FakeGitHub({}, {name: b''})
        api.assets[name]['state'] = 'starter'
        with tempfile.TemporaryDirectory() as tmp:
            p.publish(api, {'android': entry}, files, REPO, Path(tmp))
        self.assertEqual(p.parse_state(api.body), {'android': entry})
        self.assertEqual(api.assets[name]['digest'], 'sha256:' + hashlib.sha256(files[name]).hexdigest())

    def test_cleanup_never_deletes_assets_in_the_current_download_table(self):
        entry, files = p.prepare('android', run(), {'android': android()})
        api = FakeGitHub({'android': entry}, files)
        inconsistent = copy.deepcopy(entry)
        inconsistent['retiredAssets'] = [next(iter(files))]
        with self.assertRaises(ValueError):
            p.parse_state(p.render_notes({'android': inconsistent}, REPO))
        with tempfile.TemporaryDirectory() as tmp:
            p.publish(api, {'android': inconsistent}, {}, REPO, Path(tmp))
        self.assertEqual(set(api.assets), set(files))

    def test_failed_upload_preserves_old_download_and_page(self):
        old, old_files = p.prepare('android', run(), {'android': android()})
        new, files = p.prepare('android', run(attempt=2), {'android': android()})
        api = FakeGitHub({'android': old}, old_files, fail=True)
        original = api.body
        with tempfile.TemporaryDirectory() as tmp, self.assertRaises(RuntimeError):
            p.publish(api, {'android': new}, files, REPO, Path(tmp))
        self.assertEqual(api.body, original)
        self.assertEqual(set(api.assets), set(old_files))

    def test_merge_keeps_other_platform_and_retry_is_idempotent(self):
        old, old_files = p.prepare('android', run(), {'android': android()})
        new, files = p.prepare('android', run(attempt=2), {'android': android()})
        other = copy.deepcopy(old)
        other['assets'] = {}
        other['downloads'] = {}
        state = {'android': new, 'windows': other}
        api = FakeGitHub({'android': old, 'windows': other}, old_files)
        with tempfile.TemporaryDirectory() as tmp:
            p.publish(api, state, files, REPO, Path(tmp))
            self.assertLess(api.events.index('edit'), api.events.index('delete'))
            self.assertEqual(set(api.assets), set(files))
            self.assertEqual(p.parse_state(api.body), state)
            api.events.clear()
            p.publish(api, state, files, REPO, Path(tmp))
            self.assertNotIn('upload', api.events)
            self.assertNotIn('delete', api.events)

    def test_existing_asset_with_wrong_bytes_does_not_get_silently_overwritten(self):
        entry, files = p.prepare('android', run(), {'android': android()})
        api = FakeGitHub({}, {next(iter(files)): b'wrong'})
        with tempfile.TemporaryDirectory() as tmp, self.assertRaises(ValueError):
            p.publish(api, {'android': entry}, files, REPO, Path(tmp))
        self.assertNotIn('edit', api.events)

    def test_cleanup_interruption_can_be_retried_without_losing_old_asset_ownership(self):
        old, old_files = p.prepare('android', run(), {'android': android()})
        new, files = p.prepare('android', run(attempt=2), {'android': android()})
        api = FakeGitHub({'android': old}, old_files)
        original_delete = api.delete_asset
        def fail_delete(asset):
            raise RuntimeError('delete interrupted')
        api.delete_asset = fail_delete
        with tempfile.TemporaryDirectory() as tmp:
            with self.assertRaises(RuntimeError):
                p.publish(api, {'android': new}, files, REPO, Path(tmp))
            api.delete_asset = original_delete
            p.publish(api, p.parse_state(api.body), {}, REPO, Path(tmp))
        self.assertEqual(set(api.assets), set(files))

    def test_missing_or_corrupted_state_is_not_treated_as_empty_release(self):
        for body in ('hand-maintained release', '<!-- bluegrass-preview-state\n{}\n-->'):
            with self.assertRaises(ValueError):
                p.parse_state(body)


if __name__ == '__main__':
    unittest.main()
