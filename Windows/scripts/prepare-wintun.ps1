param([string]$Destination = '', [switch]$VerifyOnly)
$ErrorActionPreference = 'Stop'
$clientRoot = [IO.Path]::GetFullPath((Split-Path $PSScriptRoot -Parent))
$driver = (Get-Content -LiteralPath (Join-Path $clientRoot 'toolchain.json') -Raw | ConvertFrom-Json).wintun
if (!$driver -or $driver.version -cne '0.14.1' -or $driver.architecture -cne 'amd64') {
    throw 'The approved Wintun 0.14.1 amd64 metadata is required.'
}

function Assert-OrdinaryPath([string]$Path) {
    $current = [IO.Path]::GetFullPath($Path)
    while ($current) {
        if (Test-Path -LiteralPath $current) {
            if ((Get-Item -LiteralPath $current -Force).Attributes -band [IO.FileAttributes]::ReparsePoint) {
                throw "Wintun path contains a reparse point: $current"
            }
        }
        $current = [IO.Path]::GetDirectoryName($current)
    }
}

function Assert-Wintun([string]$Dll, [string]$License) {
    foreach ($file in @($Dll, $License)) {
        Assert-OrdinaryPath $file
        if (!(Test-Path -LiteralPath $file -PathType Leaf)) { throw "Required Wintun file missing: $file" }
    }
    if ((Get-FileHash -LiteralPath $Dll -Algorithm SHA256).Hash.ToLowerInvariant() -cne $driver.dllSHA256) {
        throw 'Wintun DLL SHA256 does not match the approved official amd64 binary.'
    }
    if ((Get-FileHash -LiteralPath $License -Algorithm SHA256).Hash.ToLowerInvariant() -cne $driver.licenseSHA256) {
        throw 'Wintun prebuilt-binaries license SHA256 mismatch.'
    }
    $bytes = [IO.File]::ReadAllBytes($Dll)
    $pe = [BitConverter]::ToInt32($bytes, 0x3c)
    if ([BitConverter]::ToUInt32($bytes, $pe) -ne 0x4550 -or [BitConverter]::ToUInt16($bytes, $pe + 4) -ne 0x8664) {
        throw 'Wintun must be an amd64 PE DLL.'
    }
    $signature = Get-AuthenticodeSignature -LiteralPath $Dll
    if ($signature.Status -ne 'Valid' -or !$signature.SignerCertificate -or
        $signature.SignerCertificate.Thumbprint -cne $driver.signerThumbprint) {
        throw 'Wintun requires the valid official WireGuard Authenticode signature.'
    }
    if ((Get-Item -LiteralPath $Dll).VersionInfo.FileVersion -cne $driver.version) {
        throw 'Unexpected Wintun file version.'
    }
}

$sourceDll = Join-Path $clientRoot $driver.dllSourcePath
$sourceLicense = Join-Path $clientRoot $driver.licenseSourcePath
if (!$Destination -and $VerifyOnly) {
    Assert-Wintun $sourceDll $sourceLicense
    return
}
if (!$Destination) { $Destination = Join-Path $clientRoot 'native/bin' }
$Destination = [IO.Path]::GetFullPath($Destination)
$dll = Join-Path $Destination 'wintun.dll'
$license = Join-Path $Destination 'LICENSE.wintun'
if (!$VerifyOnly) {
    Assert-Wintun $sourceDll $sourceLicense
    Assert-OrdinaryPath $dll
    Assert-OrdinaryPath $license
    New-Item -ItemType Directory -Path $Destination -Force | Out-Null
    # App-local, unchanged vendor bytes. Never re-sign or load from PATH/CWD.
    Copy-Item -LiteralPath $sourceDll -Destination $dll -Force
    Copy-Item -LiteralPath $sourceLicense -Destination $license -Force
}
Assert-Wintun $dll $license
