param([Parameter(Mandatory)][string]$Plan, [Parameter(Mandatory)][string]$Target)
$ErrorActionPreference = 'Stop'
try {
    . (Join-Path $PSScriptRoot 'signing.ps1')
    $config = Get-Content -Raw -LiteralPath $Plan | ConvertFrom-Json
    if ($config.Schema -ne 1) { throw 'Unsupported signing plan.' }
    [void](Assert-SigningArguments -Candidate $false -Thumbprint $config.Thumbprint -SignTool $config.SignTool -TimestampUrl $config.TimestampUrl)
    Assert-SigningTarget -Root $config.ArtifactRoot -Target $Target
    $identity = Get-ReleaseSigningIdentity -Thumbprint $config.Thumbprint -StoreLocation $config.StoreLocation -SignTool $config.SignTool
    if ($identity.PublisherSHA256 -cne $config.PublisherSHA256) { throw 'Release signing identity changed after preflight.' }
    $arguments = @(Get-ReleaseSignArguments -Identity $identity -TimestampUrl $config.TimestampUrl -Target $Target)
    & $identity.SignTool @arguments
    if ($LASTEXITCODE -ne 0) { throw 'SignTool signing or RFC3161 timestamping failed.' }
    Assert-SignedReleaseArtifact -Path $Target -Identity $identity
    # Inno's automatically signed uninstall/self-copy inputs are temporary.
    # Preserve the exact signed bytes and verification receipt before embedding.
    $kind = if ([IO.Path]::GetDirectoryName($Target) -ieq (Join-Path $config.ArtifactRoot 'signed-uninstaller') -and
        [IO.Path]::GetFileName($Target) -like 'uninst.*') { 'uninstaller' }
        elseif ($Target -ieq $config.Setup) { 'setup' } else { 'inno-intermediate' }
    $id = [Guid]::NewGuid().ToString('N')
    $snapshot = Join-Path $config.ReceiptDirectory ($kind + '-' + $id + '.exe')
    Copy-Item -LiteralPath $Target -Destination $snapshot
    $hash = (Get-FileHash -LiteralPath $Target -Algorithm SHA256).Hash.ToLowerInvariant()
    if ((Get-FileHash -LiteralPath $snapshot -Algorithm SHA256).Hash.ToLowerInvariant() -cne $hash) { throw 'Signed artifact changed while recording evidence.' }
    @{ Kind=$kind; Source=$Target; Snapshot=$snapshot; SHA256=$hash; PublisherSHA256=$identity.PublisherSHA256; TimestampVerified=$true } |
        ConvertTo-Json | Set-Content -LiteralPath (Join-Path $config.ReceiptDirectory ($id + '.json')) -Encoding utf8
    exit 0
} catch {
    Write-Error $_ -ErrorAction Continue
    exit 1
}
