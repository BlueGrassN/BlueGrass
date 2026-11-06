param()
$ErrorActionPreference = 'Stop'
$clientRoot = Split-Path $PSScriptRoot -Parent
$toolRoot = Join-Path $clientRoot '.artifacts\inno-7.1.0'
$download = Join-Path $clientRoot '.artifacts\innosetup-7.1.0-x64.exe'
$expected = '0362a383ed217d4c4239b5933866dd96d3eb2102737da92f80f6057a4b40df2f'
$url = 'https://github.com/jrsoftware/issrc/releases/download/is-7_1_0/innosetup-7.1.0-x64.exe'
# Official release asset digest, inspected 2026-10-12. No key/trust installation.
New-Item -ItemType Directory -Force -Path (Split-Path $download -Parent) | Out-Null
if (!(Test-Path -LiteralPath $download)) {
    $pending = $download + '.partial'
    try {
        Invoke-WebRequest -Uri $url -OutFile $pending -TimeoutSec 60
    } catch {
        # Same fixed official asset via API when github.com downloads fail.
        Invoke-WebRequest -Uri 'https://api.github.com/repos/jrsoftware/issrc/releases/assets/511336600?download=1' -Headers @{Accept='application/octet-stream'; 'User-Agent'='BlueGrass-build'} -OutFile $pending -TimeoutSec 120
    }
    if ((Get-FileHash -LiteralPath $pending -Algorithm SHA256).Hash.ToLowerInvariant() -ne $expected) { throw 'Compiler download hash mismatch.' }
    Move-Item -LiteralPath $pending -Destination $download
}
if ((Get-FileHash -LiteralPath $download -Algorithm SHA256).Hash.ToLowerInvariant() -ne $expected) { throw 'Compiler download hash mismatch.' }
$signature = Get-AuthenticodeSignature -LiteralPath $download
if ($signature.Status -ne 'Valid') { throw 'Official compiler Authenticode signature is not trusted.' }
if (!(Test-Path -LiteralPath (Join-Path $toolRoot 'ISCC.exe'))) {
    # Upstream isportable.iss disables uninstall registration; setup.iss skips
    # association/desktop tasks and shortcuts. /CURRENTUSER avoids elevation.
    $arguments = "/PORTABLE=1 /CURRENTUSER /VERYSILENT /SUPPRESSMSGBOXES /NORESTART /SP- /NOICONS /TASKS= `"/DIR=$toolRoot`""
    $process = Start-Process -FilePath $download -ArgumentList $arguments -WindowStyle Hidden -PassThru -Wait
    if ($process.ExitCode -ne 0) { throw "Portable compiler preparation failed ($($process.ExitCode))." }
}
$compiler = Join-Path $toolRoot 'ISCC.exe'
$compilerSignature = Get-AuthenticodeSignature -LiteralPath $compiler
if ($compilerSignature.Status -ne 'Valid') { throw 'Compiler binary signature is not trusted.' }
$archivePublisher = [Convert]::ToHexString([Security.Cryptography.SHA256]::HashData($signature.SignerCertificate.RawData))
$compilerPublisher = [Convert]::ToHexString([Security.Cryptography.SHA256]::HashData($compilerSignature.SignerCertificate.RawData))
if ($archivePublisher -ne $compilerPublisher) { throw 'Compiler publisher differs from the pinned official package.' }
Write-Output $compiler
