param(
    [string]$Bundle = '',
    [string]$Version = '',
    [Parameter(Mandatory)][uint32]$Build,
    [string]$Compiler = '',
    [string]$CertificateThumbprint = '',
    [ValidateSet('CurrentUser','LocalMachine')][string]$CertificateStore = 'CurrentUser',
    [string]$SignTool = '',
    [string]$TimestampUrl = '',
    [switch]$UnsignedCandidate,
    [switch]$ValidateOnly
)
$ErrorActionPreference = 'Stop'
$clientRoot = Split-Path $PSScriptRoot -Parent
$repoRoot = Split-Path $clientRoot -Parent
. "$PSScriptRoot/release-config.ps1"
$release = Get-BlueGrassRelease -Version $Version -Build $Build
$Version = $release.Version
. (Join-Path $clientRoot 'installer\signing.ps1')
$hasSigning = Assert-SigningArguments -Candidate $UnsignedCandidate.IsPresent -Thumbprint $CertificateThumbprint -SignTool $SignTool -TimestampUrl $TimestampUrl
$numericVersion = $Version
if (@($numericVersion.Split('.') | Where-Object { [int]$_ -gt 65535 }).Count -gt 0 -or $Build -eq 0 -or $Build -gt 65535) { throw 'Version must fit Windows version resources, with build 1..65535.' }
if (!$UnsignedCandidate -and !$ValidateOnly -and !$hasSigning) { throw 'Production compilation requires CertificateThumbprint, SignTool and TimestampUrl with an existing release certificate.' }
if (!$Bundle) { $Bundle = Join-Path $clientRoot 'app\build\windows\x64\runner\Release' }
$bundlePath = (Resolve-Path -LiteralPath $Bundle).Path
if ((Get-Item -LiteralPath $bundlePath).Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'Payload directory cannot be a reparse point.' }
. (Join-Path $clientRoot 'installer\payload.ps1')
# The official driver stays signed and pinned even in unsigned candidates.
& "$PSScriptRoot\prepare-wintun.ps1" -Destination $bundlePath -VerifyOnly
& "$PSScriptRoot\verify-native-dependencies.ps1" -BinaryDirectory $bundlePath
$required = $RequiredPayload
foreach ($name in $required) {
    if (!(Test-Path -LiteralPath (Join-Path $bundlePath $name) -PathType Leaf)) { throw "Required payload missing: $name" }
}
# Exact production payload inventory. Reject unexpected files, including old
# Flutter assets, test bindings, account caches and credentials; never copy '*'.
$allowed = @{};
foreach ($name in ($required + $OptionalPayload)) { $allowed[$name] = $true }
$files = @()
foreach ($item in Get-ChildItem -LiteralPath $bundlePath -Recurse -Force) {
    if ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'Payload contains a reparse point.' }
    if ($item.PSIsContainer) { continue }
    $relative = [IO.Path]::GetRelativePath($bundlePath, $item.FullName)
    if (!$allowed.ContainsKey($relative)) { throw "Unapproved payload: $relative" }
    $files += [PSCustomObject]@{ Path=$item.FullName; Relative=$relative; SHA256=(Get-FileHash -LiteralPath $item.FullName -Algorithm SHA256).Hash.ToLowerInvariant(); Size=$item.Length }
}
$publisher = ''
if (!$UnsignedCandidate) {
    foreach ($item in $files | Where-Object { [IO.Path]::GetExtension($_.Path) -in @('.exe','.dll') }) {
        $sig = Get-AuthenticodeSignature -LiteralPath $item.Path
        if ($sig.Status -ne 'Valid' -or !$sig.SignerCertificate) { throw "Unsigned or untrusted payload: $($item.Relative)" }
        if ($item.Relative -like 'BlueGrass*.exe') {
            $fingerprint = [Convert]::ToHexString([Security.Cryptography.SHA256]::HashData($sig.SignerCertificate.RawData)).ToLowerInvariant()
            if (!$publisher) { $publisher=$fingerprint }
            elseif ($publisher -ne $fingerprint) { throw 'Product executable publisher certificates differ.' }
        }
    }
    if (!(Test-Path -LiteralPath (Join-Path $bundlePath 'BlueGrass.SetupTrust.exe'))) { throw 'Signed BlueGrass.SetupTrust.exe is required for release.' }
    if (!(Test-Path -LiteralPath (Join-Path $bundlePath 'BlueGrass.UserCleanup.exe'))) { throw 'Signed BlueGrass.UserCleanup.exe is required for release.' }
}
$identity = $null
if ($hasSigning) {
    $identity = Get-ReleaseSigningIdentity -Thumbprint $CertificateThumbprint -StoreLocation $CertificateStore -SignTool $SignTool
    if ($identity.PublisherSHA256 -cne $publisher) { throw 'Selected release certificate does not match the signed product payload.' }
    foreach ($item in $files | Where-Object { $_.Relative -like 'BlueGrass*.exe' }) {
        Assert-SignedReleaseArtifact -Path $item.Path -Identity $identity
    }
}
if ($ValidateOnly) {
    [PSCustomObject]@{ Valid=$true; Files=$files.Count; UnsignedCandidate=$UnsignedCandidate.IsPresent; SigningReady=($null -ne $identity); Distributable=$false }
    return
}
if (!$Compiler) { $Compiler=Join-Path $clientRoot '.artifacts\inno-7.1.0\ISCC.exe' }
if (!(Test-Path -LiteralPath $Compiler -PathType Leaf)) { throw 'Inno Setup compiler missing. Run Windows/installer/prepare-compiler.ps1.' }
if ((Get-AuthenticodeSignature -LiteralPath $Compiler).Status -ne 'Valid') { throw 'Compiler must have a valid Authenticode signature.' }
$output = Join-Path $clientRoot ('.artifacts\installers\' + $Version + '-' + $Build + '-' + [Guid]::NewGuid().ToString('N'))
$stage = Join-Path $output 'payload'
New-Item -ItemType Directory -Force -Path $stage | Out-Null
foreach ($item in $files) {
    $destination=Join-Path $stage $item.Relative
    New-Item -ItemType Directory -Force -Path (Split-Path $destination -Parent) | Out-Null
    Copy-Item -LiteralPath $item.Path -Destination $destination
    if ((Get-FileHash -LiteralPath $destination -Algorithm SHA256).Hash.ToLowerInvariant() -ne $item.SHA256) { throw 'Payload changed during staging.' }
}
if ($UnsignedCandidate) {
$go = 'D:\Program Files\Go\bin\go.exe'
Push-Location (Join-Path $repoRoot 'core')
try {
    & $go build -trimpath -ldflags '-s -w -H windowsgui' -o (Join-Path $stage 'BlueGrass.SetupTrust.exe') (Join-Path $clientRoot 'installer\trustcheck\main.go')
    if ($LASTEXITCODE -ne 0) { throw 'Installer trust helper build failed.' }
    & $go build -trimpath -ldflags '-s -w -H windowsgui' -o (Join-Path $stage 'BlueGrass.UserCleanup.exe') (Join-Path $clientRoot 'installer\usercleanup\main_windows.go') (Join-Path $clientRoot 'installer\usercleanup\cleanup.go')
    if ($LASTEXITCODE -ne 0) { throw 'User uninstall helper build failed.' }
} finally { Pop-Location }
$mt = Get-ChildItem 'C:\Program Files (x86)\Windows Kits\10\bin\*\x64\mt.exe' -ErrorAction SilentlyContinue | Sort-Object FullName | Select-Object -Last 1
if (!$mt) { throw 'Windows SDK mt.exe is required for the asInvoker user-phase manifest.' }
& $mt.FullName '-nologo' '-manifest' (Join-Path $clientRoot 'installer\usercleanup\usercleanup.manifest') ("-outputresource:" + (Join-Path $stage 'BlueGrass.UserCleanup.exe') + ';#1')
if ($LASTEXITCODE -ne 0) { throw 'User-phase manifest embedding failed.' }
}
# Production consumes the explicitly pre-signed helpers without rebuilding or
# altering their PE resources. Vendor files likewise retain their own signatures.
if ($identity) {
    foreach ($item in $files | Where-Object { $_.Relative -like 'BlueGrass*.exe' }) {
        Assert-SignedReleaseArtifact -Path (Join-Path $stage $item.Relative) -Identity $identity
    }
}
$stageFiles = Get-ChildItem -LiteralPath $stage -File -Recurse | Sort-Object FullName
$lines = @('[Files]')
$inventory = @()
foreach ($file in $stageFiles) {
    $relative = [IO.Path]::GetRelativePath($stage,$file.FullName)
    $subdir = [IO.Path]::GetDirectoryName($relative)
    $dest = if ($subdir) { '{app}\' + $subdir } else { '{app}' }
    $lines += 'Source: "{#StageDir}\' + $relative + '"; DestDir: "' + $dest + '"; Flags: ignoreversion'
    $inventory += [PSCustomObject]@{ Path=$relative; Size=$file.Length; SHA256=(Get-FileHash -LiteralPath $file.FullName -Algorithm SHA256).Hash.ToLowerInvariant() }
}
$include = Join-Path $output 'payload.iss'
[IO.File]::WriteAllLines($include,$lines,[Text.UTF8Encoding]::new($false))
$suffix = if ($UnsignedCandidate) { '-unsigned-candidate' } else { '' }
$setup = Join-Path $output "BlueGrass-$Version-Windows-x64$suffix.exe"
$compilerArgs = @('--quiet-progress','--no-ide-signtools',"/DStageDir=$stage","/DPayloadInclude=$include","/DOutputDir=$output","/DAppVersion=$Version","/DVersionResource=$numericVersion.$Build")
if ($UnsignedCandidate) {
    $compilerArgs += @('--no-signing','/DUnsignedCandidate=1','/DPublisherSHA256=unavailable')
} else {
    $receipts = Join-Path $output 'signing-receipts'
    $signingTemp = Join-Path $output 'signing-temp'
    foreach ($dir in @($receipts,$signingTemp,(Join-Path $output 'signed-uninstaller'))) { New-Item -ItemType Directory -Path $dir | Out-Null }
    $plan = Join-Path $output 'signing-plan.json'
    @{ Schema=1; ArtifactRoot=$output; Setup=$setup; ReceiptDirectory=$receipts; Thumbprint=$identity.Thumbprint; StoreLocation=$identity.StoreLocation; PublisherSHA256=$publisher; SignTool=$identity.SignTool; TimestampUrl=$TimestampUrl } |
        ConvertTo-Json | Set-Content -LiteralPath $plan -Encoding utf8
    $command = Get-InnoSigningCommand -PowerShell (Join-Path $PSHOME 'pwsh.exe') -Script (Join-Path $clientRoot 'installer\sign-artifact.ps1') -Plan $plan
    $compilerArgs += @('/DUnsignedCandidate=0',"/DPublisherSHA256=$publisher",("--signtool=bluegrass=" + $command))
}
$compilerArgs += Join-Path $clientRoot 'installer\BlueGrass.iss'
$oldTemp = $env:TEMP
$oldTmp = $env:TMP
try {
    if (!$UnsignedCandidate) { $env:TEMP=$signingTemp; $env:TMP=$signingTemp }
    & $Compiler @compilerArgs
    if ($LASTEXITCODE -ne 0) { throw 'Installer compile/signing failed; outputs are not release-ready.' }
} finally { $env:TEMP=$oldTemp; $env:TMP=$oldTmp }
if ($identity) {
    Assert-SignedReleaseArtifact -Path $setup -Identity $identity
    $signed = @(Get-ChildItem -LiteralPath $receipts -Filter '*.json' | ForEach-Object { Get-Content -Raw -LiteralPath $_.FullName | ConvertFrom-Json })
    $setupHash = (Get-FileHash -LiteralPath $setup -Algorithm SHA256).Hash.ToLowerInvariant()
    # Bind Setup by bytes, because Inno may sign a temporary file then rename it.
    if (@($signed | Where-Object SHA256 -eq $setupHash).Count -lt 1 -or @($signed | Where-Object Kind -eq 'uninstaller').Count -lt 1) { throw 'Missing verified Setup or signed uninstaller evidence.' }
    foreach ($record in $signed) {
        Assert-SigningTarget -Root $output -Target $record.Snapshot
        Assert-SignedReleaseArtifact -Path $record.Snapshot -Identity $identity
        if (!$record.TimestampVerified -or (Get-FileHash -LiteralPath $record.Snapshot -Algorithm SHA256).Hash.ToLowerInvariant() -cne $record.SHA256) { throw 'Signed artifact evidence changed.' }
    }
}
# Independent repair download: its own executable, not a wrapper around service.
$repair = Join-Path $output "BlueGrass-Repair$suffix.exe"
Copy-Item -LiteralPath (Join-Path $stage 'BlueGrass.Repair.exe') -Destination $repair
foreach ($file in @($setup,$repair)) { if (!(Test-Path -LiteralPath $file)) { throw 'Expected installer artifact missing.' } }
if ($identity) { Assert-SignedReleaseArtifact -Path $repair -Identity $identity }
# Signing establishes artifact identity, not completion of the external Beta
# acceptance matrix. Keep that release decision explicit even for signed output.
@{ Version=$Version; Build=$Build; Distributable=$false; ReleaseAcceptanceRequired=$true; CandidateOnly=$UnsignedCandidate.IsPresent; SigningVerified=($null -ne $identity); PublisherSHA256=$publisher; Files=$inventory } | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath (Join-Path $output 'payload-inventory.json') -Encoding utf8
Write-Output ([PSCustomObject]@{ Setup=$setup; Repair=$repair; Inventory=(Join-Path $output 'payload-inventory.json'); Distributable=$false; ReleaseAcceptanceRequired=$true; SigningVerified=($null -ne $identity); InstallDisabled=$UnsignedCandidate.IsPresent; SHA256=(Get-FileHash -LiteralPath $setup -Algorithm SHA256).Hash.ToLowerInvariant() })
