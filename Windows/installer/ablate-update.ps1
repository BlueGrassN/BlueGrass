param()
$ErrorActionPreference = 'Stop'
$clientRoot = Split-Path $PSScriptRoot -Parent
$repoRoot = Split-Path $clientRoot -Parent
$source = Join-Path $repoRoot 'core\update'
$temporary = Join-Path ([IO.Path]::GetTempPath()) ('BlueGrass-update-ablation-' + [Guid]::NewGuid().ToString('N'))
$go = 'D:\Program Files\Go\bin\go.exe'
$priorModule = $env:GO111MODULE
New-Item -ItemType Directory -Path $temporary | Out-Null
try {
    Get-ChildItem -LiteralPath $source -Filter '*.go' | ForEach-Object { Copy-Item -LiteralPath $_.FullName -Destination $temporary }
    $env:GO111MODULE = 'off'
    Push-Location $temporary
    try {
        & $go test -count=1 -run 'TestValidEncodingWrongManifestSignatureRejected|TestDownloadRejectsBadResponsesAndPublisher/hash' .
        if ($LASTEXITCODE -ne 0) { throw 'Ablation baseline is not green.' }
        $manifest = Join-Path $temporary 'manifest.go'
        $text = [IO.File]::ReadAllText($manifest)
        if (!$text.Contains('|| !ed25519.Verify(')) { throw 'Signature ablation target not found.' }
        [IO.File]::WriteAllText($manifest,$text.Replace('|| !ed25519.Verify(','|| false && !ed25519.Verify('))
        $output = (& $go test -count=1 -run '^TestValidEncodingWrongManifestSignatureRejected$' . 2>&1 | Out-String)
        if ($LASTEXITCODE -eq 0 -or !$output.Contains('wrong signing key accepted')) { throw 'Signature ablation was not detected by the expected assertion.' }
        Write-Output 'PASS: removing Ed25519 verification causes the wrong-key negative test to fail.'
        [IO.File]::WriteAllText($manifest,$text)
        $download = Join-Path $temporary 'download.go'
        $text = [IO.File]::ReadAllText($download)
        if (!$text.Contains('if hex.EncodeToString(hash.Sum(nil)) != candidate.pkg.SHA256 {')) { throw 'Hash ablation target not found.' }
        [IO.File]::WriteAllText($download,$text.Replace('if hex.EncodeToString(hash.Sum(nil)) != candidate.pkg.SHA256 {','if false && hex.EncodeToString(hash.Sum(nil)) != candidate.pkg.SHA256 {'))
        $output = (& $go test -count=1 -run '^TestDownloadRejectsBadResponsesAndPublisher$/^hash$' . 2>&1 | Out-String)
        if ($LASTEXITCODE -eq 0 -or !$output.Contains('invalid artifact became ready')) { throw 'Hash ablation was not detected by the expected assertion.' }
        Write-Output 'PASS: removing SHA-256 verification causes the corrupted-package negative test to fail.'
    } finally { Pop-Location }
} finally {
    $env:GO111MODULE = $priorModule
    $resolved = [IO.Path]::GetFullPath($temporary)
    $temp = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('\') + '\'
    if (!$resolved.StartsWith($temp,[StringComparison]::OrdinalIgnoreCase) -or [IO.Path]::GetFileName($resolved) -notlike 'BlueGrass-update-ablation-*') { throw 'Unsafe ablation cleanup path.' }
    Remove-Item -LiteralPath $resolved -Recurse -Force
}
