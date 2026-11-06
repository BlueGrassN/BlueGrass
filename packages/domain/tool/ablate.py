"""Run real safety ablations in a package-local copy; never mutate live sources."""
from pathlib import Path
import re
import shutil
import subprocess

ROOT = Path(__file__).resolve().parents[1]
WORK = ROOT / '.ablation'
DART = r'E:\tools\flutter\bin\dart.bat'

MUTATIONS = [
    ('observed-ip-validation', 'lib/src/connection.dart',
     '!isPublicIpv4(event.verifiedExitIpv4)', 'false'),
    ('deadline-check', 'lib/src/profile.dart',
     'if (node.expiresAtUtc != null && !effectiveNowUtc.isBefore(node.expiresAtUtc!)) {', 'if (false) {'),
    ('native-generation-check', 'lib/src/connection.dart',
     'if (event.context != state.context || event.eventSequence <= state.lastEventSequence) return next();',
     'if (event.eventSequence <= state.lastEventSequence) return next();'),
    ('exact-counter-representation', 'lib/src/traffic.dart',
     'return parsed;', 'return BigInt.from(parsed.toDouble());'),
    ('counter-sample-continuity', 'lib/src/traffic.dart',
     'current.sequence == previous.sequence + 1 && ', ''),
    ('batch-membership-boundary', 'lib/src/reminders.dart',
     'for (final key in valid.members) {',
     'for (final key in evaluateReminders(snapshot, this, effectiveNowUtc).dueItems) {'),
]


def run(label, *args):
    result = subprocess.run([DART, *args], cwd=WORK, capture_output=True, text=True, encoding='utf-8', errors='replace')
    output = result.stdout + result.stderr
    (WORK / f'{label}.log').write_text(output, encoding='utf-8')
    return result.returncode, output


def counts(output):
    values = re.findall(r'\+(\d+)(?: -(\d+))?:', output)
    if not values:
        raise RuntimeError('No test count in output')
    passed, failed = values[-1]
    return int(passed), int(failed or 0)


def main():
    WORK.mkdir(exist_ok=True)
    for folder in ['lib', 'test']:
        shutil.copytree(ROOT / folder, WORK / folder, dirs_exist_ok=True)
    for filename in ['pubspec.yaml', 'pubspec.lock']:
        shutil.copy2(ROOT / filename, WORK / filename)
    helper = WORK / 'test/fixtures.dart'
    text = helper.read_text(encoding='utf-8')
    text = text.replace('../../contracts/fixtures/', (ROOT.parent.parent / 'contracts/fixtures').as_posix() + '/')
    helper.write_text(text, encoding='utf-8')
    code, _ = run('pub-get', 'pub', 'get', '--offline')
    if code:
        raise RuntimeError('Isolated package dependency setup failed')
    code, output = run('baseline', 'test', '--reporter', 'expanded')
    if code:
        raise RuntimeError('Isolated baseline is not green')
    baseline = counts(output)
    rows = []
    for label, path, before, after in MUTATIONS:
        target = WORK / path
        original = target.read_text(encoding='utf-8')
        pattern = re.compile(re.escape(before).replace(r'\ ', r'\s*'))
        matches = list(pattern.finditer(original))
        if len(matches) != 1:
            raise RuntimeError(f'{label}: mutation anchor is not unique')
        matched = matches[0]
        try:
            target.write_text(original[:matched.start()] + after + original[matched.end():], encoding='utf-8')
            code, output = run(label, 'test', '--reporter', 'expanded')
            passed, failed = counts(output)
            if code == 0 or failed == 0 or 'Failed to load' in output:
                raise RuntimeError(f'{label}: did not produce a behavioral regression')
            rows.append((label, path, matched.group(), after, passed, failed))
            print(f'{label}: {passed} passed / {failed} failed', flush=True)
        finally:
            target.write_text(original, encoding='utf-8')
    code, output = run('restored', 'test', '--reporter', 'expanded')
    if code:
        raise RuntimeError('Restored copy is not green')
    restored = counts(output)
    report = [
        '# Domain safety ablation (executed)', '',
        'Run: `py -3.14 tool/ablate.py` with the configured Flutter SDK Dart on Windows.',
        'Expected-exit metadata no longer gates connection; the former expected-exit mutation was replaced by observed-IP validation.',
        'All mutations were applied only to `.ablation/lib/`; live source was unchanged.',
        f'Baseline: {baseline[0]} passed, {baseline[1]} failed. Restored: {restored[0]} passed, {restored[1]} failed.', '',
        '| Removed mechanism | Passed | Failed |', '| --- | ---: | ---: |',
    ]
    for label, _, _, _, passed, failed in rows:
        report.append(f'| {label} | {passed} | {failed} |')
    report.extend(['', '## Exact temporary source differences', ''])
    for label, path, before, after, _, _ in rows:
        report.extend([f'{label}: `{path}`', '', '```diff', '- ' + before.replace('\n', '\n- '), '+ ' + after, '```', ''])
    report.extend([
        'All six checks are retained: removing them caused existing behavioral tests to fail.',
        'Full stdout/stderr are in `.ablation/*.log` (ignored local evidence).',
        '', '## Abstraction review', '',
        'No forwarding service/repository/factory layer was present to remove. The package has',
        'immutable contract models and pure functions only; its sealed result/event types are',
        'required by K-05. No unperformed simplification is claimed as a source change.',
    ])
    (ROOT / 'ABLATION.md').write_text('\n'.join(report) + '\n', encoding='utf-8')
    print(f'Restored: {restored[0]} passed / {restored[1]} failed', flush=True)


if __name__ == '__main__':
    main()
