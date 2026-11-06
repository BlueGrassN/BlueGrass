param([Parameter(Mandatory)][string]$ArtifactDirectory)
$ErrorActionPreference = 'Stop'
$clientRoot = Split-Path $PSScriptRoot -Parent
$directory = (Resolve-Path -LiteralPath $ArtifactDirectory).Path
$allowedRoot = [IO.Path]::GetFullPath((Join-Path $clientRoot '.artifacts\installers')).TrimEnd('\') + '\'
if (!$directory.StartsWith($allowedRoot,[StringComparison]::OrdinalIgnoreCase)) { throw 'Expected a local installer artifact directory.' }
$inventory = Get-Content -Raw -LiteralPath (Join-Path $directory 'payload-inventory.json') | ConvertFrom-Json
$origins = Get-Content -Raw -LiteralPath (Join-Path $directory 'actual-build-origins.json') | ConvertFrom-Json
if (!$inventory.CandidateOnly -or $inventory.Distributable -or !$origins.CompileEvidenceOnly -or $origins.Distributable) { throw 'Evidence must identify a non-distributable compile candidate.' }
$payload = Join-Path $directory 'payload'
. (Join-Path $PSScriptRoot 'payload.ps1')
. (Join-Path $PSScriptRoot 'candidate-payload.ps1')
$hashes = Get-VerifiedCandidatePayload -Payload $payload -Inventory @($inventory.Files) -Origins @($origins.Sources)
$go = 'D:\Program Files\Go\bin\go.exe'
$buildMetadata = @{}
foreach ($name in $NativePayload) {
    $original = Join-Path $clientRoot ('native\bin\' + $name)
    if ((Get-FileHash -LiteralPath $original -Algorithm SHA256).Hash.ToLowerInvariant() -ne $hashes[$name]) { throw "Native binary changed after snapshot: $name. Compile a fresh candidate." }
    $buildMetadata[$name] = @(& $go version -m (Join-Path $payload $name))
    if ($LASTEXITCODE -ne 0 -or !($buildMetadata[$name] -match 'path\s+bluegrass/native/core/cmd/bluegrass-')) { throw 'Missing actual native Go build metadata.' }
}
$repair = Join-Path $directory 'BlueGrass-Repair-unsigned-candidate.exe'
if ((Get-FileHash -LiteralPath $repair -Algorithm SHA256).Hash.ToLowerInvariant() -ne $hashes['BlueGrass.Repair.exe']) { throw 'Independent repair artifact is not the compiled repair binary.' }
$mt = Get-ChildItem 'C:\Program Files (x86)\Windows Kits\10\bin\*\x64\mt.exe' | Sort-Object FullName | Select-Object -Last 1
if (!$mt) { throw 'Windows SDK mt.exe required for read-only embedded manifest inspection.' }
foreach ($name in @('BlueGrass.Repair.exe','BlueGrass.UserCleanup.exe','BlueGrass.Update.exe','BlueGrass.Diagnostics.exe')) {
    $manifest = Join-Path $directory ($name + '.manifest.xml')
    & $mt.FullName '-nologo' ("-inputresource:" + (Join-Path $payload $name) + ';#1') ("-out:" + $manifest)
    if ($LASTEXITCODE -ne 0) { throw 'Embedded manifest extraction failed.' }
    [xml]$xml = Get-Content -Raw -LiteralPath $manifest
    $level = $xml.SelectSingleNode("//*[local-name()='requestedExecutionLevel']").level
    $expected = if ($name -eq 'BlueGrass.Repair.exe') { 'requireAdministrator' } else { 'asInvoker' }
    if ($level -ne $expected) { throw "Incorrect execution level: $name" }
}
$setup = Join-Path $directory ('BlueGrass-' + $inventory.Version + '-Windows-x64-unsigned-candidate.exe')
if ((Get-AuthenticodeSignature -LiteralPath $setup).Status -ne 'NotSigned') { throw 'Unexpected candidate signature state.' }
$result = [ordered]@{
    VerifiedAtUTC=[DateTime]::UtcNow.ToString('o'); Distributable=$false; ExecutedInstallerOrRepair=$false
    Setup=$setup; SetupSHA256=(Get-FileHash -LiteralPath $setup -Algorithm SHA256).Hash.ToLowerInvariant()
    Repair=$repair; RepairSHA256=$hashes['BlueGrass.Repair.exe']; PayloadFileCount=$hashes.Count
    AllPayloadHashesMatch=$true; ActualNativeBinariesMatch=$true; RequiredPayloadPresent=$true; PinnedWintunVerified=$true
    RepairExecutionLevel='requireAdministrator'; UserCleanupExecutionLevel='asInvoker'; UpdateExecutionLevel='asInvoker'; DiagnosticsExecutionLevel='asInvoker'
    NativeBuildMetadata=$buildMetadata
}
$result | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath (Join-Path $directory 'verification.json') -Encoding utf8
[PSCustomObject]$result | Select-Object Setup,SetupSHA256,RepairSHA256,PayloadFileCount,AllPayloadHashesMatch,ActualNativeBinariesMatch,RequiredPayloadPresent,PinnedWintunVerified,RepairExecutionLevel,UserCleanupExecutionLevel,Distributable | ConvertTo-Json
