[CmdletBinding()]
param(
    [string]$Go = 'D:\Program Files\Go\bin\go.exe',
    [string]$Dart = 'E:\tools\flutter\bin\cache\dart-sdk\bin\dart.exe',
    [string]$FlutterTools = 'E:\tools\flutter\bin\cache\flutter_tools.snapshot',
    [string]$Python = 'C:\msys64\ucrt64\bin\python.exe',
    [switch]$ScriptOnly
)
$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false # Mutants must return failure without aborting restoration.
$clientRoot = [IO.Path]::GetFullPath((Split-Path $PSScriptRoot -Parent))
$experiment = Join-Path $clientRoot ('.artifacts\ablation\observed-exit-' + (Get-Date -Format 'yyyyMMdd-HHmmss') + '-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $experiment | Out-Null
$utf8 = [Text.UTF8Encoding]::new($false)

# Execute actual Python functions, omitting only module-level DLL/account-loader
# initialization. All native processes, WinINET, clock and network I/O are test
# boundaries. No real account, port, proxy, service or connection is accessed.
$pythonTests = @'
import ast
import copy
import ipaddress
import json
from pathlib import Path
import sys
import types
import unittest
import uuid

path = Path(sys.argv.pop(1))
tree = ast.parse(path.read_text(encoding='utf-8-sig'), filename=str(path))
functions = ast.Module(body=[n for n in tree.body if isinstance(n, (ast.FunctionDef, ast.ClassDef))
    and n.name in ('native_check', 'wait_observed_exit')], type_ignores=[])

class Clock:
    value = 0.0
    def monotonic(self): return self.value
    def sleep(self, seconds): self.value += seconds

class Flow:
    def __init__(self, kind):
        # Disabled proxy can still retain a dormant 10808 string. Restoration
        # compares the complete original tuple; there is no listener/PID premise.
        self.original = (1, '127.0.0.1:10808', '<local>;10.*', '')
        self.value = self.original
        self.owner = None
        self.journal = None
        self.recovery = False
        self.reports = []
        self.commands = []
        self.key = dict(environment='fixture', dataSource='fixture', accountKey='account', sourceId='node', kind=kind)

    def snapshot(self, host):
        return dict(connection=host.state, systemProxyApplied=self.value == host.own,
            proxyRecoveryRequired=self.recovery, proxySettingsPreserved=host.preserved,
            capabilities=['system_proxy'], businessGateOpen=host.state == 'connected', gateKnown=True,
            proxyAddress='127.0.0.1:32123', verifiedExitIpv4='9.9.9.9' if host.reads > 2 else '',
            context=dict(runtimeEpoch='runtime', logicalSessionId='logical', attemptId=str(host.attempt), resourceKey=self.key.copy()))

    def host(self, unused):
        flow = self
        class Host:
            def __init__(self):
                self.state, self.attempt, self.reads = 'idle', 0, 0
                self.preserved = False
                self.own = (3, '127.0.0.1:32123', '<local>', '')
                self.competitor = flow.owner is not None
                self.alive = True
                if not self.competitor: flow.owner = self
                self.process = types.SimpleNamespace(stdin=types.SimpleNamespace(closed=False))
                def close():
                    self.process.stdin.closed = True
                    if not self.competitor and self.alive:
                        self.ok('Disconnect')
                        self.alive = False
                        flow.owner = None
                def kill():
                    self.alive = False
                    if flow.owner is self: flow.owner = None
                self.process.stdin.close = close
                self.process.kill = kill
                self.process.wait = lambda timeout: 1 if self.competitor else 0
                self.process.poll = lambda: None if self.alive and not self.competitor else 0
            def ok(self, command, **kwargs):
                flow.commands.append(command)
                if command == 'Hello' and flow.journal:
                    if flow.value == self.own:
                        flow.value = flow.original
                        flow.journal = None
                    else: flow.recovery = True
                if command == 'Pause': self.state = 'paused'
                if command == 'GetStatus':
                    self.reads += 1
                    if flow.journal and flow.value != self.own:
                        self.state, flow.recovery = 'paused', True
                if command == 'Disconnect':
                    self.preserved = flow.journal is not None and flow.value != self.own
                    if flow.journal and flow.value == self.own: flow.value = flow.original
                    flow.journal, flow.recovery, self.state = None, False, 'idle'
                return flow.snapshot(self)
            def connect(self, profile):
                flow.commands.append('Connect')
                if 'expectedExitIpv4' in profile:
                    raise AssertionError('native_check sent an obsolete expected-IP requirement')
                if profile['key'] != flow.key:
                    raise AssertionError('native_check changed the supplied logical resource key/kind')
                self.attempt += 1
                self.reads = 0
                self.state = 'connected'
                flow.value, flow.journal = self.own, True
                return flow.snapshot(self)
            def eof(self): self.process.stdin.close()
        return Host()

    def request(self):
        if self.owner is None or self.owner.state != 'connected': raise OSError('fixture gate closed')
        return '8.8.8.8'  # Deliberately differs from the native observation.

def load(flow):
    clock = Clock()
    namespace = dict(uuid=uuid, time=clock, ipaddress=ipaddress, Host=flow.host,
        settings=lambda: flow.value, set_settings=lambda v: setattr(flow, 'value', v),
        system_request=flow.request, report=lambda stage, **values: flow.reports.append(dict(stage=stage, **values)))
    exec(compile(functions, str(path), 'exec'), namespace)
    return namespace, clock

class ScriptContract(unittest.TestCase):
    def test_no_expected_ip_full_lifecycle_and_distinct_public_observations(self):
        for kind in ('subscription', 'asset'):
            with self.subTest(kind=kind):
                flow = Flow(kind)
                namespace, _ = load(flow)
                namespace['native_check']('unused.exe', dict(protocol='socks5', host='node.example.test', port=1234,
                    username='fixture', password='fixture', expires_at=None), flow.key)
                self.assertEqual(flow.value, flow.original)
                stages = {r['stage']: r for r in flow.reports}
                for stage in ('system_single_owner','system_pause','system_protected_retry','system_disconnect',
                              'system_parent_eof','system_native_crash_restart','system_ownership_loss','system_final_restore'):
                    self.assertIn(stage, stages)
                self.assertEqual(stages['windows_default_http']['observed'], '8.8.8.8')
                self.assertEqual(stages['windows_default_http']['reportedExit'], '9.9.9.9')
                self.assertEqual(flow.commands.count('Pause'), 1)

    def poll(self, change=None):
        flow = Flow('asset')
        namespace, clock = load(flow)
        connected = dict(connection='connected', businessGateOpen=True, gateKnown=True,
            systemProxyApplied=True, proxyRecoveryRequired=False, proxyAddress='127.0.0.1:32123',
            context=dict(runtimeEpoch='r',logicalSessionId='l',attemptId='a',resourceKey=flow.key.copy()))
        calls = []
        def status(command, **kwargs):
            calls.append(command)
            self.assertEqual(command, 'GetStatus')
            self.assertGreater(kwargs['timeout'], 0)
            self.assertLessEqual(kwargs['timeout'], .5)
            snapshot = copy.deepcopy(connected)
            if change: change(snapshot, len(calls))
            return snapshot
        host = types.SimpleNamespace(ok=status)
        return namespace['wait_observed_exit'], host, connected, clock, calls

    def test_waits_after_connected_for_current_public_observation(self):
        wait, host, connected, clock, calls = self.poll(lambda s,n: s.update(verifiedExitIpv4='' if n<3 else '9.9.9.9'))
        self.assertEqual(wait(host, connected, timeout=.5), '9.9.9.9')
        self.assertEqual(len(calls), 3)

    def test_missing_detector_is_bounded_without_changing_connection(self):
        wait, host, connected, clock, calls = self.poll()
        with self.assertRaises(TimeoutError): wait(host, connected, timeout=.5)
        self.assertLessEqual(clock.value, .5)
        self.assertGreater(len(calls), 0)
        self.assertEqual(connected['connection'], 'connected')
        self.assertTrue(all(name == 'GetStatus' for name in calls))

    def test_late_attempt_observation_is_not_accepted(self):
        def stale(s,n):
            s['verifiedExitIpv4']='9.9.9.9'
            s['context']['attemptId']='old-attempt'
        wait, host, connected, _, _ = self.poll(stale)
        with self.assertRaises(RuntimeError): wait(host, connected, timeout=.5)

    def test_nonpublic_observation_is_not_reported_as_exit(self):
        for value in ('10.0.0.1', '224.0.0.1', '0.0.0.0', '2001:4860:4860::8888', 'invalid', 134744072):
            with self.subTest(value=value):
                wait, host, connected, _, _ = self.poll(lambda s,n:s.update(verifiedExitIpv4=value))
                with self.assertRaises(ValueError): wait(host, connected, timeout=.5)

unittest.main()
'@
$testPath = Join-Path $experiment 'system_proxy_contract_test.py'
[IO.File]::WriteAllText($testPath, $pythonTests, $utf8)
$proxyScript = Join-Path $PSScriptRoot 'system-proxy-check.py'
& $Python -B $testPath $proxyScript *> (Join-Path $experiment 'script-contract.log')
if ($LASTEXITCODE -ne 0) {
    Get-Content -LiteralPath (Join-Path $experiment 'script-contract.log')
    throw "Offline script contract failed: $experiment"
}
if ($ScriptOnly) { Write-Output "Offline script contract passed: $experiment"; return }

$copyRoot = Join-Path $experiment 'workspace'
$native = Join-Path $copyRoot 'native\core'
$app = Join-Path $copyRoot 'app'
$inputs = [Collections.Generic.List[object]]::new()
function Copy-Input([string]$Relative) {
    $source = Join-Path $clientRoot $Relative
    $dest = Join-Path $copyRoot $Relative
    $before = (Get-FileHash -LiteralPath $source -Algorithm SHA256).Hash
    New-Item -ItemType Directory -Force -Path (Split-Path $dest -Parent) | Out-Null
    Copy-Item -LiteralPath $source -Destination $dest
    if ((Get-FileHash -LiteralPath $dest -Algorithm SHA256).Hash -ne $before) {
        throw "Source changed during snapshot copy: $Relative"
    }
    $inputs.Add(@{path=$Relative; sha256=$before.ToLowerInvariant()})
}
foreach ($file in @('native\core\go.mod','native\core\go.sum')) { Copy-Input $file }
foreach ($package in @('control','engine','gate','clock','protocolconfig')) {
    foreach ($file in Get-ChildItem -LiteralPath (Join-Path $clientRoot "native\core\$package") -Filter '*.go' -File) {
        Copy-Input "native\core\$package\$($file.Name)"
    }
}
foreach ($folder in @('app\lib','app\assets','packages\domain\lib','packages\data_source\lib','contracts\fixtures')) {
    foreach ($file in Get-ChildItem -LiteralPath (Join-Path $clientRoot $folder) -File -Recurse) {
        Copy-Input ([IO.Path]::GetRelativePath($clientRoot, $file.FullName))
    }
}
foreach ($file in @('app\test\observed_exit_test.dart','app\test\utils\native_process_fixture.dart')) { Copy-Input $file }
foreach ($project in @('app','packages\domain','packages\data_source')) {
    foreach ($name in @('pubspec.yaml','pubspec.lock')) {
        $relative = "$project\$name"
        if (Test-Path -LiteralPath (Join-Path $clientRoot $relative)) { Copy-Input $relative }
    }
}
$inputs | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath (Join-Path $experiment 'source-sha256.json') -Encoding utf8

function Replace-ExactlyOnce([string]$Text, [string]$Before, [string]$After) {
    if ([regex]::Matches($Text, [regex]::Escape($Before)).Count -ne 1) {
        throw 'Ablation anchor changed or is ambiguous; review current source and tests.'
    }
    return $Text.Replace($Before, $After)
}
function Native-Behavior([string]$Phase, [string]$Pattern) {
    $log = Join-Path $experiment "native-$Phase.log"
    Push-Location $native
    try {
        & $Go test -mod=readonly ./control -count=1 "-run=$Pattern" -v *> $log
        return $LASTEXITCODE
    } finally { Pop-Location }
}
function App-Behavior([string]$Phase, [switch]$Mutation) {
    $log = Join-Path $experiment "gui-$Phase.log"
    $arguments = @($FlutterTools, 'test', '--no-pub', '--concurrency=1', '--reporter', 'expanded', 'test/observed_exit_test.dart')
    if ($Mutation) { $arguments += @('--plain-name', 'connection observation is advisory and context bound: changed-exit-system-proxy') }
    Push-Location $app
    try { & $Dart @arguments *> $log; return $LASTEXITCODE }
    finally { Pop-Location }
}

# Current-attempt validation remains required for publishing an observation. It
# is independent of admission and never compares an expected exit address.
$nativePath = Join-Path $native 'control\controller.go'
$nativeOriginal = [IO.File]::ReadAllBytes($nativePath)
$nativeText = [Text.Encoding]::UTF8.GetString($nativeOriginal).Replace("`r`n", "`n")
$nativeBefore = "result, err := c.e.ProbeExit(a.ctx)`n`tc.mu.Lock()`n`tdefer c.mu.Unlock()`n`tif !c.currentLocked(a) {`n`t`treturn`n`t}"
$nativeAfter = "result, err := c.e.ProbeExit(a.ctx)`n`tc.mu.Lock()`n`tdefer c.mu.Unlock()"
$nativeMutation = Replace-ExactlyOnce $nativeText $nativeBefore $nativeAfter
if ((Native-Behavior 'baseline' '^TestObservedExit') -ne 0) { throw "Native observation baseline failed: $experiment" }
try {
    [IO.File]::WriteAllText($nativePath, $nativeMutation, $utf8)
    $code = Native-Behavior 'ablated' '^TestObservedExitCancellationCannotPublishLateAddress$'
    $log = [IO.File]::ReadAllText((Join-Path $experiment 'native-ablated.log'))
    if ($code -eq 0 -or !$log.Contains('late observation survived cancellation') -or
            !$log.Contains('--- FAIL: TestObservedExitCancellationCannotPublishLateAddress') -or $log.Contains('[build failed]')) {
        throw "Native mutation did not cause the expected behavioral failure: $experiment"
    }
} finally { [IO.File]::WriteAllBytes($nativePath, $nativeOriginal) }
if ((Native-Behavior 'restored' '^TestObservedExit') -ne 0) { throw "Native restoration failed: $experiment" }

# Resolve dependencies offline inside the copy. Never reuse a package_config
# that points the app/domain/data_source back into the shared workspace.
Push-Location $app
try {
    & $Dart $FlutterTools pub get --offline *> (Join-Path $experiment 'gui-dependencies.log')
    if ($LASTEXITCODE -ne 0) { throw "Isolated offline dependency setup failed: $experiment" }
} finally { Pop-Location }
$configPath = Join-Path $app '.dart_tool\package_config.json'
$config = Get-Content -LiteralPath $configPath -Raw | ConvertFrom-Json
$configURI = [Uri]::new($configPath)
foreach ($name in @('bluegrass_client','bluegrass_domain','bluegrass_data_source')) {
    $entry = @($config.packages | Where-Object name -eq $name)
    if ($entry.Count -ne 1) { throw "Missing isolated package: $name" }
    $resolved = [Uri]::new($configURI, [string]$entry[0].rootUri).LocalPath
    if (![IO.Path]::GetFullPath($resolved).StartsWith($copyRoot + '\', [StringComparison]::OrdinalIgnoreCase)) {
        throw "Local package escaped the isolated snapshot: $name"
    }
}
$guiPath = Join-Path $app 'lib\app_controller.dart'
$guiOriginal = [IO.File]::ReadAllBytes($guiPath)
$guiText = [Text.Encoding]::UTF8.GetString($guiOriginal)
$guiMutation = Replace-ExactlyOnce $guiText 'actualExitIp = value is String && domain.isPublicIpv4(value) ? value : null;' '// Ablation: omit applying the current native observation to GUI state.'
if ((App-Behavior 'baseline') -ne 0) { throw "GUI observation baseline failed: $experiment" }
try {
    [IO.File]::WriteAllText($guiPath, $guiMutation, $utf8)
    $code = App-Behavior 'ablated' -Mutation
    $log = [IO.File]::ReadAllText((Join-Path $experiment 'gui-ablated.log'))
    if ($code -eq 0 -or !$log.Contains("Expected: '8.8.8.8'") -or !$log.Contains('Actual: <null>') -or
            $log.Contains('Compilation failed') -or $log.Contains('Failed to load')) {
        throw "GUI mutation did not cause the expected behavioral failure: $experiment"
    }
} finally { [IO.File]::WriteAllBytes($guiPath, $guiOriginal) }
if ((App-Behavior 'restored') -ne 0) { throw "GUI restoration failed: $experiment" }

$changed = @($inputs | Where-Object {
    (Get-FileHash -LiteralPath (Join-Path $clientRoot $_.path) -Algorithm SHA256).Hash.ToLowerInvariant() -ne $_.sha256
})
$results = @{
    scriptContract='5 tests passed (subscription and asset lifecycle fixtures)'
    sourceSnapshotCurrent=($changed.Count -eq 0)
    changedSourcePaths=@($changed | ForEach-Object { $_.path })
    experiments=@(
        @{name='remove-current-attempt-observation-check'; baseline='pass'; ablated='late observation survived cancellation'; restored='pass'; decision='retain current-attempt publication check'},
        @{name='remove-gui-observed-ip-update'; baseline='pass'; ablated='expected actual observed IPv4 but GUI remained null'; restored='pass'; decision='retain native snapshot to GUI observation flow'}
    )
    expectedExitGuard='not required by the corrected product flow; not an ablation target'
    liveSystemProxyRun=$false
}
$results | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath (Join-Path $experiment 'results.json') -Encoding utf8
if ($changed.Count -ne 0) { throw "Shared source changed since snapshot; rerun against the new checkpoint: $experiment" }
Write-Output "Two isolated observed-exit behavior ablations passed (baseline/failure/restoration): $experiment"
