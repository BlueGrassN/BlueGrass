param([Parameter(Mandatory)][string]$ActualArtifactDirectory)
$ErrorActionPreference = 'Stop'
$clientRoot = Split-Path $PSScriptRoot -Parent
. (Join-Path $PSScriptRoot 'signing.ps1')
$actual = (Resolve-Path -LiteralPath $ActualArtifactDirectory).Path
$inventory = Get-Content -Raw -LiteralPath (Join-Path $actual 'payload-inventory.json') | ConvertFrom-Json
$origins = Get-Content -Raw -LiteralPath (Join-Path $actual 'actual-build-origins.json') | ConvertFrom-Json
if (!$inventory.CandidateOnly -or !$origins.CompileEvidenceOnly) { throw 'This rejection test requires an actual-binary compile-evidence snapshot.' }
foreach ($record in $inventory.Files) {
    if ((Get-FileHash -LiteralPath (Join-Path $actual ('payload\' + $record.Path)) -Algorithm SHA256).Hash.ToLowerInvariant() -cne $record.SHA256) { throw 'Actual payload evidence changed.' }
}
$output = Join-Path $clientRoot ('.artifacts\signing-compiler-rejection\' + [Guid]::NewGuid().ToString('N'))
$temp = Join-Path $output 'temp'
foreach ($dir in @($output,$temp,(Join-Path $output 'signing-receipts'),(Join-Path $output 'signed-uninstaller'))) { New-Item -ItemType Directory -Path $dir -Force | Out-Null }
$plan = Join-Path $output 'signing-plan.json'
$signtool = 'C:\Program Files (x86)\Windows Kits\10\bin\10.0.26100.0\x64\signtool.exe'
# The deliberately absent selector makes the REAL callback abort before signing.
# This is a compiler/callback handshake test, never a release or install artifact.
@{ Schema=1; ArtifactRoot=$output; Setup=(Join-Path $output 'BlueGrass-0.1.0-Windows-x64.exe'); ReceiptDirectory=(Join-Path $output 'signing-receipts'); Thumbprint=('0' * 40); StoreLocation='CurrentUser'; PublisherSHA256=('a' * 64); SignTool=$signtool; TimestampUrl='https://timestamp.example.test/' } |
    ConvertTo-Json | Set-Content -LiteralPath $plan -Encoding utf8
$command = Get-InnoSigningCommand (Join-Path $PSHOME 'pwsh.exe') (Join-Path $PSScriptRoot 'sign-artifact.ps1') $plan
$oldTemp = $env:TEMP
$oldTmp = $env:TMP
try {
    $env:TEMP=$temp; $env:TMP=$temp
    $log = @(& (Join-Path $clientRoot '.artifacts\inno-7.1.0\ISCC.exe') '--no-ide-signtools' '--no-compression' ("--signtool=bluegrass=" + $command) "/DStageDir=$actual\payload" "/DPayloadInclude=$actual\payload.iss" "/DOutputDir=$output" '/DAppVersion=0.1.0' '/DVersionResource=0.1.0.1' '/DUnsignedCandidate=0' ('/DPublisherSHA256=' + ('a' * 64)) (Join-Path $PSScriptRoot 'BlueGrass.iss') 2>&1)
    $exitCode = $LASTEXITCODE
} finally { $env:TEMP=$oldTemp; $env:TMP=$oldTmp }
$log | Set-Content -LiteralPath (Join-Path $output 'compiler-rejection.txt') -Encoding utf8
if ($exitCode -eq 0 -or !($log -match 'Exactly one existing release certificate') -or !($log -match 'Sign Tool command failed')) { throw "Production callback failed for an unexpected reason; inspect $output" }
if (@(Get-ChildItem -LiteralPath (Join-Path $output 'signing-receipts') -File).Count -ne 0) { throw 'Unexpected successful signing receipt.' }
@{ Distributable=$false; ExpectedCompilerFailure=$true; CertificateNotFound=$true; SignatureOperations=0; ActualSource=$actual } | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $output 'verification.json') -Encoding utf8
Write-Output "PASS: real production Inno callback confined its generated uninstaller, reached missing-certificate gate and aborted before signing. Evidence: $output"
