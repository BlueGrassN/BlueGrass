# Shared read-only candidate inventory validation; this never executes payloads.
function Get-VerifiedCandidatePayload {
    param(
        [Parameter(Mandatory)][string]$Payload,
        [Parameter(Mandatory)][AllowEmptyCollection()][object[]]$Inventory,
        [AllowEmptyCollection()][object[]]$Origins = @()
    )
    $ErrorActionPreference = 'Stop'
    $clientRoot = Split-Path $PSScriptRoot -Parent
    . (Join-Path $PSScriptRoot 'payload.ps1')
    if (!(Test-Path -LiteralPath $Payload -PathType Container)) { throw 'Candidate payload directory missing.' }
    if ((Get-Item -LiteralPath $Payload -Force).Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'Candidate payload contains a reparse point.' }
    $items = @(Get-ChildItem -LiteralPath $Payload -Recurse -Force)
    if ($items | Where-Object { $_.Attributes -band [IO.FileAttributes]::ReparsePoint }) { throw 'Candidate payload contains a reparse point.' }
    foreach ($name in $RequiredPayload) {
        if (!(Test-Path -LiteralPath (Join-Path $Payload $name) -PathType Leaf)) { throw "Required candidate payload missing: $name" }
        if ($name -notin $Inventory.Path) { throw "Required candidate inventory entry missing: $name" }
    }
    $allowed = $RequiredPayload + $OptionalPayload
    $hashes = @{}
    foreach ($record in $Inventory) {
        if ($record.Path -notin $allowed -or $hashes.ContainsKey($record.Path)) { throw 'Unexpected or duplicate payload inventory entry.' }
        $file = Join-Path $Payload $record.Path
        $hash = (Get-FileHash -LiteralPath $file -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($hash -ne $record.SHA256 -or (Get-Item -LiteralPath $file).Length -ne $record.Size) { throw 'Payload inventory mismatch.' }
        $hashes[$record.Path] = $hash
    }
    if (@($items | Where-Object { !$_.PSIsContainer }).Count -ne $hashes.Count) { throw 'Uninventoried payload file.' }
    foreach ($record in $Origins) {
        if ($hashes[$record.Payload] -ne $record.SHA256) { throw 'Actual build provenance mismatch.' }
    }
    # Inventories are evidence, not an authority to change the vendor pin.
    & (Join-Path $clientRoot 'scripts/prepare-wintun.ps1') -Destination $Payload -VerifyOnly
    return $hashes
}
