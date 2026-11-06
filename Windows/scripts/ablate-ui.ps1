$ErrorActionPreference = 'Stop'
. "$PSScriptRoot\toolchain.ps1"
$clientRoot = Split-Path $PSScriptRoot -Parent
$repoRoot = Split-Path $clientRoot -Parent
$experiment = Join-Path $clientRoot ('.artifacts\ablation\ui-' + [Guid]::NewGuid().ToString('N'))
$contracts = Join-Path $experiment 'contracts'
New-Item -ItemType Directory -Force -Path $contracts | Out-Null
Copy-Item -LiteralPath (Join-Path $repoRoot 'contracts\fixtures') -Destination $contracts -Recurse
$app = Join-Path $experiment 'Windows\app'
New-Item -ItemType Directory -Force -Path $app | Out-Null
foreach ($name in @('lib', 'test', 'assets', 'pubspec.yaml', 'pubspec.lock')) {
    Copy-Item -LiteralPath (Join-Path $clientRoot "app\$name") -Destination $app -Recurse
}
foreach ($package in @('domain', 'data_source', 'client')) {
    $target = Join-Path $experiment "packages\$package"
    New-Item -ItemType Directory -Force -Path $target | Out-Null
    foreach ($name in @('lib', 'pubspec.yaml', 'pubspec.lock')) {
        $source = Join-Path $repoRoot "packages\$package\$name"
        if (Test-Path -LiteralPath $source) { Copy-Item -LiteralPath $source -Destination $target -Recurse }
    }
}
$logs = Join-Path $clientRoot '.artifacts\verification'
New-Item -ItemType Directory -Force -Path $logs | Out-Null
$target = Join-Path $experiment 'packages\client\lib\app_controller.dart'
$originalBytes = [IO.File]::ReadAllBytes($target)
$original = [Text.Encoding]::UTF8.GetString($originalBytes)
$guard = 'if (_disposed || attempt != _attempt || generation != _generation) return;'
if (!$original.Contains($guard)) { throw 'Attempt guard changed; review mutation before rerunning.' }
Push-Location $app
try {
    & $Flutter pub get --offline *> (Join-Path $logs 'ui-ablation-dependencies.log')
    if ($LASTEXITCODE -ne 0) { throw 'Isolated dependency setup failed.' }
    & $Flutter test --reporter expanded *> (Join-Path $logs 'ui-ablation-baseline.log')
    if ($LASTEXITCODE -ne 0) { throw 'Baseline must pass before ablation.' }
    [IO.File]::WriteAllText($target, $original.Replace($guard, ''), [Text.UTF8Encoding]::new($false))
    & $Flutter test test/app_controller_test.dart --plain-name 'cancelled detail response cannot establish connection' --reporter expanded *> (Join-Path $logs 'ui-ablation-no-attempt-guard.log')
    $mutationExit = $LASTEXITCODE
} finally {
    [IO.File]::WriteAllBytes($target, $originalBytes)
    Pop-Location
}
if ($mutationExit -eq 0) { throw 'Mutation survived: test failed to detect unsafe late response.' }
$failure = Get-Content -Raw (Join-Path $logs 'ui-ablation-no-attempt-guard.log')
if ($failure -notmatch "Actual: 'connected'") { throw 'Mutation failed for an unexpected reason. Inspect the log.' }
Push-Location $app
try {
    & $Flutter test --reporter expanded *> (Join-Path $logs 'ui-ablation-restored.log')
    if ($LASTEXITCODE -ne 0) { throw 'Restored suite must pass.' }
} finally { Pop-Location }
Write-Host 'Attempt guard removal was detected (idle became connected); restored full suite passed.'
