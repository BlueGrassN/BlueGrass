param(
    [string]$ApiUrl = 'https://client-api.sunip.vip/api/client/v1',
    [string]$WebsiteUrl = 'https://sunipip.com/',
    [string]$ApiProxy = '',
    [ValidateRange(1, 65535)][int]$Build = 1,
    [ValidatePattern('^(0|[1-9][0-9]{0,4})\.(0|[1-9][0-9]{0,4})\.(0|[1-9][0-9]{0,4})(-(beta|rc)\.(0|[1-9][0-9]{0,4}))?$')][string]$Version = '0.1.0',
    [switch]$NoExpand
)
$ErrorActionPreference = 'Stop'
if (@(($Version -split '-')[0].Split('.') | Where-Object { [int]$_ -gt 65535 }).Count -gt 0) { throw 'Version must fit Windows version resources.' }
. "$PSScriptRoot\toolchain.ps1"
$api = [Uri]$ApiUrl
if (!$api.IsAbsoluteUri -or $api.Scheme -ne 'https' -or $api.UserInfo -or $api.Query -or $api.Fragment) {
    throw 'ApiUrl must be an HTTPS API base without credentials, query or fragment.'
}
if ($ApiUrl -cne 'https://client-api.sunip.vip/api/client/v1') {
    throw 'This build uses the shared client-api.sunip.vip bootstrap. Publish client-config/config.json to change the runtime API endpoint.'
}
& "$PSScriptRoot\build-native.ps1" -ControlApiHost $api.DnsSafeHost
if ($WebsiteUrl) {
    $website = [Uri]$WebsiteUrl
    if (!$website.IsAbsoluteUri -or $website.Scheme -ne 'https' -or $website.UserInfo) {
        throw 'WebsiteUrl must be HTTPS without credentials.'
    }
}
$clientRoot = Split-Path $PSScriptRoot -Parent
Push-Location (Join-Path $clientRoot 'app')
try {
    & $Flutter pub get
    if ($LASTEXITCODE -ne 0) { throw 'Dependency resolution failed' }
    # Only the production entry point can produce a release bundle.
    & $Flutter build windows --release --target lib/main.dart "--build-name=$Version" "--build-number=$Build" "--dart-define=BLUEGRASS_VERSION=$Version" "--dart-define=BLUEGRASS_BUILD=$Build" "--dart-define=BLUEGRASS_API_URL=$ApiUrl" "--dart-define=BLUEGRASS_WEBSITE_URL=$WebsiteUrl" "--dart-define=BLUEGRASS_API_PROXY=$ApiProxy"
    if ($LASTEXITCODE -ne 0) { throw 'Windows release build failed' }
    $bundle = Join-Path $clientRoot 'app\build\windows\x64\runner\Release'
    if (!(Test-Path (Join-Path $bundle 'BlueGrass.exe'))) { throw 'BlueGrass.exe is missing' }
    $vswhere = Join-Path ${env:ProgramFiles(x86)} 'Microsoft Visual Studio\Installer\vswhere.exe'
    $visualStudio = & $vswhere -latest -products '*' -requires Microsoft.VisualStudio.Component.VC.Tools.x86.x64 -property installationPath
    if (!$visualStudio) { throw 'Visual C++ redistribution files are required for packaging.' }
    $runtimeVersion = (Get-Content (Join-Path $visualStudio 'VC\Auxiliary\Build\Microsoft.VCRedistVersion.default.txt') -Raw).Trim()
    $runtime = Join-Path $visualStudio "VC\Redist\MSVC\$runtimeVersion\x64\Microsoft.VC143.CRT"
    if (!(Test-Path -LiteralPath $runtime)) { throw 'The selected Visual C++ x64 CRT directory is missing.' }
    Copy-Item -Path "$runtime\*.dll" -Destination $bundle -Force
    $packagedBinding = Join-Path $bundle 'BlueGrass.test-binding.json'
    if (Test-Path -LiteralPath $packagedBinding) { Remove-Item -LiteralPath $packagedBinding }
    # Flutter refreshes its asset directory; remove stale top-level notices too.
    foreach ($retiredLicense in @('LICENSE.sing-box', 'LICENSE.sing-tun', 'LICENSE.gvisor')) {
        $stale = Join-Path $bundle $retiredLicense
        if (Test-Path -LiteralPath $stale) { Remove-Item -LiteralPath $stale -Force }
    }
    & "$PSScriptRoot\prepare-wintun.ps1" -Destination $bundle
    foreach ($license in @('LICENSE.Xray-core', 'LICENSE.wintun', 'LICENSE.sing')) {
        Copy-Item -LiteralPath (Join-Path $clientRoot ('app\assets\' + $license)) -Destination $bundle -Force
    }
    . (Join-Path $clientRoot 'installer/payload.ps1')
    # Distribute the exact derived datasets and their attribution alongside the
    # embedded copies; these files are notices/source, never runtime overrides.
    foreach ($relative in $RoutingDataPayload) {
        $destination = Join-Path $bundle ('data\routing-policy\' + $relative)
        New-Item -ItemType Directory -Path (Split-Path $destination -Parent) -Force | Out-Null
        Copy-Item -LiteralPath (Join-Path $clientRoot ('native\core\routingpolicy\assets\' + $relative)) -Destination $destination -Force
    }
    $portable = & "$PSScriptRoot\stage-portable.ps1" -Bundle $bundle
    $dist = Join-Path $clientRoot 'dist'
    New-Item -ItemType Directory -Force -Path $dist | Out-Null
    $archive = Join-Path $dist 'BlueGrass-Windows-x64-dev.zip'
    Compress-Archive -Path "$portable\*" -DestinationPath $archive -Force
    if (!$NoExpand) { & "$PSScriptRoot\expand-bundle.ps1" }
    Get-FileHash -Algorithm SHA256 $archive | Format-List
    Write-Host "Unsigned Windows portable bundle ($Version+$Build): $archive"
} finally { Pop-Location }
