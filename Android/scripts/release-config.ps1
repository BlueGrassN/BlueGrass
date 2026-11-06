function Get-BlueGrassAndroidRelease {
    param([ValidateRange(0, 2100000000)][int]$BuildNumber = 0)
    $androidRoot = Split-Path $PSScriptRoot -Parent
    $repoRoot = Split-Path $androidRoot -Parent
    $release = Get-Content -LiteralPath (Join-Path $repoRoot 'release.json') -Raw -Encoding UTF8 | ConvertFrom-Json
    if ($release.version -isnot [string] -or $release.version -cnotmatch '\A(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\z') {
        throw 'Root release.json version must be X.X.X without leading zeros or suffixes.'
    }
    # pubspec retains the local internal build floor; its public version is not authoritative.
    $match = [regex]::Match((Get-Content -LiteralPath (Join-Path $androidRoot 'pubspec.yaml') -Raw -Encoding UTF8), '(?m)^version:\s*[^\s+]+\+([1-9][0-9]*)\s*$')
    if (!$match.Success) { throw 'Android pubspec must retain its internal build number.' }
    $minimum = [long]$match.Groups[1].Value
    if ($minimum -lt 9 -or $minimum -gt 2100000000) { throw 'Android local build number must remain in 9..2100000000.' }
    if (!$BuildNumber) { $BuildNumber = $minimum }
    if ($BuildNumber -lt $minimum) { throw 'BuildNumber must not be below the pubspec build number.' }
    [PSCustomObject]@{ Version = $release.version; BuildNumber = $BuildNumber }
}
