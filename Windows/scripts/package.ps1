param(
    [string]$Version = '',
    [ValidateRange(0, 65535)][int]$Build = 0
)
$ErrorActionPreference = 'Stop'
$clientRoot = [IO.Path]::GetFullPath((Split-Path $PSScriptRoot -Parent))
$release = Get-Content -LiteralPath (Join-Path $clientRoot 'release.json') -Raw | ConvertFrom-Json
if (!$Version) { $Version = $release.version }
if (!$Build) { $Build = $release.buildBase }
. "$PSScriptRoot/toolchain.ps1"
$Python = Get-BlueGrassPython

& "$PSScriptRoot/check.ps1"
& "$PSScriptRoot/build.ps1" -Version $Version -Build $Build
foreach ($script in @('verify-release-bundles.py', 'verify-portable-runtime.py')) {
    & $Python (Join-Path $PSScriptRoot $script)
    if ($LASTEXITCODE -ne 0) { throw "$script failed." }
}

# Publish only these explicit files, never the source tree or verification logs.
$destination = Join-Path $clientRoot 'dist/release'
if ((Test-Path -LiteralPath $destination) -and
    ((Get-Item -LiteralPath $destination).Attributes -band [IO.FileAttributes]::ReparsePoint)) {
    throw 'Release staging must not be a linked directory.'
}
New-Item -ItemType Directory -Path $destination -Force | Out-Null
$archive = Join-Path $destination 'BlueGrass-Windows-x64.zip'
Copy-Item -LiteralPath (Join-Path $clientRoot 'dist/BlueGrass-Windows-x64-dev.zip') -Destination $archive -Force
$sha = & git -C $clientRoot rev-parse HEAD
if ($LASTEXITCODE -ne 0 -or $sha -notmatch '^[0-9a-f]{40}$') { throw 'Cannot identify source commit.' }
$changes = @(& git -C $clientRoot status --porcelain --untracked-files=normal)
if ($LASTEXITCODE -ne 0) { throw 'Cannot inspect source state.' }
$hash = (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash.ToLowerInvariant()
[ordered]@{
    version = $Version; build = $Build; platform = 'windows-x64'
    sourceCommit = $sha; sourceDirty = ($changes.Count -ne 0); createdUtc = [DateTime]::UtcNow.ToString('o')
    githubRunId = $env:GITHUB_RUN_ID; githubRunAttempt = $env:GITHUB_RUN_ATTEMPT
    archive = 'BlueGrass-Windows-x64.zip'; sha256 = $hash
    bytes = (Get-Item -LiteralPath $archive).Length; signed = $false
} | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $destination 'build-info.json') -Encoding utf8
$manifestHash = (Get-FileHash -LiteralPath (Join-Path $destination 'build-info.json') -Algorithm SHA256).Hash.ToLowerInvariant()
# The publisher runs GNU sha256sum on Linux: use LF without a BOM so CR is not
# interpreted as part of each filename.
[IO.File]::WriteAllText((Join-Path $destination 'SHA256SUMS.txt'),
    "$hash  BlueGrass-Windows-x64.zip`n$manifestHash  build-info.json`n",
    [Text.UTF8Encoding]::new($false))
Write-Host "Verified Windows package: $destination"
