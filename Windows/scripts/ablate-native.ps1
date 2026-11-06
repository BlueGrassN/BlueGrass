$ErrorActionPreference = 'Stop'
. "$PSScriptRoot\toolchain.ps1"
$Go = (Get-Command go -ErrorAction Stop).Source
$clientRoot = Split-Path $PSScriptRoot -Parent
$repoRoot = Split-Path $clientRoot -Parent
$sourceRoot = Join-Path $repoRoot 'core'
$experiment = Join-Path $clientRoot '.artifacts\ablation\native-gate'
New-Item -ItemType Directory -Force -Path $experiment | Out-Null
$native = Join-Path $experiment 'core'
New-Item -ItemType Directory -Force -Path $native | Out-Null
Get-ChildItem -LiteralPath $sourceRoot -Force | Copy-Item -Destination $native -Recurse -Force
$contracts = Join-Path $experiment 'contracts'
New-Item -ItemType Directory -Force -Path $contracts | Out-Null
Copy-Item -LiteralPath (Join-Path $repoRoot 'contracts\fixtures') -Destination $contracts -Recurse -Force
$code = Join-Path $native 'localengine\engine.go'
$original = [IO.File]::ReadAllBytes($code)
$text = [Text.Encoding]::UTF8.GetString($original)
$guard = 'if !e.gate || epoch != e.generation || dialCtx.Err() != nil {'
if (!$text.Contains($guard)) { throw 'Late-dial guard changed; review experiment.' }
Push-Location $native
try {
    & $Go test ./localengine -count=1 *> (Join-Path $experiment 'baseline.log')
    if ($LASTEXITCODE -ne 0) { throw 'Baseline failed' }
    [IO.File]::WriteAllText($code,$text.Replace($guard,'if false {'),[Text.UTF8Encoding]::new($false))
    & $Go test ./localengine -run TestGateCloseCancelsDialAndRejectsLateConnection -count=1 *> (Join-Path $experiment 'mutation.log')
    if ($LASTEXITCODE -eq 0 -or (Get-Content (Join-Path $experiment 'mutation.log') -Raw) -notmatch 'late dial escaped closed gate') {
        throw 'Mutation did not produce the expected behavioral failure'
    }
} finally {
    [IO.File]::WriteAllBytes($code,$original)
    Pop-Location
}
Push-Location $native
try {
    & $Go test ./localengine -count=1 *> (Join-Path $experiment 'restored.log')
    if ($LASTEXITCODE -ne 0) { throw 'Restored suite failed' }
} finally { Pop-Location }
Write-Host 'Isolated native ablation detected late-dial release; restored suite passed. Production source was unchanged.'
