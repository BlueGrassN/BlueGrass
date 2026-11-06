param()
$ErrorActionPreference = 'Stop'
$clientRoot = Split-Path $PSScriptRoot -Parent
. (Join-Path $PSScriptRoot 'payload.ps1')
. (Join-Path $PSScriptRoot 'candidate-payload.ps1')
$testRoot = Join-Path ([IO.Path]::GetTempPath()) ('BlueGrass-candidate-payload-' + [Guid]::NewGuid().ToString('N'))
$payload = Join-Path $testRoot 'payload'

function Read-TestInventory {
    @($RequiredPayload | Where-Object { Test-Path -LiteralPath (Join-Path $payload $_) -PathType Leaf } | ForEach-Object {
        $file = Get-Item -LiteralPath (Join-Path $payload $_)
        [PSCustomObject]@{ Path=$_; Size=$file.Length; SHA256=(Get-FileHash -LiteralPath $file.FullName -Algorithm SHA256).Hash.ToLowerInvariant() }
    })
}
function Assert-Rejected([string]$Expected, [scriptblock]$Action) {
    try { & $Action | Out-Null } catch {
        if ($_.Exception.Message -notlike "*$Expected*") { throw }
        return
    }
    throw "Expected rejection: $Expected"
}
try {
    foreach ($name in $RequiredPayload) {
        $path = Join-Path $payload $name
        New-Item -ItemType Directory -Path (Split-Path $path -Parent) -Force | Out-Null
        [IO.File]::WriteAllText($path, 'inventory-only fixture; never executable')
    }
    & (Join-Path $clientRoot 'scripts/prepare-wintun.ps1') -Destination $payload
    $inventory = Read-TestInventory
    $hashes = Get-VerifiedCandidatePayload -Payload $payload -Inventory $inventory -Origins @()
    if ($hashes.Count -ne $RequiredPayload.Count) { throw 'Complete payload was not verified.' }
    foreach ($name in @('wintun.dll', 'LICENSE.wintun', 'data\app.so')) {
        $path = Join-Path $payload $name
        $bytes = [IO.File]::ReadAllBytes($path)
        Remove-Item -LiteralPath $path
        # Omit both file and record: an allowlist/count-only check misses this.
        $incomplete = Read-TestInventory
        Assert-Rejected "Required candidate payload missing: $name" {
            Get-VerifiedCandidatePayload -Payload $payload -Inventory $incomplete -Origins @()
        }
        [IO.File]::WriteAllBytes($path, $bytes)
    }
    Assert-Rejected 'Required candidate inventory entry missing: wintun.dll' {
        Get-VerifiedCandidatePayload -Payload $payload -Inventory @($inventory | Where-Object Path -ne 'wintun.dll') -Origins @()
    }
    $dll = Join-Path $payload 'wintun.dll'
    $original = [IO.File]::ReadAllBytes($dll)
    $changed = [byte[]]$original.Clone()
    $changed[512] = $changed[512] -bxor 1
    [IO.File]::WriteAllBytes($dll, $changed)
    # Recomputed inventory and provenance must not substitute the official pin.
    $changedInventory = Read-TestInventory
    $changedOrigins = @($changedInventory | ForEach-Object { [PSCustomObject]@{ Payload=$_.Path; SHA256=$_.SHA256 } })
    Assert-Rejected 'Wintun DLL SHA256' {
        Get-VerifiedCandidatePayload -Payload $payload -Inventory $changedInventory -Origins $changedOrigins
    }
    [IO.File]::WriteAllBytes($dll, $original)
    Get-VerifiedCandidatePayload -Payload $payload -Inventory $inventory -Origins @() | Out-Null
    Write-Output 'PASS: complete candidate payload; omitted required DLL/license/data; omitted inventory entry; tampered DLL with recomputed inventory/provenance rejected.'
} finally {
    $resolved = [IO.Path]::GetFullPath($testRoot)
    $tempRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('\') + '\'
    if (!$resolved.StartsWith($tempRoot, [StringComparison]::OrdinalIgnoreCase) -or [IO.Path]::GetFileName($resolved) -notlike 'BlueGrass-candidate-payload-*') { throw 'Unsafe test cleanup path.' }
    if (Test-Path -LiteralPath $resolved) { Remove-Item -LiteralPath $resolved -Recurse -Force }
}
