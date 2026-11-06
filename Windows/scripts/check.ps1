param([switch]$NativeIntegration)
$ErrorActionPreference = 'Stop'
. "$PSScriptRoot\toolchain.ps1"
$Go = (Get-Command go -ErrorAction Stop).Source
# CPython wheels must not be loaded by the unrelated MSYS Python runtime.
$Python = Get-BlueGrassPython
if (!(Get-Command gcc -ErrorAction SilentlyContinue) -and (Test-Path -LiteralPath 'C:\msys64\ucrt64\bin\gcc.exe')) {
    $env:Path = 'C:\msys64\ucrt64\bin;' + $env:Path
}
$env:CGO_ENABLED = '1'
$clientRoot = Split-Path $PSScriptRoot -Parent
$logs = Join-Path $clientRoot '.artifacts\verification'
New-Item -ItemType Directory -Force -Path $logs | Out-Null

function Invoke-Check([string]$Name, [string]$Directory, [string]$Command, [string[]]$Arguments) {
    Push-Location $Directory
    try {
        & $Command @Arguments 2>&1 | Tee-Object -FilePath (Join-Path $logs "$Name.log")
        if ($LASTEXITCODE -ne 0) { throw "$Name failed: exit $LASTEXITCODE" }
    } finally { Pop-Location }
}

foreach ($package in @('domain', 'data_source')) {
    $directory = Join-Path $clientRoot "packages\$package"
    Invoke-Check "$package-pub" $directory $Dart @('pub', 'get')
    Invoke-Check "$package-analyze" $directory $Dart @('analyze', '--fatal-infos')
    Invoke-Check "$package-test" $directory $Dart @('test', '--reporter', 'expanded')
}
& "$PSScriptRoot\verify-native-dependencies.ps1" -Go $Go
& "$PSScriptRoot\prepare-wintun.ps1" -VerifyOnly
Invoke-Check 'native-test' (Join-Path $clientRoot 'native\core') $Go @('test', '-p', '1', '-race', '-count=1', './...')
Invoke-Check 'native-vet' (Join-Path $clientRoot 'native\core') $Go @('vet', './...')
Invoke-Check 'contracts' (Join-Path $clientRoot 'contracts') $Python @('test_contracts.py')
Invoke-Check 'live-api-redirects' (Join-Path $clientRoot 'scripts') $Python @('test_live_api_transport.py')
Invoke-Check 'app-pub' (Join-Path $clientRoot 'app') $Flutter @('pub', 'get')
Invoke-Check 'app-analyze' (Join-Path $clientRoot 'app') $Flutter @('analyze')
Invoke-Check 'app-test' (Join-Path $clientRoot 'app') $Flutter @('test', '--reporter', 'expanded')
if ($NativeIntegration) {
    & "$PSScriptRoot\test-portable.ps1"
    & "$PSScriptRoot\build-native.ps1"
    & "$PSScriptRoot\prepare-wintun.ps1" -Destination (Join-Path $clientRoot 'app/build/windows/x64/runner/Debug')
    Invoke-Check 'windows-integration' (Join-Path $clientRoot 'app') $Flutter @('test', 'integration_test/windows_test.dart', '-d', 'windows', '--reporter', 'expanded')
} else {
    Write-Host 'Native Windows integration was not requested. Use -NativeIntegration to run DPAPI and desktop UI tests.'
}
Write-Host "Checks passed. Logs: $logs"
