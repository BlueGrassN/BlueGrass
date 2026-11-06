param([string]$Device='emulator-5554',[int]$Count=6,[int]$StartOrdinal=1)
$ErrorActionPreference='Stop'
$ui=Join-Path $PSScriptRoot 'live-ui.ps1'
$adb='E:\AndroidTools\sdk\platform-tools\adb.exe'
$reportDir=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\.artifacts\live-functional-20261024'))
New-Item -ItemType Directory -Force $reportDir | Out-Null
function Status { (& $ui -Device $Device -Action status) | ConvertFrom-Json }
function Tap([string]$label) { & $ui -Device $Device -Action tap -Label $label | Out-Null }
function Wait-Connected {
  $deadline=[DateTime]::UtcNow.AddSeconds(45)
  do {
    $state=Status
    if ($state.connected -and -not $state.busy -and -not $state.pendingSwitch) { return $state }
    if ($state.paused -or $state.safeErrors.Count -gt 0) {
      $bad=@($state.safeErrors | Where-Object { $_ -ne '暂未获取' })
      if ($state.paused -or $bad.Count) { throw 'Connection did not become usable.' }
    }
    Start-Sleep -Milliseconds 300
  } while ([DateTime]::UtcNow -lt $deadline)
  throw 'Connection timed out.'
}
$results=@()
if ($StartOrdinal -gt 1) {
  $results=@(Get-Content (Join-Path $reportDir 'resources-summary.json') -Raw | ConvertFrom-Json | Where-Object ordinal -lt $StartOrdinal)
}
try {
for ($ordinal=$StartOrdinal; $ordinal -le $Count; $ordinal++) {
  $state=Status
  $label=if (@($state.buttons | Where-Object { $_.label -eq '切换并连接' -and $_.enabled }).Count) { '切换并连接' } else { '连接' }
  Tap $label
  Wait-Connected | Out-Null
  $raw=(& $adb -s $Device shell /data/local/tmp/bluegrass-network-probe --dns-server 172.19.0.2:53) -join ''
  $probe=$raw | ConvertFrom-Json
  [IO.File]::WriteAllText((Join-Path $reportDir ('node-{0:d2}-fixed.json' -f $ordinal)),$raw)
  Tap '检测出口'
  $deadline=[DateTime]::UtcNow.AddSeconds(25)
  do { $state=Status; if ($state.observedIpv4.Count) { break }; Start-Sleep -Milliseconds 300 } while ([DateTime]::UtcNow -lt $deadline)
  $udp=@($probe.stun | Where-Object ok)
  $result=[ordered]@{
    ordinal=$ordinal; connected=$state.connected; dns=$probe.dns.ok; https=$probe.https.ok
    udp=$udp.Count -gt 0
    app_exit_matches=($state.observedIpv4.Count -eq 1 -and $state.observedIpv4[0] -eq $probe.https.public_ipv4)
    udp_exit_matches=($udp.Count -gt 0 -and @($udp | Where-Object public_ipv4 -ne $probe.https.public_ipv4).Count -eq 0)
    udp_provider_passes=$udp.Count
  }
  $results+=,[pscustomobject]$result
  $result | ConvertTo-Json -Compress
  $results | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath (Join-Path $reportDir 'resources-summary.json') -Encoding utf8
  if ($ordinal -lt $Count) {
    Tap '我的 IP'
    & (Join-Path $PSScriptRoot 'next-live-resource.ps1') -Device $Device | Out-Null
    Tap '首页'
  }
}
} finally {
  $state=Status
  if (@($state.buttons | Where-Object { $_.label -eq '断开连接' -and $_.enabled }).Count) { Tap '断开连接' }
}
if (@($results | Where-Object { -not $_.dns -or -not $_.https -or -not $_.udp -or -not $_.app_exit_matches -or -not $_.udp_exit_matches }).Count) {
  throw 'Resource checks completed with forwarding failures; inspect resources-summary.json.'
}
'All designated resources forwarded DNS, HTTPS and UDP through matching exits.'
