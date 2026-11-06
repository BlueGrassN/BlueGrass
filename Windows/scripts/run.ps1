param([switch]$Fixture)
$ErrorActionPreference = 'Stop'
. "$PSScriptRoot\toolchain.ps1"
& "$PSScriptRoot\build-native.ps1"
& "$PSScriptRoot\prepare-wintun.ps1" -Destination (Join-Path (Split-Path $PSScriptRoot -Parent) 'app/build/windows/x64/runner/Debug')
Push-Location (Join-Path (Split-Path $PSScriptRoot -Parent) 'app')
try {
    $entry = if ($Fixture) { 'lib/main_fixture.dart' } else { 'lib/main.dart' }
    & $Flutter run -d windows --target $entry
    if ($LASTEXITCODE -ne 0) { throw 'Windows client exited with a Flutter error' }
} finally { Pop-Location }
