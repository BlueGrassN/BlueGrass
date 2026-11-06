param()
$ErrorActionPreference = 'Stop'
$clientRoot = Split-Path $PSScriptRoot -Parent
$repoRoot = Split-Path $clientRoot -Parent
$go = 'D:\Program Files\Go\bin\go.exe'
$windres = 'C:\msys64\ucrt64\bin\windres.exe'
if (!(Test-Path -LiteralPath $go) -or !(Test-Path -LiteralPath $windres)) { throw 'Go and windres are required to build the update helper.' }
$output = Join-Path $clientRoot ('.artifacts\update\' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $output -Force | Out-Null
$executable = Join-Path $output 'BlueGrass.Update.exe'
$priorCgo = $env:CGO_ENABLED
Push-Location (Join-Path $repoRoot 'core')
try {
    $env:CGO_ENABLED = '0'
    & "$PSScriptRoot\verify-native-dependencies.ps1" -Go $go
    Push-Location 'cmd\bluegrass-update'
    try {
        & $windres '-i' 'resource.rc' '-o' 'resource_windows_amd64.syso' '-O' 'coff' '--target=pe-x86-64'
        if ($LASTEXITCODE -ne 0) { throw 'Update asInvoker resource compilation failed.' }
    } finally { Pop-Location }
    & $go build -mod=readonly -trimpath -ldflags '-s -w -H windowsgui' -o $executable ./cmd/bluegrass-update
    if ($LASTEXITCODE -ne 0) { throw 'Update helper build failed.' }
} finally { $env:CGO_ENABLED = $priorCgo; Pop-Location }
[PSCustomObject]@{ Executable=$executable; SHA256=(Get-FileHash -LiteralPath $executable -Algorithm SHA256).Hash.ToLowerInvariant(); Signed=$false; Distributable=$false }
