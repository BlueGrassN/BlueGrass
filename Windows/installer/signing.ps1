# Release signing helpers. No certificate import/export, key generation or trust
# changes. Callers supply a certificate already installed in a read-only My store.
function Assert-SigningArguments {
    param([bool]$Candidate, [string]$Thumbprint, [string]$SignTool, [string]$TimestampUrl)
    $any = $Thumbprint -or $SignTool -or $TimestampUrl
    if ($Candidate -and $any) { throw 'Candidate mode cannot accept signing parameters.' }
    if (!$any) { return $false }
    if ($Thumbprint -and $Thumbprint -cnotmatch '^[a-fA-F0-9]{40}$') { throw 'CertificateThumbprint must be exactly 40 hexadecimal characters.' }
    if (!$Thumbprint -or !$SignTool -or !$TimestampUrl) { throw 'Signing requires CertificateThumbprint, SignTool and TimestampUrl together.' }
    # No credentials, query secrets, Inno macros or command-line metacharacters.
    $uri = $null
    if ($TimestampUrl -cnotmatch '^https://[a-zA-Z0-9.-]+(?::443)?(/[a-zA-Z0-9._~/-]*)?$' -or
        ![Uri]::TryCreate($TimestampUrl, [UriKind]::Absolute, [ref]$uri) -or !$uri.Host -or
        $uri.UserInfo -or $uri.Query -or $uri.Fragment) { throw 'TimestampUrl must be a plain HTTPS endpoint without credentials, query, fragment or signing macros.' }
    if ($SignTool -notmatch '^[a-zA-Z]:\\' -or [IO.Path]::GetFileName($SignTool) -ine 'signtool.exe' -or
        [IO.Path]::GetFullPath($SignTool) -ine $SignTool -or $SignTool.Contains('"') -or $SignTool.Contains("`n")) {
        throw 'SignTool must be an absolute local signtool.exe path.'
    }
    return $true
}

function Assert-CodeSigningCertificate {
    param([Security.Cryptography.X509Certificates.X509Certificate2]$Certificate)
    if (!$Certificate -or !$Certificate.HasPrivateKey) { throw 'Release certificate must have an existing private key.' }
    $now = [DateTime]::UtcNow
    if ($Certificate.NotBefore.ToUniversalTime() -gt $now -or $Certificate.NotAfter.ToUniversalTime() -le $now) { throw 'Release certificate is not currently valid.' }
    $eku = @($Certificate.Extensions | Where-Object { $_.Oid.Value -eq '2.5.29.37' })
    if ($eku.Count -ne 1 -or '1.3.6.1.5.5.7.3.3' -notin @($eku[0].EnhancedKeyUsages | ForEach-Object Value)) { throw 'Release certificate requires explicit Code Signing EKU.' }
    $chain = [Security.Cryptography.X509Certificates.X509Chain]::new()
    try {
        $chain.ChainPolicy.RevocationMode = [Security.Cryptography.X509Certificates.X509RevocationMode]::Online
        $chain.ChainPolicy.RevocationFlag = [Security.Cryptography.X509Certificates.X509RevocationFlag]::ExcludeRoot
        $chain.ChainPolicy.VerificationFlags = [Security.Cryptography.X509Certificates.X509VerificationFlags]::NoFlag
        $chain.ChainPolicy.UrlRetrievalTimeout = [TimeSpan]::FromSeconds(15)
        [void]$chain.ChainPolicy.ApplicationPolicy.Add([Security.Cryptography.Oid]::new('1.3.6.1.5.5.7.3.3'))
        if (!$chain.Build($Certificate)) { throw 'Release certificate chain/revocation validation failed.' }
    } finally { $chain.Dispose() }
}

function Get-ReleaseSigningIdentity {
    param([string]$Thumbprint, [ValidateSet('CurrentUser','LocalMachine')][string]$StoreLocation = 'CurrentUser', [string]$SignTool)
    if (!(Test-Path -LiteralPath $SignTool -PathType Leaf) -or
        (Get-Item -LiteralPath $SignTool).Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'SignTool executable missing or linked.' }
    $toolSignature = Get-AuthenticodeSignature -LiteralPath $SignTool
    if ($toolSignature.Status -ne 'Valid' -or !$toolSignature.SignerCertificate -or
        $toolSignature.SignerCertificate.Subject -notmatch '(^|,\s*)O=Microsoft Corporation(,|$)') { throw 'SignTool must have a trusted Microsoft Authenticode signature.' }
    $store = [Security.Cryptography.X509Certificates.X509Store]::new('My', [Security.Cryptography.X509Certificates.StoreLocation]::$StoreLocation)
    try {
        $store.Open([Security.Cryptography.X509Certificates.OpenFlags]::ReadOnly -bor [Security.Cryptography.X509Certificates.OpenFlags]::OpenExistingOnly)
        $matches = @($store.Certificates | Where-Object { $_.Thumbprint -ieq $Thumbprint })
        if ($matches.Count -ne 1) { throw 'Exactly one existing release certificate must match the thumbprint in the selected My store.' }
        $certificate = $matches[0]
        Assert-CodeSigningCertificate $certificate
        return [PSCustomObject]@{
            Thumbprint=$certificate.Thumbprint.ToUpperInvariant(); StoreLocation=$StoreLocation
            PublisherSHA256=[Convert]::ToHexString([Security.Cryptography.SHA256]::HashData($certificate.RawData)).ToLowerInvariant()
            SignTool=(Resolve-Path -LiteralPath $SignTool).Path
        }
    } finally { $store.Close(); $store.Dispose() }
}

function Get-ReleaseSignArguments {
    param($Identity, [string]$TimestampUrl, [string]$Target)
    $arguments = @('sign','/q','/s','My','/sha1',$Identity.Thumbprint,'/fd','SHA256','/tr',$TimestampUrl,'/td','SHA256')
    if ($Identity.StoreLocation -eq 'LocalMachine') { $arguments += '/sm' }
    $arguments += $Target
    return $arguments
}

function Assert-SignedReleaseArtifact {
    param([string]$Path, $Identity)
    $verification = @(& $Identity.SignTool 'verify' '/q' '/pa' '/all' '/tw' '/sha1' $Identity.Thumbprint $Path 2>&1)
    if ($LASTEXITCODE -ne 0) { throw 'Release artifact signature/timestamp verification failed.' }
    Write-Verbose ($verification -join [Environment]::NewLine)
    $signature = Get-AuthenticodeSignature -LiteralPath $Path
    if ($signature.Status -ne 'Valid' -or !$signature.SignerCertificate -or !$signature.TimeStamperCertificate) { throw 'Release artifact requires a trusted timestamped Authenticode signature.' }
    $publisher = [Convert]::ToHexString([Security.Cryptography.SHA256]::HashData($signature.SignerCertificate.RawData)).ToLowerInvariant()
    if ($publisher -cne $Identity.PublisherSHA256) { throw 'Release artifact publisher mismatch.' }
}

function Get-InnoSigningCommand {
    param([string]$PowerShell, [string]$Script, [string]$Plan)
    # Inno starts this executable directly. Its $f placeholder already supplies
    # a quoted filename; literal dollars in our paths must remain literal.
    foreach ($path in @($PowerShell,$Script,$Plan)) {
        if (![IO.Path]::IsPathFullyQualified($path) -or $path.Contains('"') -or $path.Contains("`n") -or $path.Contains("`r")) { throw 'Invalid signing callback path.' }
    }
    return '$q' + $PowerShell.Replace('$','$$') + '$q -NoLogo -NoProfile -NonInteractive -File $q' + $Script.Replace('$','$$') + '$q -Plan $q' + $Plan.Replace('$','$$') + '$q -Target $f'
}

function Assert-SigningTarget {
    param([string]$Root, [string]$Target)
    $rootPath = [IO.Path]::GetFullPath($Root).TrimEnd('\')
    $targetPath = [IO.Path]::GetFullPath($Target)
    if (![IO.Path]::IsPathFullyQualified($Target) -or $targetPath -ine $Target -or
        !$targetPath.StartsWith($rootPath + '\', [StringComparison]::OrdinalIgnoreCase) -or $Target.Substring(2).Contains(':')) { throw 'Signing target escapes the isolated build directory.' }
    $item = Get-Item -LiteralPath $targetPath -Force
    if ($item.PSIsContainer) { throw 'Signing target must be a file.' }
    while ($item) {
        if ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'Signing target cannot traverse reparse points.' }
        if ($item.FullName -ieq $rootPath) { break }
        $item = if ($item -is [IO.DirectoryInfo]) { $item.Parent } else { $item.Directory }
    }
}
