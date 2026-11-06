#!/usr/bin/env python3
"""Reconcile trusted main build artifacts into one rolling preview Release.

The workflow serializes all writers. Artifacts are data only; nothing downloaded
is executed. An atomic Release-body update commits the per-platform state after
all new files have been uploaded and verified. Old downloads are removed last.
"""
import argparse
import base64
import copy
import hashlib
import io
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
from urllib.parse import quote
import zipfile

TAG = 'preview'
TITLE = 'BlueGrass 开发预览版'
WORKFLOWS = {'windows': 'windows-release.yml', 'android': 'android-release.yml',
             'macos': 'macos-dev.yml'}
LABELS = {'windows': 'Windows x64', 'android': 'Android ARM64', 'macos': 'macOS'}
MARKER = '<!-- bluegrass-preview-state\n'


def require(condition, message):
    if not condition:
        raise ValueError(message)


def sha256(data):
    return hashlib.sha256(data).hexdigest()


def pick_run(runs, repo):
    valid = [r for r in runs if r['head_branch'] == 'main'
             and r['head_repository']['full_name'] == repo
             and r['event'] in ('push', 'workflow_dispatch')
             and r['status'] == 'completed' and r['conclusion'] == 'success']
    return max(valid, key=lambda r: (r['run_number'], r['run_attempt']), default=None)


def newer(run, published):
    return not published or (run['run_number'], run['run_attempt']) > (
        published['runNumber'], published['runAttempt'])


def read_artifact(data):
    with zipfile.ZipFile(io.BytesIO(data)) as z:
        names = z.namelist()
        require(len(names) == len(set(names)), 'Duplicate artifact filenames')
        require(all(re.fullmatch(r'[A-Za-z0-9_.+\-]+', n) for n in names),
                'Artifact must contain only top-level files')
        require(sum(i.file_size for i in z.infolist()) < 1024 ** 3, 'Artifact too large')
        return {name: z.read(name) for name in names}


def verify_checksums(files, name):
    checks = {}
    for line in files[name].decode('utf-8-sig').splitlines():
        match = re.fullmatch(r'([0-9a-f]{64})  ([A-Za-z0-9_.+\-]+)', line)
        require(match is not None, 'Invalid checksum line')
        digest, filename = match.groups()
        require(filename in files and sha256(files[filename]) == digest,
                f'Checksum mismatch: {filename}')
        checks[filename] = digest
    return checks


def prepare(platform, run, archives):
    """Validate an entire platform and return its entry and namespaced bytes."""
    require(re.fullmatch('[0-9a-f]{40}', run['head_sha']), 'Invalid source commit')
    require(platform in WORKFLOWS, 'Unknown platform')
    variants = ('arm64', 'x86_64') if platform == 'macos' else (platform,)
    require(set(archives) == set(variants), 'Missing platform architecture')
    output, downloads, versions = {}, {}, set()
    label = {'windows': 'Windows', 'android': 'Android', 'macos': 'macOS'}[platform]
    for variant in variants:
        files = read_artifact(archives[variant])
        metadata_name = 'BUILD.json' if platform == 'macos' else 'build-info.json'
        info = json.loads(files[metadata_name].decode('utf-8-sig'))
        version = info['version']
        require(re.fullmatch(r'\d+\.\d+\.\d+(?:-[A-Za-z0-9.-]+)?', version), 'Invalid version')
        build = info['buildNumber'] if platform == 'android' else info['build']
        require(type(build) is int and build > 0, 'Invalid build number')
        source = info['commit'] if platform == 'macos' else info['sourceCommit']
        require(source == run['head_sha'], 'Wrong source commit')
        prefix = f"BlueGrass-{label}-{version}+{build}-r{run['run_attempt']}"
        checks = verify_checksums(files, 'SHA256SUMS' if platform == 'android' else 'SHA256SUMS.txt')
        if platform == 'android':
            require(info['sourceDirty'] is False, 'Refusing modified Android source')
            require(info['signingMode'] == 'configured', 'Refusing test-signed Android artifact')
            require(build == 100000 + run['run_number'], 'Wrong Android build number')
            original = f'BlueGrass-Android-{version}+{build}-arm64.apk'
            require(info['apk'] == original, 'Unexpected APK filename')
            name, architecture = f'{prefix}-arm64.apk', 'ARM64 APK'
        elif platform == 'windows':
            require(info['signed'] is False, 'Windows signing policy changed; review publisher')
            require(str(info['githubRunId']) == str(run['id']), 'Wrong Windows run ID')
            require(0 < int(info['githubRunAttempt']) <= run['run_attempt'], 'Wrong Windows attempt')
            # Existing packaging reports post-build sourceDirty, including build
            # changes. Keep it verbatim; do not invent a clean-source attestation.
            original = 'BlueGrass-Windows-x64.zip'
            require(info['archive'] == original, 'Unexpected Windows archive')
            require(info['bytes'] == len(files[original]), 'Windows size mismatch')
            require(metadata_name in checks, 'Windows metadata checksum missing')
            name, architecture = f'{prefix}-x64.zip', 'x64 ZIP'
        else:
            require(info['architecture'] == variant, 'Wrong macOS architecture')
            require(build == run['run_number'], 'Wrong macOS build number')
            require(info['signing'] == 'ad-hoc development; not notarized', 'macOS signing policy changed')
            original = f'BlueGrass-macOS-{variant}-{version}+{build}.zip'
            require(info['archive'] == original, 'Unexpected macOS archive')
            name, architecture = f'{prefix}-{variant}.zip', f'{variant} ZIP'
        require(original in checks and info['sha256'] == sha256(files[original]), 'Package checksum mismatch')
        output[name] = files[original]
        meta_suffix = f'{variant}-BUILD.json' if platform == 'macos' else 'build-info.json'
        output[f'{prefix}-{meta_suffix}'] = files[metadata_name]
        if platform == 'macos':
            testing_name = f'{prefix}-TESTING.md'
            require(testing_name not in output or output[testing_name] == files['TESTING.md'],
                    'macOS architectures have different instructions')
            output[testing_name] = files['TESTING.md']
        downloads[architecture] = name
        versions.add(f'{version}+{build}')
    require(len(versions) == 1, 'Platform architectures have different versions')
    checksum_name = f'{prefix}-SHA256SUMS.txt'
    output[checksum_name] = ''.join(f'{sha256(data)}  {name}\n' for name, data in sorted(output.items())).encode()
    entry = dict(version=versions.pop(), sourceCommit=run['head_sha'], runId=run['id'],
                 runNumber=run['run_number'], runAttempt=run['run_attempt'],
                 downloads=downloads, checksums=checksum_name,
                 assets={name: {'sha256': sha256(data), 'bytes': len(data)} for name, data in output.items()})
    return entry, output


def parse_state(body):
    require(body.count(MARKER) == 1, 'Release is missing managed state; refusing to replace it')
    raw = body.split(MARKER, 1)[1].split('\n-->', 1)[0]
    state = json.loads(raw)
    require(state.get('schema') == 1 and isinstance(state.get('platforms'), dict), 'Invalid release state')
    require(set(state['platforms']) <= set(WORKFLOWS), 'Unknown platform in release state')
    for entry in state['platforms'].values():
        require(isinstance(entry.get('assets'), dict), 'Invalid platform assets')
        require(type(entry.get('runNumber')) is int and type(entry.get('runAttempt')) is int,
                'Invalid published run identity')
        for name, info in entry['assets'].items():
            require(re.fullmatch(r'BlueGrass-[A-Za-z0-9_.+\-]+', name), 'Invalid managed asset name')
            require(re.fullmatch('[0-9a-f]{64}', info['sha256']) and info['bytes'] > 0,
                    'Invalid managed asset digest')
        require(all(re.fullmatch(r'BlueGrass-[A-Za-z0-9_.+\-]+', name)
                    for name in entry.get('retiredAssets', [])), 'Invalid retired asset name')
    active = {name for entry in state['platforms'].values() for name in entry['assets']}
    retired = {name for entry in state['platforms'].values() for name in entry.get('retiredAssets', [])}
    require(not active.intersection(retired), 'Release state marks active downloads for deletion')
    return state['platforms']


def render_notes(state, repo):
    base = f'https://github.com/{repo}'
    lines = ['各平台最新成功构建集中在本页；版本独立更新。下载对应安装包，不要下载 Source code。', '',
             '| 平台 | 版本 | 安装包 | SHA256 | 源码 / 构建 |',
             '| --- | --- | --- | --- | --- |']
    for platform, label in LABELS.items():
        entry = state.get(platform)
        if not entry:
            lines.append(f'| {label} | 等待成功构建 | — | — | — |')
            continue
        url = lambda name: f'{base}/releases/download/{TAG}/{quote(name)}'
        downloads = ' · '.join(f'[{arch}]({url(name)})' for arch, name in entry['downloads'].items())
        source = f"[源码 {entry['sourceCommit'][:7]}]({base}/commit/{entry['sourceCommit']})"
        build = f"[构建]({base}/actions/runs/{entry['runId']}/attempts/{entry['runAttempt']})"
        lines.append(f"| {label} | {entry['version']} | {downloads} | [校验文件]({url(entry['checksums'])}) | {source} / {build} |")
    lines += ['', 'Windows：完整解压 ZIP 后运行 BlueGrass.exe，保留 runtime 文件夹；当前为未签名便携测试包。',
              'Android：Android 8.0 及以上 ARM64 设备；沿用已配置的固定 Alpha 签名。',
              'macOS：macOS 13 及以上，按芯片选择 arm64 或 x86_64；临时开发签名，未公证。',
              '', '各端自动化构建通过不代表真实账号联网、VPN/TUN 或系统权限恢复已经通过设备验收。',
              '构建信息保留原始 artifact 文件名和 sourceDirty 等事实；下载文件名以本页链接和校验文件为准。',
              'iOS / HarmonyOS 暂无可下载安装包。历史平台 Release 保留供追溯。',
              'preview 为固定下载入口，tag 的 Source code 不代表全部平台的源码，请使用表格中的逐端源码链接。', '',
              MARKER + json.dumps({'schema': 1, 'platforms': state}, ensure_ascii=False, sort_keys=True) + '\n-->']
    return '\n'.join(lines) + '\n'


class GitHub:
    def __init__(self, repo, directory):
        self.repo, self.directory = repo, directory

    def command(self, *args):
        try:
            result = subprocess.run(['gh', *args], stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=300)
        except subprocess.TimeoutExpired as exc:
            raise RuntimeError('GitHub command timed out after 300 seconds') from exc
        if result.returncode:
            raise RuntimeError(result.stderr.decode('utf-8', errors='replace'))
        return result.stdout

    def api(self, path, *args):
        return json.loads(self.command('api', f'repos/{self.repo}/{path}', *args))

    def pages(self, path, key):
        return [item for page in self.api(path, '--paginate', '--slurp') for item in page[key]]

    def release(self):
        try:
            return self.api(f'releases/tags/{TAG}')
        except RuntimeError as exc:
            if 'HTTP 404' in str(exc):
                # A draft may have a pending tag that the tag endpoint cannot
                # resolve yet. Authenticated release listings include drafts.
                releases = [r for page in self.api('releases?per_page=100', '--paginate', '--slurp')
                            for r in page if r['tag_name'] == TAG]
                require(len(releases) <= 1, 'Multiple preview releases; refusing ambiguous draft')
                if releases:
                    return self.api(f"releases/{releases[0]['id']}")
                return None
            raise

    def create(self, target):
        notes = self.directory / 'initial-notes.md'
        notes.write_text(render_notes({}, self.repo), encoding='utf-8')
        self.command('release', 'create', TAG, '--repo', self.repo, '--target', target,
                     '--title', TITLE, '--notes-file', str(notes), '--draft', '--prerelease', '--latest=false')

    def upload(self, path):
        self.command('release', 'upload', TAG, str(path), '--repo', self.repo)

    def edit(self, body):
        notes = self.directory / 'release-notes.md'
        notes.write_text(body, encoding='utf-8')
        self.command('release', 'edit', TAG, '--repo', self.repo, '--title', TITLE,
                     '--notes-file', str(notes), '--draft=false', '--prerelease', '--latest=false')

    def delete_asset(self, asset):
        self.command('api', f"repos/{self.repo}/releases/assets/{asset['id']}", '--method', 'DELETE')

    def archives(self, platform, run):
        artifacts = self.pages(f"actions/runs/{run['id']}/artifacts?per_page=100", 'artifacts')
        artifacts = [a for a in artifacts if not a['expired']]
        selected = {}
        if platform == 'macos':
            for arch in ('arm64', 'x86_64'):
                matches = [a for a in artifacts if a['name'] == f'BlueGrass-macOS-{arch}-dev']
                require(len(matches) == 1, f'Missing/ambiguous macOS {arch} artifact')
                selected[arch] = matches[0]
        else:
            prefix = 'windows-release-' if platform == 'windows' else 'Android-arm64-'
            pattern = re.compile(re.escape(f"{prefix}{run['id']}-") + r'(\d+)')
            matches = [(int(m[1]), a) for a in artifacts if (m := pattern.fullmatch(a['name']))
                       and int(m[1]) <= run['run_attempt']]
            require(bool(matches), f'Missing {platform} artifact')
            selected[platform] = max(matches, key=lambda item: item[0])[1]
        return {key: self.command('api', f"repos/{self.repo}/actions/artifacts/{a['id']}/zip")
                for key, a in selected.items()}


def publish(api, state, files, repo, directory):
    previous = api.release()
    require(previous is not None, 'Release must exist before upload')
    old = parse_state(previous['body'])
    existing = {a['name']: a for a in previous['assets']}
    expected = {name: info for entry in state.values() for name, info in entry['assets'].items()}
    committed = {name for entry in old.values() for name in entry['assets']}
    for name, data in files.items():
        asset = existing.get(name)
        if asset and asset.get('state') == 'starter' and asset['size'] == 0 and name not in committed:
            # GitHub can leave an empty placeholder after a failed upload.
            # It is safe to retry only an unpublished, empty starter asset.
            api.delete_asset(asset)
            asset = None
        if asset:
            require(asset.get('digest') == 'sha256:' + sha256(data) and asset['size'] == len(data),
                    f'Existing asset differs; refusing overwrite: {name}')
            continue
        path = directory / name
        path.write_bytes(data)
        api.upload(path)
    uploaded = {a['name']: a for a in api.release()['assets']}
    for name, info in expected.items():
        asset = uploaded.get(name, {})
        require(asset.get('digest') == 'sha256:' + info['sha256'] and asset.get('size') == info['bytes'],
                f'Uploaded asset verification failed: {name}')
    pending = copy.deepcopy(state)
    for entry in pending.values():
        entry.pop('retiredAssets', None)
    for platform, entry in old.items():
        retired = (set(entry.get('retiredAssets', [])) | entry['assets'].keys()) - expected.keys()
        if retired:
            pending[platform]['retiredAssets'] = sorted(retired)
    api.edit(render_notes(pending, repo))
    retired = {name for entry in pending.values() for name in entry.get('retiredAssets', [])} - expected.keys()
    for name in retired:
        if name in uploaded:
            api.delete_asset(uploaded[name])
    if retired:
        for entry in pending.values():
            entry.pop('retiredAssets', None)
        api.edit(render_notes(pending, repo))


def reconcile(api, repo, directory, dry_run):
    release = api.release()
    state = parse_state(release['body']) if release else {}
    files, failures, changed = {}, [], []
    for platform, workflow in WORKFLOWS.items():
        try:
            runs = api.pages(f'actions/workflows/{workflow}/runs?branch=main&status=success&per_page=100', 'workflow_runs')
            run = pick_run(runs, repo)
            if not run or not newer(run, state.get(platform)):
                continue
            relation = api.api(f"compare/{run['head_sha']}...main")
            require(relation['status'] in ('ahead', 'identical'), 'Build commit is not on current main history')
            print(f"Verifying {platform}: run {run['id']}, attempt {run['run_attempt']}", flush=True)
            entry, payload = prepare(platform, run, api.archives(platform, run))
            if platform == 'windows':
                config = api.api(f"contents/Windows/release.json?ref={run['head_sha']}")
                identity = json.loads(base64.b64decode(config['content']))
                require(entry['version'] == f"{identity['version']}+{identity['buildBase'] + run['run_number']}",
                        'Windows artifact does not match source release configuration')
            state[platform] = entry
            files.update(payload)
            changed.append(platform)
        except (ValueError, KeyError, RuntimeError, zipfile.BadZipFile) as exc:
            failures.append(f'{platform}: {exc}')
    notes = render_notes(state, repo)
    (directory / 'preview-notes.md').write_text(notes, encoding='utf-8')
    print(json.dumps({'updated': changed, 'platforms': {k: v['version'] for k, v in state.items()},
                      'dryRun': dry_run, 'errors': failures}, ensure_ascii=False), flush=True)
    if changed and not dry_run:
        if release is None:
            api.create(api.api('commits/main')['sha'])
        publish(api, state, files, repo, directory)
    elif release and (release['draft'] or any(e.get('retiredAssets') for e in state.values())) and not dry_run:
        publish(api, state, files, repo, directory)
    if not state:
        failures.append('No verified platform packages are available')
    require(not failures, '\n'.join(failures))
    return state


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--repo', default=os.environ.get('GITHUB_REPOSITORY'))
    parser.add_argument('--dry-run', action='store_true', help='Download and verify only; never change GitHub')
    parser.add_argument('--work-dir', type=Path)
    args = parser.parse_args()
    require(args.repo and re.fullmatch(r'[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+', args.repo), 'Pass --repo owner/name')
    with tempfile.TemporaryDirectory(prefix='bluegrass-preview-') as temporary:
        directory = args.work_dir or Path(temporary)
        directory.mkdir(parents=True, exist_ok=True)
        reconcile(GitHub(args.repo, directory), args.repo, directory, args.dry_run)
    if summary := os.environ.get('GITHUB_STEP_SUMMARY'):
        with open(summary, 'a', encoding='utf-8') as stream:
            stream.write(f'[统一开发预览版](https://github.com/{args.repo}/releases/tag/{TAG})\n')


if __name__ == '__main__':
    main()
