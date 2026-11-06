param()
$ErrorActionPreference = 'Stop'
$clientRoot = Split-Path $PSScriptRoot -Parent
$builder = Join-Path $clientRoot 'scripts\build-installer.ps1'
$testRoot = Join-Path ([IO.Path]::GetTempPath()) ('BlueGrass-installer-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $testRoot | Out-Null
$bundle = Join-Path $testRoot 'bundle'
New-Item -ItemType Directory -Path $bundle | Out-Null
$priorPath = $env:Path
function Expect-Rejection([string]$Expected, [scriptblock]$Action) {
    try { & $Action; throw 'Expected rejection did not happen.' }
    catch { if ($_.Exception.Message -notlike "*$Expected*") { throw } }
}
try {
    Expect-Rejection 'Required Wintun file missing' { & $builder -Bundle $bundle -Version 0.1.0 -Build 1 -UnsignedCandidate -ValidateOnly }
    . (Join-Path $PSScriptRoot 'payload.ps1')
    $files = $RequiredPayload + @('BlueGrass.SetupTrust.exe','BlueGrass.UserCleanup.exe')
    foreach ($name in $files) {
        $target = Join-Path $bundle $name
        New-Item -ItemType Directory -Force -Path (Split-Path $target -Parent) | Out-Null
        [IO.File]::WriteAllText($target, 'temporary compile fixture')
    }
    & (Join-Path $clientRoot 'scripts/prepare-wintun.ps1') -Destination $bundle
    # Only Go build-info inspection is stubbed. This inventory/signing test
    # must not rebuild native code or depend on a concurrently replaced bin/.
    # The real official DLL hash, version and Authenticode checks always run.
    $tools = Join-Path $testRoot 'metadata-fixture'
    New-Item -ItemType Directory -Path $tools | Out-Null
    $pin = (Get-Content -LiteralPath (Join-Path $clientRoot 'toolchain.json') -Raw | ConvertFrom-Json).embeddedCoreModule
    if ($pin -notmatch '^[a-zA-Z0-9.+-]+$') { throw 'Invalid fixture module pin.' }
    $goFixture = @'
@echo off
if not "%~1"=="version" exit /b 2
if not "%~2"=="-m" exit /b 2
if not exist "%~3" exit /b 1
echo fixture.exe:
echo   dep github.com/xtls/xray-core __XRAY_PIN__ h1:fixture
echo   build GOOS=windows
echo   build GOARCH=amd64
exit /b 0
'@
    [IO.File]::WriteAllText((Join-Path $tools 'go.cmd'), $goFixture.Replace('__XRAY_PIN__', $pin), [Text.Encoding]::ASCII)
    $env:Path = $tools + ';' + $priorPath
    if ((Get-Command go).Source -ne (Join-Path $tools 'go.cmd')) { throw 'Isolated Go metadata fixture was not selected.' }
    $validation = & $builder -Bundle $bundle -Version 0.1.0 -Build 1 -UnsignedCandidate -ValidateOnly
    if (!$validation.Valid -or !$validation.UnsignedCandidate -or $validation.Distributable) { throw 'Candidate validation crossed the distribution/signing boundary.' }
    foreach ($name in @('wintun.dll', 'LICENSE.wintun')) {
        $target = Join-Path $bundle $name
        $bytes = [IO.File]::ReadAllBytes($target)
        Remove-Item -LiteralPath $target
        Expect-Rejection 'Required Wintun file missing' { & $builder -Bundle $bundle -Version 0.1.0 -Build 1 -UnsignedCandidate -ValidateOnly }
        [IO.File]::WriteAllBytes($target, $bytes)
    }
    $dll = Join-Path $bundle 'wintun.dll'
    $original = [IO.File]::ReadAllBytes($dll)
    $tampered = [byte[]]$original.Clone()
    $tampered[512] = $tampered[512] -bxor 1
    [IO.File]::WriteAllBytes($dll, $tampered)
    Expect-Rejection 'Wintun DLL SHA256' { & $builder -Bundle $bundle -Version 0.1.0 -Build 1 -UnsignedCandidate -ValidateOnly }
    [IO.File]::WriteAllBytes($dll, $original)
    foreach ($name in @('BlueGrass.exe', 'BlueGrass.Update.exe','BlueGrass.Diagnostics.exe', 'data\app.so')) {
        $target = Join-Path $bundle $name
        Remove-Item -LiteralPath $target
        $message = if ($name -in $NativePayload) { "Unable to inspect native binary: $name" } else { "Required payload missing: $name" }
        Expect-Rejection $message { & $builder -Bundle $bundle -Version 0.1.0 -Build 1 -UnsignedCandidate -ValidateOnly }
        [IO.File]::WriteAllText($target, 'temporary compile fixture')
    }
    $privateFile = Join-Path $bundle 'BlueGrass.test-binding.json'
    [IO.File]::WriteAllText($privateFile, '{}')
    Expect-Rejection 'Unapproved payload' { & $builder -Bundle $bundle -Version 0.1.0 -Build 1 -UnsignedCandidate -ValidateOnly }
    Remove-Item -LiteralPath $privateFile
    $tokenFile = Join-Path $bundle 'data\token.json'
    [IO.File]::WriteAllText($tokenFile, '{}')
    Expect-Rejection 'Unapproved payload' { & $builder -Bundle $bundle -Version 0.1.0 -Build 1 -UnsignedCandidate -ValidateOnly }
    Remove-Item -LiteralPath $tokenFile
    Expect-Rejection 'Unsigned or untrusted payload' { & $builder -Bundle $bundle -Version 0.1.0 -Build 1 -ValidateOnly }
    Expect-Rejection 'Version must' { & $builder -Bundle $bundle -Version '1.2.3 /bad' -Build 1 -UnsignedCandidate -ValidateOnly }
    Write-Output 'PASS: official signed Wintun fixture; missing/tampered DLL, missing license/runtime/helpers, private/test files, unsigned production and invalid version rejected. Go metadata inspection is isolated; no native build.'
} finally {
    $env:Path = $priorPath
    $resolved = [IO.Path]::GetFullPath($testRoot)
    $temp = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('\') + '\'
    if (!$resolved.StartsWith($temp, [StringComparison]::OrdinalIgnoreCase) -or [IO.Path]::GetFileName($resolved) -notlike 'BlueGrass-installer-*') { throw 'Unsafe test cleanup path.' }
    Remove-Item -LiteralPath $resolved -Recurse -Force
}
