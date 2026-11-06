$ErrorActionPreference = 'Stop'
. "$PSScriptRoot/toolchain.ps1"
$Go = (Get-Command go -ErrorAction Stop).Source
$clientRoot = Split-Path $PSScriptRoot -Parent
$repoRoot = Split-Path $clientRoot -Parent
$evidence = Join-Path $clientRoot '.artifacts/verification/wintun-loader'
$present = Join-Path $evidence 'present'
$absent = Join-Path $evidence 'absent'
New-Item -ItemType Directory -Force -Path $present, $absent | Out-Null
if (Test-Path -LiteralPath (Join-Path $absent 'wintun.dll')) { throw 'The negative-test directory must not contain Wintun.' }
Push-Location (Join-Path $repoRoot 'core')
try {
    & $Go test -c -o (Join-Path $present 'loader-test.exe') ./tunengine
    if ($LASTEXITCODE -ne 0) { throw 'Driver loader test build failed.' }
} finally { Pop-Location }
& "$PSScriptRoot/prepare-wintun.ps1" -Destination $present
Copy-Item -LiteralPath (Join-Path $present 'loader-test.exe') -Destination (Join-Path $absent 'loader-test.exe') -Force
$priorMode = $env:BLUEGRASS_TEST_WINTUN_LOAD
try {
    Push-Location (Join-Path $env:SystemRoot 'System32')
    try {
        $env:BLUEGRASS_TEST_WINTUN_LOAD = 'present'
        & (Join-Path $present 'loader-test.exe') '-test.run=^TestWintunLoadFromExecutableDirectory$' '-test.v'
        if ($LASTEXITCODE -ne 0) { throw 'EXE-adjacent approved driver did not load.' }
    } finally { Pop-Location }
    # CWD contains the valid DLL, but the executable's own folder does not.
    Push-Location $present
    try {
        $env:BLUEGRASS_TEST_WINTUN_LOAD = 'absent'
        & (Join-Path $absent 'loader-test.exe') '-test.run=^TestWintunLoadFromExecutableDirectory$' '-test.v'
        if ($LASTEXITCODE -ne 0) { throw 'Driver loader used a non-adjacent DLL.' }
    } finally { Pop-Location }
} finally { $env:BLUEGRASS_TEST_WINTUN_LOAD = $priorMode }
Write-Host 'Approved app-local Wintun loads; missing adjacent DLL rejects fallback. No adapter was opened.'
