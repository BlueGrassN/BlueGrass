import base64
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


def android(info_changes=None, payload=b'apk', number=2, version='0.1.0'):
    build = 100000 + number
    name = f'BlueGrass-Android-{version}-arm64.apk'
    digest = hashlib.sha256(payload).hexdigest()
    info = dict(version=version, buildNumber=build, sourceCommit=SHA,
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
        self.assertIn('BlueGrass-Android-0.1.0-arm64.apk', files)
        self.assertEqual(entry['version'], '0.1.0')
        self.assertEqual(entry['buildNumber'], 100002)
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

    def test_public_versions_reject_suffixes_and_leading_zeroes(self):
        for version in ('0.1.0-beta.1', '0.1.0+2', '01.1.0', '0.1', 'v0.1.0', '1١.0.0'):
            with self.subTest(configuration=version), self.assertRaises(ValueError):
                p.validate_version(version)
            with self.subTest(version=version), self.assertRaises(ValueError):
                p.prepare('android', run(), {'android': android(version=version)})

    def test_mac_requires_both_architectures_from_same_run(self):
        def mac(arch, commit=SHA):
            name = f'BlueGrass-macOS-{arch}-0.1.0.zip'
            info = dict(version='0.1.0', build=2, architecture=arch, commit=commit,
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
            info = dict(version='0.1.0', build=27, sourceCommit=SHA,
                        sourceDirty=False, archive='BlueGrass-Windows-x64.zip',
                        sha256=hashlib.sha256(b'win').hexdigest(), bytes=3, signed=False,
                        githubRunId='202', githubRunAttempt='1')
            info.update(changes or {})
            meta = json.dumps(info).encode()
            return archive({'BlueGrass-Windows-x64.zip': b'win', 'build-info.json': meta,
                            'SHA256SUMS.txt': f"{info['sha256']}  BlueGrass-Windows-x64.zip\n"
                            f"{hashlib.sha256(meta).hexdigest()}  build-info.json\n"})
        entry, _ = p.prepare('windows', run(), {'windows': windows()})
        self.assertEqual(entry['version'], '0.1.0')
        _, retained = p.prepare('windows', run(), {'windows': windows({'sourceDirty': True})})
        metadata = next(data for name, data in retained.items() if name.endswith('build-info.json'))
        self.assertTrue(json.loads(metadata)['sourceDirty'])
        for change in ({'bytes': 8}, {'githubRunId': '999'}):
            with self.assertRaises(ValueError):
                p.prepare('windows', run(), {'windows': windows(change)})


class FakeGitHub:
    def __init__(self, state, files, fail=False):
        self.body = p.render_notes(state, REPO)
        self.assets = {name: {'id': i, 'name': name, 'size': len(data), 'state': 'uploaded',
                             'digest': 'sha256:' + hashlib.sha256(data).hexdigest()}
                       for i, (name, data) in enumerate(files.items(), 1)}
        self.fail = fail
        self.events = []
        self.draft = False

    def release(self):
        return {'id': 42, 'body': self.body, 'assets': list(self.assets.values()), 'draft': self.draft}

    def upload(self, path):
        if self.fail:
            raise RuntimeError('upload interrupted')
        data = path.read_bytes()
        self.assets[path.name] = dict(id=100 + len(self.assets), name=path.name, state='uploaded',
                                      size=len(data), digest='sha256:' + hashlib.sha256(data).hexdigest())
        self.events.append('upload')

    def edit(self, body):
        self.body = body
        self.draft = False
        self.events.append('edit')

    def edit_pending(self, body):
        self.body = body
        self.events.append('journal')

    def delete_asset(self, asset):
        self.assets.pop(asset['name'])
        self.events.append('delete')


class PublicationTests(unittest.TestCase):
    def test_journal_patch_only_changes_body_and_preserves_draft_status(self):
        with tempfile.TemporaryDirectory() as tmp:
            client = p.GitHub(REPO, Path(tmp))
            client.release_id = 42
            with patch.object(client, 'api') as request:
                client.edit_pending('journal')
            payload = json.loads((Path(tmp) / 'pending-release.json').read_text(encoding='utf-8'))
            self.assertEqual(payload, {'body': 'journal'})
            self.assertEqual(request.call_args.args[:3], ('releases/42', '--method', 'PATCH'))

    def test_existing_draft_uses_cli_pending_tag_lookup_and_numeric_identity(self):
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
            lookup = json.dumps({'apiUrl': f'https://api.github.com/repos/{REPO}/releases/42'}).encode()
            with patch.object(client, 'api', side_effect=api), patch.object(client, 'command', return_value=lookup):
                self.assertEqual(client.release(), draft)

    def test_created_release_id_is_used_without_waiting_for_tag_index(self):
        draft = {'id': 42, 'tag_name': 'preview', 'draft': True, 'assets': [],
                 'body': p.render_notes({}, REPO)}
        def api(path, *args):
            if path in ('releases', 'releases/42'):
                return draft
            if path == 'releases/tags/preview':
                raise RuntimeError('HTTP 404: tag index not ready')
            if path == 'releases?per_page=100':
                return [[]]
            raise AssertionError(path)
        with tempfile.TemporaryDirectory() as tmp:
            client = p.GitHub(REPO, Path(tmp))
            with patch.object(client, 'api', side_effect=api), patch.object(client, 'command', return_value=b''):
                client.create(SHA)
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
        new, files = p.prepare('android', run(3), {'android': android(number=3, version='0.1.1')})
        api = FakeGitHub({'android': old}, old_files, fail=True)
        original = api.body
        with tempfile.TemporaryDirectory() as tmp, self.assertRaises(RuntimeError):
            p.publish(api, {'android': new}, files, REPO, Path(tmp))
        self.assertEqual(api.body.split(p.MARKER)[0], original.split(p.MARKER)[0])
        self.assertEqual(p.parse_state(api.body), {'android': old})
        self.assertEqual(set(api.assets), set(old_files))

    def test_merge_keeps_other_platform_and_retry_is_idempotent(self):
        old, old_files = p.prepare('android', run(), {'android': android()})
        new, files = p.prepare('android', run(3), {'android': android(number=3, version='0.1.1')})
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
        self.assertEqual(api.events, [])

    def test_cleanup_interruption_can_be_retried_without_losing_old_asset_ownership(self):
        old, old_files = p.prepare('android', run(), {'android': android()})
        new, files = p.prepare('android', run(3), {'android': android(number=3, version='0.1.1')})
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


def platform_archives(platform, number=2, version='0.1.0'):
    if platform == 'android':
        return {'android': android(number=number, version=version)}
    if platform == 'windows':
        name, payload = 'BlueGrass-Windows-x64.zip', b'windows'
        digest = p.sha256(payload)
        info = dict(version=version, build=22 + number, sourceCommit=SHA,
                    sourceDirty=True, archive=name, sha256=digest, bytes=len(payload),
                    signed=False, githubRunId=str(200 + number), githubRunAttempt='1')
        metadata = json.dumps(info).encode()
        return {'windows': archive({name: payload, 'build-info.json': metadata,
                                    'SHA256SUMS.txt': f'{digest}  {name}\n{p.sha256(metadata)}  build-info.json\n'})}
    result = {}
    for arch in ('arm64', 'x86_64'):
        name, payload = f'BlueGrass-macOS-{arch}-{version}.zip', arch.encode()
        info = dict(version=version, build=number, architecture=arch, commit=SHA,
                    archive=name, sha256=p.sha256(payload),
                    signing='ad-hoc development; not notarized')
        result[arch] = archive({name: payload, 'BUILD.json': json.dumps(info),
                               'SHA256SUMS.txt': f'{p.sha256(payload)}  {name}\n', 'TESTING.md': 'test'})
    return result


def published_bundle(version='0.0.9', number=1):
    state, files = {}, {}
    for platform in p.WORKFLOWS:
        state[platform], payload = p.prepare(platform, run(number), platform_archives(platform, number, version))
        files.update(payload)
    return state, files


class CandidateGitHub(FakeGitHub):
    def __init__(self, state, files, version='0.1.0', number=2):
        super().__init__(state, files)
        self.version = version
        self.number = number
        self.runs = {platform: [run(number)] for platform in p.WORKFLOWS}
        self.payloads = {platform: platform_archives(platform, number, version) for platform in p.WORKFLOWS}
        self.downloaded = []
        self.relation = 'identical'

    def pages(self, path, key):
        platform = next(k for k, workflow in p.WORKFLOWS.items() if workflow in path)
        return self.runs[platform]

    def api(self, path, *args):
        if path == f'compare/{SHA}...main':
            return {'status': self.relation}
        if path == f'contents/release.json?ref={SHA}':
            if self.version is None:
                raise RuntimeError('HTTP 404: Not Found')
            return {'content': base64.b64encode(json.dumps({'version': self.version}).encode()).decode()}
        if path == f'contents/Windows/release.json?ref={SHA}':
            return {'content': base64.b64encode(b'{"buildBase":22}').decode()}
        raise AssertionError(path)

    def archives(self, platform, selected):
        if selected['id'] != 200 + self.number:
            raise AssertionError('Downloaded an ineligible run')
        self.downloaded.append(platform)
        return self.payloads[platform]


class UnifiedVersionTests(unittest.TestCase):
    def reconcile(self, api, version='0.1.0'):
        with tempfile.TemporaryDirectory() as tmp:
            return p.reconcile(api, REPO, Path(tmp), dry_run=False, version=version)

    def test_waits_for_all_platforms_without_changing_existing_downloads(self):
        old, files = published_bundle()
        api = CandidateGitHub(old, files)
        api.runs['macos'] = [run(2, status='in_progress', conclusion=None)]
        original = api.body
        self.assertEqual(self.reconcile(api), old)
        self.assertEqual(api.body, original)
        self.assertEqual(set(api.assets), set(files))
        self.assertEqual(api.events, [])

    def test_completed_run_is_published_when_server_success_index_lags(self):
        old, files = published_bundle()

        class LaggingIndexGitHub(CandidateGitHub):
            def pages(self, path, key):
                if 'status=success' in path:
                    return [run(1)]
                return [run(9, status='in_progress'), run(8, conclusion='failure'),
                        run(7, event='pull_request'), run(6, head_branch='feature'),
                        run(5, head_repository={'full_name': 'elsewhere/fork'}), run(2), run(1)]

        api = LaggingIndexGitHub(old, files)
        result = self.reconcile(api)
        self.assertEqual(set(result), set(p.WORKFLOWS))
        self.assertEqual({e['version'] for e in result.values()}, {'0.1.0'})
        self.assertEqual({e['runId'] for e in result.values()}, {202})
        self.assertEqual(len(api.assets), 12)
        self.assertTrue(set(api.assets).isdisjoint(files))
        self.assertLess(api.events.index('edit'), api.events.index('delete'))

    def test_migrates_legacy_mixed_versions_only_as_a_complete_bundle(self):
        old, files = published_bundle()
        for platform, version in {'windows': '0.1.0-beta.1+23', 'android': '0.5.0+100001', 'macos': '0.1.0-dev+1'}.items():
            old[platform]['version'] = version
        api = CandidateGitHub(old, files)
        result = self.reconcile(api)
        self.assertEqual({e['version'] for e in result.values()}, {'0.1.0'})
        self.assertEqual(set(api.assets), {name for e in result.values() for name in e['assets']})

    def test_same_version_rebuild_is_immutable_and_does_not_download(self):
        current, files = published_bundle('0.1.0')
        api = CandidateGitHub(current, files)
        self.assertEqual(self.reconcile(api), current)
        self.assertEqual(api.events, [])
        self.assertEqual(api.downloaded, [])

    def test_rejects_semantic_version_rollback_before_any_upload(self):
        current, files = published_bundle('0.2.0')
        api = CandidateGitHub(current, files)
        with self.assertRaisesRegex(ValueError, 'rollback'):
            self.reconcile(api)
        self.assertEqual(api.events, [])

    def test_old_workflow_run_cannot_publish_a_new_version(self):
        current, files = published_bundle(number=4)
        api = CandidateGitHub(current, files)
        self.assertEqual(self.reconcile(api), current)
        self.assertEqual(api.downloaded, [])
        self.assertEqual(api.events, [])

    def test_missing_legacy_source_config_waits_without_relabeling_packages(self):
        old, files = published_bundle()
        api = CandidateGitHub(old, files)
        api.version = None
        self.assertEqual(self.reconcile(api), old)
        self.assertEqual(api.downloaded, [])

    def test_mismatched_package_version_or_source_rejects_the_whole_bundle(self):
        old, files = published_bundle()
        for changes in ({'version': '0.1.1'}, {'sourceCommit': 'b' * 40}, {'sha256': '0' * 64}):
            with self.subTest(changes=changes):
                api = CandidateGitHub(old, files)
                payload = android(version=changes['version']) if 'version' in changes else android(changes)
                api.payloads['android'] = {'android': payload}
                with self.assertRaises(ValueError):
                    self.reconcile(api)
                self.assertEqual(api.events, [])
                self.assertEqual(p.parse_state(api.body), old)

    def test_partial_upload_failure_keeps_entire_old_page_and_retries(self):
        old, files = published_bundle()
        api = CandidateGitHub(old, files)
        original_body = api.body
        upload = api.upload

        def interrupted(path):
            if api.events.count('upload') == 3:
                raise RuntimeError('upload interrupted between platforms')
            upload(path)

        api.upload = interrupted
        with self.assertRaises(RuntimeError):
            self.reconcile(api)
        self.assertEqual(api.body.split(p.MARKER)[0], original_body.split(p.MARKER)[0])
        self.assertEqual(p.parse_state(api.body), old)
        self.assertTrue(set(files) <= set(api.assets))
        self.assertNotIn('edit', api.events)
        api.upload = upload
        result = self.reconcile(api)
        self.assertEqual({e['version'] for e in result.values()}, {'0.1.0'})
        self.assertEqual(len(api.assets), 12)

    def test_other_source_version_waits_before_downloading(self):
        old, files = published_bundle()
        api = CandidateGitHub(old, files)
        api.version = '0.1.1'
        self.assertEqual(self.reconcile(api), old)
        self.assertEqual(api.downloaded, [])
        self.assertEqual(api.events, [])

    def test_interrupted_upload_recovers_with_newer_build_or_new_version(self):
        for next_version in ('0.1.0', '0.1.1'):
            with self.subTest(next_version=next_version):
                old, files = published_bundle()
                api = CandidateGitHub(old, files)
                upload = api.upload

                def interrupted(path):
                    if api.events.count('upload') == 3:
                        raise RuntimeError('upload interrupted')
                    upload(path)

                api.upload = interrupted
                with self.assertRaises(RuntimeError):
                    self.reconcile(api)
                api.upload = upload
                api.version, api.number = next_version, 3
                api.runs = {platform: [run(3)] for platform in p.WORKFLOWS}
                api.payloads = {platform: platform_archives(platform, 3, next_version) for platform in p.WORKFLOWS}
                result = self.reconcile(api, version=next_version)
                self.assertEqual({e['version'] for e in result.values()}, {next_version})
                self.assertEqual({e['runId'] for e in result.values()}, {203})
                self.assertEqual(len(api.assets), 12)
                self.assertEqual(set(api.assets), {n for e in result.values() for n in e['assets']})

    def test_recovery_refuses_modified_pending_assets_and_keeps_unmanaged_files(self):
        old, files = published_bundle()
        pending, pending_files = published_bundle('0.1.0', 2)
        api = CandidateGitHub(old, {**files, **pending_files, 'User-backup.zip': b'private'})
        api.body = p.render_notes(old, REPO, pending_release=pending)
        altered = next(iter(pending_files))
        api.assets[altered]['digest'] = 'sha256:' + '0' * 64
        with self.assertRaisesRegex(ValueError, 'Pending asset differs'):
            self.reconcile(api)
        self.assertEqual(api.events, [])
        api.assets[altered]['digest'] = 'sha256:' + p.sha256(pending_files[altered])
        self.reconcile(api)
        self.assertIn('User-backup.zip', api.assets)
        self.assertEqual(len(api.assets), 13)

    def test_unowned_name_conflict_does_not_poison_later_version_publication(self):
        old, files = published_bundle()
        conflict = 'BlueGrass-Windows-0.1.0-build-info.json'
        api = CandidateGitHub(old, {**files, conflict: b'not our metadata'})
        original = api.body
        with self.assertRaisesRegex(ValueError, 'Existing asset differs'):
            self.reconcile(api)
        self.assertEqual(api.events, [])
        self.assertEqual(api.body, original)
        api.version, api.number = '0.1.1', 3
        api.runs = {platform: [run(3)] for platform in p.WORKFLOWS}
        api.payloads = {platform: platform_archives(platform, 3, '0.1.1') for platform in p.WORKFLOWS}
        result = self.reconcile(api, version='0.1.1')
        self.assertEqual({e['version'] for e in result.values()}, {'0.1.1'})
        self.assertIn(conflict, api.assets)
        self.assertEqual(len(api.assets), 13)

    def test_dry_run_does_not_mutate_pending_uploads_or_the_page(self):
        old, files = published_bundle()
        pending, pending_files = published_bundle('0.1.0', 2)
        api = CandidateGitHub(old, {**files, **pending_files})
        api.body = p.render_notes(old, REPO, pending_release=pending)
        before_body, before_assets = api.body, copy.deepcopy(api.assets)
        with tempfile.TemporaryDirectory() as tmp:
            p.reconcile(api, REPO, Path(tmp), dry_run=True, version='0.1.0')
        self.assertEqual(api.body, before_body)
        self.assertEqual(api.assets, before_assets)
        self.assertEqual(api.events, [])

    def test_waiting_initial_release_remains_a_draft(self):
        api = CandidateGitHub({}, {})
        api.draft = True
        api.runs['macos'] = []
        self.assertEqual(self.reconcile(api), {})
        self.assertTrue(api.draft)
        self.assertEqual(api.events, [])

    def test_detached_source_is_rejected_before_any_upload(self):
        old, files = published_bundle()
        api = CandidateGitHub(old, files)
        api.relation = 'diverged'
        with self.assertRaises(ValueError):
            self.reconcile(api)
        self.assertEqual(api.events, [])

    def test_same_version_retries_pending_cleanup(self):
        current, files = published_bundle('0.1.0')
        retired = 'BlueGrass-Android-legacy.apk'
        current['android']['retiredAssets'] = [retired]
        api = CandidateGitHub(current, {**files, retired: b'old'})
        result = self.reconcile(api)
        self.assertNotIn(retired, api.assets)
        self.assertNotIn('retiredAssets', p.parse_state(api.body)['android'])
        self.assertEqual(api.downloaded, [])


if __name__ == '__main__':
    unittest.main()
