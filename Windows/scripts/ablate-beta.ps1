$ErrorActionPreference = 'Stop'
. "$PSScriptRoot\toolchain.ps1"
$clientRoot = Split-Path $PSScriptRoot -Parent
$repoRoot = Split-Path $clientRoot -Parent
$source = Join-Path $repoRoot 'core'
$experiment = Join-Path $clientRoot ('.artifacts\ablation\beta-' + (Get-Date -Format 'yyyyMMdd-HHmmss'))
New-Item -ItemType Directory -Force -Path $experiment | Out-Null
$copy = Join-Path $experiment 'core'
Copy-Item -LiteralPath $source -Destination $copy -Recurse
$contracts = Join-Path $experiment 'contracts'
New-Item -ItemType Directory -Force -Path $contracts | Out-Null
Copy-Item -LiteralPath (Join-Path $repoRoot 'contracts\fixtures') -Destination $contracts -Recurse
$variants = @(
  @{Name='remove-parent-publisher-check'; File='serviceipc\identity.go';
    Before=' || p.ParentSigner != trustedSigner'; After='';
    Package='serviceipc'; Test='TestPeerRequiresInstalledSignedBrokerAndMatchingInteractiveParent'; Expected='untrusted caller accepted'},
  @{Name='release-tunnel-on-host-close'; File='nativehost\host.go';
    Before='func (h *Host) Close() error {'; After="func (h *Host) Close() error {`nif h.tunnel != nil { _ = h.tunnel.Release() }";
    Package='nativehost'; Test='TestRecoveredTunnelReportsProtectionUntilExplicitRelease'; Expected='process close released persistent protection'},
  @{Name='allow-backward-server-clock'; File='clock\clock.go';
    Before='if c.confidence == Server && anchor.Before(c.anchor) {'; After='if false {';
    Package='clock'; Test='TestFirstServerCalibrationReplacesLocalEstimateAndNeverRewindsAfterward'; Expected='old server response extended expiry'}
)
function Invoke-Behavior([string]$Package,[string]$Pattern,[string]$Name) {
  $exe = Join-Path $experiment "$Name.exe"
  $log = Join-Path $experiment "$Name.log"
  & go test -c -o $exe "./$Package" *> $log
  if ($LASTEXITCODE -ne 0) { throw "Ablation compile failed: $Name" }
  & $exe '-test.count=1' "-test.run=$Pattern" *>> $log
  return $LASTEXITCODE
}
$results = @()
Push-Location $copy
try {
  foreach ($variant in $variants) {
    if ((Invoke-Behavior $variant.Package $variant.Test "$($variant.Name)-baseline") -ne 0) { throw 'Baseline behavior failed' }
    $path = Join-Path $copy $variant.File
    $bytes = [IO.File]::ReadAllBytes($path)
    $text = [Text.Encoding]::UTF8.GetString($bytes)
    if (!$text.Contains($variant.Before)) { throw "Mutation anchor changed: $($variant.Name)" }
    try {
      [IO.File]::WriteAllText($path,$text.Replace($variant.Before,$variant.After),[Text.UTF8Encoding]::new($false))
      $code = Invoke-Behavior $variant.Package $variant.Test $variant.Name
      $log = Get-Content -Raw -LiteralPath (Join-Path $experiment "$($variant.Name).log")
      if ($code -eq 0 -or !$log.Contains($variant.Expected)) { throw "Expected behavioral failure absent: $($variant.Name)" }
    } finally { [IO.File]::WriteAllBytes($path,$bytes) }
    if ((Invoke-Behavior $variant.Package $variant.Test "$($variant.Name)-restored") -ne 0) { throw 'Restored behavior failed' }
    if ((Get-FileHash -LiteralPath $path).Hash -ne (Get-FileHash -LiteralPath (Join-Path $source $variant.File)).Hash) { throw 'Restored source differs' }
    $results += @{name=$variant.Name; baseline='pass'; ablated='behavioral failure'; restored='pass'; decision='retain protection'}
  }
} finally { Pop-Location }
$results | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $experiment 'results.json') -Encoding utf8
Write-Host "Three isolated Beta boundary ablations verified: $experiment"
