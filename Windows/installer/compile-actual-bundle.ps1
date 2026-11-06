param(
    [string]$Version = '0.1.0',
    [uint32]$Build = 1,
    [Parameter(Mandatory)][switch]$CompileEvidenceOnly
)
$ErrorActionPreference = 'Stop'
if (!$CompileEvidenceOnly) { throw 'This script only snapshots real build files for non-installable compile evidence.' }
$clientRoot = Split-Path $PSScriptRoot -Parent
. (Join-Path $PSScriptRoot 'payload.ps1')
$gui = Join-Path $clientRoot 'app\build\windows\x64\runner\Release'
$native = Join-Path $clientRoot 'native\bin'
$staging = Join-Path $clientRoot ('.artifacts\installer-inputs\actual-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Force -Path $staging | Out-Null
$origins = @()
foreach ($name in ($RequiredPayload + $OptionalPayload)) {
    if ($name -in @('BlueGrass.SetupTrust.exe','BlueGrass.UserCleanup.exe')) { continue }
    $source = if ($name -in $NativePayload) { Join-Path $native $name } else { Join-Path $gui $name }
    if (!(Test-Path -LiteralPath $source -PathType Leaf)) {
        if ($name -in $RequiredPayload) { throw "Actual compiled payload missing: $name" }
        continue
    }
    $hash = (Get-FileHash -LiteralPath $source -Algorithm SHA256).Hash
    $destination = Join-Path $staging $name
    New-Item -ItemType Directory -Force -Path (Split-Path $destination -Parent) | Out-Null
    Copy-Item -LiteralPath $source -Destination $destination
    if ((Get-FileHash -LiteralPath $destination -Algorithm SHA256).Hash -ne $hash -or (Get-FileHash -LiteralPath $source -Algorithm SHA256).Hash -ne $hash) { throw 'Source changed during binary snapshot.' }
    $origins += [PSCustomObject]@{ Payload=$name; Source=$source; SourceModified=(Get-Item -LiteralPath $source).LastWriteTimeUtc; SHA256=$hash.ToLowerInvariant() }
}
$result = & (Join-Path $clientRoot 'scripts\build-installer.ps1') -Bundle $staging -Version $Version -Build $Build -UnsignedCandidate
$evidence = Join-Path (Split-Path $result.Setup -Parent) 'actual-build-origins.json'
@{ CompileEvidenceOnly=$true; Distributable=$false; Sources=$origins; GUIBuildFreshnessNotAsserted=$true; ExcludedAllNonAllowlistedInputFiles=$true; NoUserBindingOrTokensCopied=$true } | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath $evidence -Encoding utf8
$result | Add-Member -NotePropertyName Origins -NotePropertyValue $evidence -PassThru
