param([string]$ControlApiHost = 'client-api.sunip.vip')
$ErrorActionPreference = 'Stop'
if ($ControlApiHost -notmatch '^[a-zA-Z0-9.-]+$' -or $ControlApiHost.Length -gt 253) { throw 'ControlApiHost must be a numeric IPv4 or DNS host name.' }
$ControlApiHost = $ControlApiHost.ToLowerInvariant()
. "$PSScriptRoot/environment.ps1"
$Go = (Get-Command go -ErrorAction Stop).Source
$clientRoot = Split-Path $PSScriptRoot -Parent
$output = Join-Path $clientRoot 'native\bin'
New-Item -ItemType Directory -Force -Path $output | Out-Null
Push-Location (Join-Path $clientRoot 'native\core')
$priorCgo = $env:CGO_ENABLED
$priorGoos = $env:GOOS
$priorGoarch = $env:GOARCH
try {
    $env:CGO_ENABLED = '0'
    $env:GOOS = 'windows'
    $env:GOARCH = 'amd64'
    & "$PSScriptRoot\verify-native-dependencies.ps1" -Go $Go
    & "$PSScriptRoot\prepare-wintun.ps1" -Destination $output
    $windres = if ($env:BLUEGRASS_WINDRES) { $env:BLUEGRASS_WINDRES } else {
        $found = Get-Command windres -ErrorAction SilentlyContinue
        if ($found) { $found.Source } else { 'C:\msys64\ucrt64\bin\windres.exe' }
    }
    if (!(Test-Path -LiteralPath $windres)) { throw 'windres is required for native execution manifests.' }
    foreach ($command in @('bluegrass-repair', 'bluegrass-update', 'bluegrass-diagnostics')) {
        Push-Location (Join-Path 'cmd' $command)
        try {
            & $windres '-i' 'resource.rc' '-o' 'resource_windows_amd64.syso' '-O' 'coff' '--target=pe-x86-64'
            if ($LASTEXITCODE -ne 0) { throw "Native execution manifest compilation failed: $command" }
        } finally { Pop-Location }
    }
    & $Go build -mod=readonly -p 2 -trimpath -ldflags '-s -w -H windowsgui' -o (Join-Path $output 'BlueGrass.Native.exe') ./cmd/bluegrass-native
    if ($LASTEXITCODE -ne 0) { throw 'Native build failed' }
    & $Go build -mod=readonly -p 2 -trimpath -ldflags "-s -w -X bluegrass/native/core/winservice.ControlAPIHost=$ControlApiHost" -o (Join-Path $output 'BlueGrass.Service.exe') ./cmd/bluegrass-service
    if ($LASTEXITCODE -ne 0) { throw 'Service build failed' }
    & $Go build -mod=readonly -p 2 -trimpath -ldflags '-s -w' -o (Join-Path $output 'BlueGrass.Repair.exe') ./cmd/bluegrass-repair
    if ($LASTEXITCODE -ne 0) { throw 'Repair build failed' }
    & $Go build -mod=readonly -p 2 -trimpath -ldflags '-s -w -H windowsgui' -o (Join-Path $output 'BlueGrass.Update.exe') ./cmd/bluegrass-update
    if ($LASTEXITCODE -ne 0) { throw 'Update helper build failed' }
    & $Go build -mod=readonly -p 2 -trimpath -ldflags '-s -w -H windowsgui' -o (Join-Path $output 'BlueGrass.Diagnostics.exe') ./cmd/bluegrass-diagnostics
    if ($LASTEXITCODE -ne 0) { throw 'Diagnostics helper build failed' }
    & "$PSScriptRoot\verify-native-dependencies.ps1" -Go $Go -BinaryDirectory $output
} finally { $env:CGO_ENABLED = $priorCgo; $env:GOOS = $priorGoos; $env:GOARCH = $priorGoarch; Pop-Location }
