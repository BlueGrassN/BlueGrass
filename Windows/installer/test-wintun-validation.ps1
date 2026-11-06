param([string]$TrustHelper = '', [string]$Compiler = '')
$ErrorActionPreference = 'Stop'
$clientRoot = Split-Path $PSScriptRoot -Parent
if (!$Compiler) { $Compiler = Join-Path $clientRoot '.artifacts/inno-7.1.0/ISCC.exe' }
if (!$TrustHelper) {
    $TrustHelper = (Get-ChildItem -LiteralPath (Join-Path $clientRoot '.artifacts/installers') -Filter BlueGrass.SetupTrust.exe -Recurse -File |
        Sort-Object LastWriteTimeUtc -Descending | Select-Object -First 1).FullName
}
if (!$TrustHelper -or !(Test-Path -LiteralPath $TrustHelper -PathType Leaf)) { throw 'An existing SetupTrust helper is required; this test never builds Go.' }
$TrustHelper = (Resolve-Path -LiteralPath $TrustHelper).Path
$testRoot = Join-Path ([IO.Path]::GetTempPath()) ('BlueGrass-wintun-installer-test-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $testRoot | Out-Null
$include = Join-Path $PSScriptRoot 'wintun.iss'
try {
    if (!(Test-Path -LiteralPath $include)) { throw 'Installer Wintun validation is missing.' }
    & (Join-Path $clientRoot 'scripts/prepare-wintun.ps1') -Destination $testRoot
    $source = @'
[Setup]
AppName=Wintun validator test
AppVersion=1
DefaultDirName={tmp}\unused
CreateAppDir=no
Uninstallable=no
PrivilegesRequired=lowest
OutputDir={#TestDir}
OutputBaseFilename=wintun-validator-test
Compression=none
SetupLogging=no
[Code]
#include ValidatorInclude
function InitializeSetup: Boolean;
var Accepted: Boolean;
begin
  Accepted := TrustedWintun(ExpandConstant('{param:DLL}'), ExpandConstant('{param:HELPER}'));
  if Accepted then
    SaveStringToFile(ExpandConstant('{param:RESULT}'), 'accepted', False)
  else
    SaveStringToFile(ExpandConstant('{param:RESULT}'), 'rejected', False);
  { This harness always aborts before any installation or registry writes. }
  Result := False;
end;
'@
    $script = Join-Path $testRoot 'validator-test.iss'
    [IO.File]::WriteAllText($script, $source, [Text.UTF8Encoding]::new($false))
    & $Compiler '--quiet-progress' '--no-signing' "/DTestDir=$testRoot" "/DValidatorInclude=$include" $script | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Isolated validator harness compilation failed.' }
    $exe = Join-Path $testRoot 'wintun-validator-test.exe'
    $dll = Join-Path $testRoot 'wintun.dll'
    $original = [IO.File]::ReadAllBytes($dll)
    foreach ($case in @('valid', 'missing', 'tampered', 'unavailable-trust-helper')) {
        [IO.File]::WriteAllBytes($dll, $original)
        $helper = $TrustHelper
        if ($case -eq 'missing') { Remove-Item -LiteralPath $dll }
        if ($case -eq 'tampered') {
            $bytes = [byte[]]$original.Clone()
            $bytes[512] = $bytes[512] -bxor 1
            [IO.File]::WriteAllBytes($dll, $bytes)
        }
        if ($case -eq 'unavailable-trust-helper') { $helper = Join-Path $testRoot 'no-trust-helper.exe' }
        $result = Join-Path $testRoot ($case + '.txt')
        $arguments = @('/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART', ('/DLL="' + $dll + '"'), ('/HELPER="' + $helper + '"'), ('/RESULT="' + $result + '"'))
        $process = Start-Process -FilePath $exe -ArgumentList $arguments -WindowStyle Hidden -PassThru
        if (!$process.WaitForExit(30000)) { $process.Kill(); throw "Validator timeout: $case" }
        $expected = if ($case -eq 'valid') { 'accepted' } else { 'rejected' }
        if (!(Test-Path -LiteralPath $result) -or [IO.File]::ReadAllText($result) -cne $expected) { throw "Unexpected installer validation result: $case" }
    }
    Write-Output 'PASS: actual Inno Wintun gate accepts official hash/version/vendor signature; rejects missing/tampered DLL and unavailable trust verification. No installation executed.'
} finally {
    $resolved = [IO.Path]::GetFullPath($testRoot)
    $tempRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('\') + '\'
    if (!$resolved.StartsWith($tempRoot, [StringComparison]::OrdinalIgnoreCase) -or [IO.Path]::GetFileName($resolved) -notlike 'BlueGrass-wintun-installer-test-*') { throw 'Unsafe test cleanup path.' }
    Remove-Item -LiteralPath $resolved -Recurse -Force
}
