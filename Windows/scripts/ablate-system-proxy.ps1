$ErrorActionPreference = 'Stop'
. "$PSScriptRoot\toolchain.ps1"
$clientRoot = Split-Path $PSScriptRoot -Parent
$repoRoot = Split-Path $clientRoot -Parent
$source = Join-Path $repoRoot 'core'
$experiment = Join-Path $clientRoot '.artifacts\ablation\system-proxy'
New-Item -ItemType Directory -Force -Path $experiment | Out-Null
$native = Join-Path $experiment 'core'
New-Item -ItemType Directory -Force -Path $native | Out-Null
Get-ChildItem -LiteralPath $source -Force | Copy-Item -Destination $native -Recurse -Force
$contracts = Join-Path $experiment 'contracts'
New-Item -ItemType Directory -Force -Path $contracts | Out-Null
Copy-Item -LiteralPath (Join-Path $repoRoot 'contracts\fixtures') -Destination $contracts -Recurse -Force
$variants = @(
    @{ Name = 'no-apply-rollback'; File = 'nativehost\proxy.go';
       Before = 'return errors.Join(err, e.Engine.SetBusinessGate(false))'; After = 'return errors.Join(err)';
       Test = 'TestProxyApplyFailureClosesForwarding'; Failure = 'left forwarding open' },
    @{ Name = 'no-eof-restoration'; File = 'nativehost\host.go';
       Before = 'err = errors.Join(err, h.restoreProxy(false))'; After = 'err = errors.Join(err)';
       Test = 'TestCloseRestoresOwnedProxyAndReportsFailure'; Failure = 'concealed failed restoration' },
    @{ Name = 'no-ownership-guard'; File = 'winproxy\manager.go';
       Before = 'if current != expected {'; After = 'if current != expected && false {';
       Test = 'TestRechecksAfterDurableSave'; Failure = 'missed competing writer' }
)
function Invoke-Suite([string]$Log, [string]$Pattern = '.') {
    [IO.File]::WriteAllText((Join-Path $experiment $Log), '')
    # Keep executables outside Go's immediately deleted temp directory; compile
    # first, then execute. Windows file scanners can hold newly linked files.
    foreach ($package in @('nativehost', 'winproxy')) {
        & go test -c -o (Join-Path $experiment "$package-check.exe") "./$package" 2>&1 | Out-File -FilePath (Join-Path $experiment $Log) -Append
        if ($LASTEXITCODE -ne 0) { throw 'Experiment compilation failed' }
    }
    $failed = $false
    foreach ($package in @('nativehost', 'winproxy')) {
        & (Join-Path $experiment "$package-check.exe") '-test.count=1' "-test.run=$Pattern" 2>&1 | Out-File -FilePath (Join-Path $experiment $Log) -Append
        if ($LASTEXITCODE -ne 0) { $failed = $true }
    }
    return !$failed
}
Push-Location $native
try {
    if (!(Invoke-Suite 'baseline.log')) { throw 'Baseline failed' }
    foreach ($variant in $variants) {
        $path = Join-Path $native $variant.File
        $bytes = [IO.File]::ReadAllBytes($path)
        $text = [Text.Encoding]::UTF8.GetString($bytes)
        if (!$text.Contains($variant.Before)) { throw 'Mutation target changed' }
        try {
            [IO.File]::WriteAllText($path, $text.Replace($variant.Before, $variant.After), [Text.UTF8Encoding]::new($false))
            $passed = Invoke-Suite "$($variant.Name).log" $variant.Test
            $output = Get-Content -Raw -LiteralPath (Join-Path $experiment "$($variant.Name).log")
            if ($passed -or $output -notmatch $variant.Failure -or $output -match 'build failed') {
                throw "Variant did not trigger the required behavioral failure: $($variant.Name)"
            }
        } finally { [IO.File]::WriteAllBytes($path, $bytes) }
    }
    if (!(Invoke-Suite 'restored.log')) { throw 'Restored suite failed' }
} finally { Pop-Location }
Write-Host 'Three isolated proxy ablations produced behavioral failures; restored suites passed.'
