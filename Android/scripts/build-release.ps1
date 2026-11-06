param(
    [string]$AndroidSdk = $env:ANDROID_HOME,
    [string]$JavaHome = $env:JAVA_HOME,
    [string]$FlutterExecutable = 'flutter',
    [string]$GoExecutable = 'go',
    [string]$PythonExecutable = 'python',
    [ValidateRange(1, 2100000000)][int]$BuildNumber,
    [switch]$RequireReleaseSigning
)
$ErrorActionPreference = 'Stop'
$root = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
. "$PSScriptRoot/release-config.ps1"
$release = Get-BlueGrassAndroidRelease -BuildNumber $BuildNumber
$versionName = $release.Version
$BuildNumber = $release.BuildNumber
if (-not $AndroidSdk) { $AndroidSdk = 'E:\AndroidTools\sdk' }
if (-not $JavaHome) { $JavaHome = 'D:\Android\Android Studio\jbr' }
$AndroidSdk = [IO.Path]::GetFullPath($AndroidSdk)
$JavaHome = [IO.Path]::GetFullPath($JavaHome)

# Fail before compilation, and never print key material or passwords.
$signingNames = @('BLUEGRASS_ANDROID_KEYSTORE', 'BLUEGRASS_ANDROID_STORE_PASSWORD', 'BLUEGRASS_ANDROID_KEY_ALIAS', 'BLUEGRASS_ANDROID_KEY_PASSWORD')
$configured = @($signingNames | Where-Object { -not [string]::IsNullOrWhiteSpace([Environment]::GetEnvironmentVariable($_)) })
if ($configured.Count -ne 0 -and $configured.Count -ne 4) { throw 'Provide all four BLUEGRASS_ANDROID_* signing variables, or none for a local test build.' }
if (($RequireReleaseSigning -or $env:BLUEGRASS_REQUIRE_RELEASE_SIGNING -eq 'true') -and $configured.Count -ne 4) { throw 'Publishing requires a persistent signing key. Configure the four Android Actions secrets first.' }
if ($configured.Count -eq 4 -and -not (Test-Path -LiteralPath $env:BLUEGRASS_ANDROID_KEYSTORE -PathType Leaf)) { throw 'The configured Android keystore does not exist.' }
$signingMode = if ($configured.Count -eq 4) { 'configured' } else { 'local-debug' }
$sourceCommit = (& git -C $root rev-parse HEAD).Trim()
if ($LASTEXITCODE -ne 0) { throw 'Unable to determine source commit.' }
$sourceChanges = & git -C $root status --porcelain --untracked-files=normal
if ($LASTEXITCODE -ne 0) { throw 'Unable to determine source tree status.' }
$sourceDirty = -not [string]::IsNullOrWhiteSpace(($sourceChanges -join "`n"))

function Invoke-Checked([string]$Executable, [string[]]$Arguments) {
    & $Executable @Arguments
    if ($LASTEXITCODE -ne 0) { throw "Build command failed: $Executable (exit $LASTEXITCODE)." }
}
$saved = @{}
foreach ($name in @('JAVA_HOME', 'ANDROID_HOME', 'ANDROID_SDK_ROOT', 'BLUEGRASS_REQUIRE_RELEASE_SIGNING')) {
    $saved[$name] = [Environment]::GetEnvironmentVariable($name)
}
Push-Location $root
try {
    $env:JAVA_HOME = $JavaHome
    $env:ANDROID_HOME = $AndroidSdk
    $env:ANDROID_SDK_ROOT = $AndroidSdk
    if ($RequireReleaseSigning) { $env:BLUEGRASS_REQUIRE_RELEASE_SIGNING = 'true' }
    Invoke-Checked $FlutterExecutable @('clean')
    Invoke-Checked $FlutterExecutable @('pub', 'get', '--enforce-lockfile')
    Push-Location (Join-Path (Split-Path $root -Parent) 'packages/client')
    try {
        Invoke-Checked $FlutterExecutable @('pub', 'get', '--enforce-lockfile')
        Invoke-Checked $FlutterExecutable @('analyze')
        Invoke-Checked $FlutterExecutable @('test')
    } finally { Pop-Location }
    Invoke-Checked $FlutterExecutable @('analyze')
    Invoke-Checked $FlutterExecutable @('test')
    Push-Location (Join-Path $root 'native/core')
    try {
        # Go validates downloaded dependencies against go.sum. `go mod verify`
        # also looks for a cache archive for our locally replaced module, which
        # has no downloaded zip; use readonly compilation and checked-in locks.
        $packages = @('.', 'bluegrass/native/core/routingpolicy', 'bluegrass/native/core/routingupdate', 'bluegrass/native/core/protocolconfig', 'bluegrass/native/core/gate')
        Invoke-Checked $GoExecutable (@('test', '-mod=readonly', '-count=1') + $packages)
        Invoke-Checked $GoExecutable (@('vet', '-mod=readonly') + $packages)
    } finally { Pop-Location }
    & (Join-Path $PSScriptRoot 'build-native.ps1') -AndroidSdk $AndroidSdk -JavaHome $JavaHome -GoExecutable $GoExecutable
    # Do not use --no-pub: mode-specific plugin regeneration excludes integration_test.
    Invoke-Checked $FlutterExecutable @('build', 'apk', '--release', '--target', 'lib/main.dart', '--target-platform', 'android-arm64', "--build-name=$versionName", '--build-number', "$BuildNumber", "--dart-define=BLUEGRASS_VERSION=$versionName", "--dart-define=BLUEGRASS_BUILD=$BuildNumber")
    $verification = @(
        (Join-Path $PSScriptRoot 'verify-apk.py'),
        '--apk', (Join-Path $root 'build/app/outputs/flutter-apk/app-release.apk'),
        '--sdk', $AndroidSdk, '--output-dir', (Join-Path $root "dist/$versionName+$BuildNumber"),
        '--version', $versionName, '--build-number', "$BuildNumber",
        '--source-commit', $sourceCommit, '--signing-mode', $signingMode
    )
    if ($sourceDirty) { $verification += '--source-dirty' }
    Invoke-Checked $PythonExecutable $verification
} finally {
    Pop-Location
    foreach ($name in $saved.Keys) { [Environment]::SetEnvironmentVariable($name, $saved[$name], 'Process') }
}
