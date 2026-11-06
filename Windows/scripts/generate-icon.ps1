param(
    [string]$Node = 'node',
    [string]$SharpModule = ''
)
$ErrorActionPreference = 'Stop'
# Keep launcher, tray, and UI assets synchronized with the SVG in assets/branding.
# sharp is only needed when regenerating; generated assets are checked in.
$brandArguments = @()
if ($SharpModule) { $brandArguments += @('--sharp', $SharpModule) }
& $Node (Join-Path $PSScriptRoot 'generate-brand-assets.cjs') @brandArguments
if ($LASTEXITCODE -ne 0) { throw 'Brand asset generation failed.' }
