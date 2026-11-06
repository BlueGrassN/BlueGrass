$ErrorActionPreference = 'Stop'
. "$PSScriptRoot\toolchain.ps1"
$Go = (Get-Command go -ErrorAction Stop).Source
$clientRoot = Split-Path $PSScriptRoot -Parent
$sourceRoot = Join-Path $clientRoot 'native\core'
$experiment = Join-Path $clientRoot '.artifacts\ablation\native-gate'
New-Item -ItemType Directory -Force -Path $experiment | Out-Null
foreach ($file in @('go.mod','go.sum')) { Copy-Item -LiteralPath (Join-Path $sourceRoot $file) -Destination $experiment -Force }
foreach ($directory in @('engine','gate','localengine')) {
    $target = Join-Path $experiment $directory
    New-Item -ItemType Directory -Force -Path $target | Out-Null
    Copy-Item -Path (Join-Path $sourceRoot "$directory\*.go") -Destination $target -Force
}
$code = Join-Path $experiment 'localengine\engine.go'
$original = [IO.File]::ReadAllBytes($code)
$text = [Text.Encoding]::UTF8.GetString($original)
$guard = 'if !e.gate || epoch != e.generation || dialCtx.Err() != nil {'
if (!$text.Contains($guard)) { throw 'Late-dial guard changed; review experiment.' }
Push-Location $experiment
try {
    & $Go test ./localengine -count=1 *> baseline.log
    if ($LASTEXITCODE -ne 0) { throw 'Baseline failed' }
    [IO.File]::WriteAllText($code,$text.Replace($guard,'if false {'),[Text.UTF8Encoding]::new($false))
    & $Go test ./localengine -run TestGateCloseCancelsDialAndRejectsLateConnection -count=1 *> mutation.log
    if ($LASTEXITCODE -eq 0 -or (Get-Content mutation.log -Raw) -notmatch 'late dial escaped closed gate') {
        throw 'Mutation did not produce the expected behavioral failure'
    }
} finally {
    [IO.File]::WriteAllBytes($code,$original)
    Pop-Location
}
Push-Location $experiment
try {
    & $Go test ./localengine -count=1 *> restored.log
    if ($LASTEXITCODE -ne 0) { throw 'Restored suite failed' }
} finally { Pop-Location }
Write-Host 'Isolated native ablation detected late-dial release; restored suite passed. Production source was unchanged.'
