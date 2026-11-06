function Get-BlueGrassRelease {
    param(
        [string]$Version = '',
        [ValidateRange(0, 65535)][int]$Build = 0
    )
    $clientRoot = Split-Path $PSScriptRoot -Parent
    $repoRoot = Split-Path $clientRoot -Parent
    $release = Get-Content -LiteralPath (Join-Path $repoRoot 'release.json') -Raw -Encoding UTF8 | ConvertFrom-Json
    if ($release.version -isnot [string] -or $release.version -cnotmatch '\A(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\z') {
        throw 'Root release.json version must be X.X.X without leading zeros or suffixes.'
    }
    if ($Version -and $Version -cne $release.version) { throw 'Version must match root release.json.' }
    if (@($release.version.Split('.') | Where-Object { $_.Length -gt 5 -or [int]$_ -gt 65535 }).Count -gt 0) {
        throw 'Root release.json version must fit Windows version resources.'
    }
    $platformRelease = Get-Content -LiteralPath (Join-Path $clientRoot 'release.json') -Raw -Encoding UTF8 | ConvertFrom-Json
    $base = $platformRelease.buildBase
    if (($base -isnot [int] -and $base -isnot [long]) -or $base -lt 1 -or $base -gt 65535) {
        throw 'Windows release.json buildBase must be an integer in 1..65535.'
    }
    if (!$Build) { $Build = $base }
    if ($Build -lt $base) { throw 'Build must not be below Windows buildBase.' }
    [PSCustomObject]@{ Version = $release.version; Build = $Build; BuildBase = $base }
}
