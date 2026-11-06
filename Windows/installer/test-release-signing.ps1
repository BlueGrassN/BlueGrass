param()
$ErrorActionPreference = 'Stop'
$clientRoot = Split-Path $PSScriptRoot -Parent
$builder = Join-Path $clientRoot 'scripts\build-installer.ps1'
$missingBundle = Join-Path $clientRoot '.artifacts\no-such-release-input'
function Expect-Rejection([string]$Expected, [scriptblock]$Action) {
    try { & $Action; throw 'Expected rejection did not happen.' }
    catch { if ($_.Exception.Message -notlike "*$Expected*") { throw } }
}
$base = @{ Bundle=$missingBundle; Version='0.1.0'; Build=1; ValidateOnly=$true }
Expect-Rejection 'Candidate mode cannot accept signing parameters' {
    & $builder @base -UnsignedCandidate -CertificateThumbprint ('0' * 40)
}
Expect-Rejection 'CertificateThumbprint must be exactly 40 hexadecimal characters' {
    & $builder @base -CertificateThumbprint 'bad /sha1' -SignTool 'C:\signtool.exe' -TimestampUrl 'https://timestamp.example.test/'
}
Expect-Rejection 'Signing requires CertificateThumbprint, SignTool and TimestampUrl together' {
    & $builder @base -CertificateThumbprint ('0' * 40)
}
foreach ($url in @('http://timestamp.example.test/', 'https://user@timestamp.example.test/', 'https://timestamp.example.test/?token=secret', 'https://timestamp.example.test/#extra', 'https://timestamp.example.test/" /p password', 'https://timestamp.example.test/$f')) {
    Expect-Rejection 'TimestampUrl must be a plain HTTPS endpoint' {
        & $builder @base -CertificateThumbprint ('0' * 40) -SignTool 'C:\signtool.exe' -TimestampUrl $url
    }
}
Expect-Rejection 'SignTool must be an absolute local signtool.exe path' {
    & $builder @base -CertificateThumbprint ('0' * 40) -SignTool 'signtool.exe' -TimestampUrl 'https://timestamp.example.test/'
}
Write-Output 'PASS: signing options reject candidate mixing, malformed thumbprints, partial input, unsafe timestamps and PATH lookup before touching bundle or signing.'
. (Join-Path $PSScriptRoot 'signing.ps1')
$signtool = 'C:\Program Files (x86)\Windows Kits\10\bin\10.0.26100.0\x64\signtool.exe'
Expect-Rejection 'Exactly one existing release certificate' {
    Get-ReleaseSigningIdentity -Thumbprint ('0' * 40) -SignTool $signtool
}
$fixture = Join-Path $clientRoot '.artifacts\innosetup-7.1.0-x64.exe'
$signature = Get-AuthenticodeSignature -LiteralPath $fixture
if ($signature.Status -ne 'Valid') { throw 'Expected previously verified signed compiler fixture.' }
Expect-Rejection 'existing private key' { Assert-CodeSigningCertificate -Certificate $signature.SignerCertificate }
$verifiedIdentity = [PSCustomObject]@{
    SignTool=$signtool; Thumbprint=$signature.SignerCertificate.Thumbprint
    PublisherSHA256=[Convert]::ToHexString([Security.Cryptography.SHA256]::HashData($signature.SignerCertificate.RawData)).ToLowerInvariant()
}
Assert-SignedReleaseArtifact -Path $fixture -Identity $verifiedIdentity
$verifiedIdentity.PublisherSHA256 = '0' * 64
Expect-Rejection 'publisher mismatch' { Assert-SignedReleaseArtifact -Path $fixture -Identity $verifiedIdentity }
$identity = [PSCustomObject]@{ Thumbprint=('A' * 40); StoreLocation='LocalMachine' }
$arguments = @(Get-ReleaseSignArguments $identity 'https://timestamp.example.test/' 'E:\build dir\setup.exe')
$expected = @('sign','/q','/s','My','/sha1',('A' * 40),'/fd','SHA256','/tr','https://timestamp.example.test/','/td','SHA256','/sm','E:\build dir\setup.exe')
if (Compare-Object $arguments $expected -SyncWindow 0) { throw 'SignTool arguments lost certificate binding, SHA256, timestamp, store scope or target boundary.' }
$callback = Get-InnoSigningCommand 'C:\Program Files\PowerShell\7\pwsh.exe' 'E:\build $q\sign.ps1' 'E:\build $f\plan.json'
if (!$callback.Contains('E:\build $$q\sign.ps1') -or !$callback.Contains('E:\build $$f\plan.json') -or !$callback.EndsWith('-Target $f')) { throw 'Signing callback allowed Inno macro substitution into paths.' }
$temporary = Join-Path ([IO.Path]::GetTempPath()) ('BlueGrass-signing-tests-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $temporary | Out-Null
try {
    $root = Join-Path $temporary 'build'
    New-Item -ItemType Directory -Path $root | Out-Null
    $file = Join-Path $root 'unsigned.exe'
    [IO.File]::WriteAllText($file, 'not a signed executable')
    $innoTemporary = Join-Path $root 'uninst.e32.tmp'
    Copy-Item -LiteralPath $fixture -Destination $innoTemporary
    $verifiedIdentity.PublisherSHA256 = [Convert]::ToHexString([Security.Cryptography.SHA256]::HashData($signature.SignerCertificate.RawData)).ToLowerInvariant()
    Assert-SignedReleaseArtifact -Path $innoTemporary -Identity $verifiedIdentity
    Assert-SigningTarget -Root $root -Target $file
    Expect-Rejection 'escapes' { Assert-SigningTarget -Root $root -Target (Join-Path $temporary 'elsewhere.exe') }
    Expect-Rejection 'escapes' { Assert-SigningTarget -Root $root -Target ($file + ':stream') }
    $identity | Add-Member -NotePropertyName SignTool -NotePropertyValue $signtool
    $identity | Add-Member -NotePropertyName PublisherSHA256 -NotePropertyValue ('a' * 64)
    Expect-Rejection 'signature/timestamp verification failed' { Assert-SignedReleaseArtifact -Path $file -Identity $identity }
    $include = Join-Path $root 'payload.iss'
    [IO.File]::WriteAllText($include, '[Files]')
    $compiler = Join-Path $clientRoot '.artifacts\inno-7.1.0\ISCC.exe'
    $compiled = @(& $compiler '--preprocess' '--no-ide-signtools' "/DStageDir=$root" "/DPayloadInclude=$include" "/DOutputDir=$root" '/DAppVersion=0.1.0' '/DVersionResource=0.1.0.1' '/DUnsignedCandidate=0' ('/DPublisherSHA256=' + ('a' * 64)) (Join-Path $PSScriptRoot 'BlueGrass.iss'))
    if ($LASTEXITCODE -ne 0) { throw 'Production Inno preprocessing failed.' }
    if (!($compiled -match '^SignedUninstaller=yes$') -or !($compiled -match '^SignTool=bluegrass$') -or ($compiled -match '^OutputBaseFilename=.*unsigned-candidate')) { throw 'Production Inno branch does not require signing setup and uninstaller.' }
    $candidate = @(& $compiler '--preprocess' '--no-ide-signtools' "/DStageDir=$root" "/DPayloadInclude=$include" "/DOutputDir=$root" '/DAppVersion=0.1.0' '/DVersionResource=0.1.0.1' '/DUnsignedCandidate=1' '/DPublisherSHA256=unavailable' (Join-Path $PSScriptRoot 'BlueGrass.iss'))
    if ($LASTEXITCODE -ne 0 -or ($candidate -match '^SignTool=') -or !($candidate -match 'This unsigned compile candidate cannot install BlueGrass')) { throw 'Candidate branch lost its unconditional install refusal.' }
    Write-Output 'PASS: missing/private-key certificate, exact SHA256/RFC3161/store arguments, callback escaping, target confinement, unsigned verification and production Inno branch.'
} finally {
    $resolved = [IO.Path]::GetFullPath($temporary)
    $tempRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('\') + '\'
    if (!$resolved.StartsWith($tempRoot,[StringComparison]::OrdinalIgnoreCase) -or [IO.Path]::GetFileName($resolved) -notlike 'BlueGrass-signing-tests-*') { throw 'Unsafe temporary signing-test cleanup path.' }
    Remove-Item -LiteralPath $resolved -Recurse -Force
}
