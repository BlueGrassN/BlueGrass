param([string]$Device='emulator-5554')
$ErrorActionPreference='Stop'
if ($Device -notmatch '^emulator-\d+$') { throw 'Designated emulator required.' }
$adb='E:\AndroidTools\sdk\platform-tools\adb.exe'
$ui=Join-Path $PSScriptRoot 'live-ui.ps1'
$reportDir=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\.artifacts\live-functional-20261024'))
$results=[ordered]@{}
function Status { (& $ui -Device $Device -Action status) | ConvertFrom-Json }
function Tap([string]$label) { & $ui -Device $Device -Action tap -Label $label | Out-Null }
function Wait-State([string]$name) {
  $deadline=[DateTime]::UtcNow.AddSeconds(45)
  do {
    $state=Status
    if ($state.$name -and -not $state.busy -and -not $state.pendingSwitch) { return $state }
    Start-Sleep -Milliseconds 300
  } while ([DateTime]::UtcNow -lt $deadline)
  throw "Timed out waiting for $name."
}
function Tun-Present {
  $addresses=(& $adb -s $Device shell ip -o addr show) -join "`n"
  return $addresses.Contains('172.19.0.1/30')
}
function Save-Result([string]$name,[bool]$ok) {
  $results[$name]=$ok
  $results | ConvertTo-Json | Set-Content (Join-Path $reportDir 'lifecycle-summary.json') -Encoding utf8
  "$name=$ok"
  if (-not $ok) { throw "Functional check failed: $name" }
}
function Probe([string]$name,[string]$dns='172.19.0.2:53') {
  $raw=(& $adb -s $Device shell /data/local/tmp/bluegrass-network-probe --dns-server $dns) -join ''
  [IO.File]::WriteAllText((Join-Path $reportDir "$name-probe.json"),$raw)
  $probe=$raw | ConvertFrom-Json
  if ($probe.uid -ne 2000) { throw 'Probe did not use the separate shell UID.' }
  return $probe
}
function Forwarding-OK($probe,[string]$expected) {
  $udp=@($probe.stun | Where-Object ok)
  return ($probe.dns.ok -and $probe.https.ok -and $udp.Count -gt 0 -and
    $probe.https.public_ipv4 -eq $expected -and
    @($udp | Where-Object public_ipv4 -ne $expected).Count -eq 0)
}
$wifi=((& $adb -s $Device shell settings get global wifi_on) -join '').Trim()
$mobile=((& $adb -s $Device shell settings get global mobile_data) -join '').Trim()
$networkChanged=$false
$expected=(Get-Content (Join-Path $reportDir 'node-01-fixed.json') -Raw | ConvertFrom-Json).https.public_ipv4
try {
  # Begin idle on Home with the first resource selected after a normal relaunch.
  if (-not (Status).connected) { Tap '连接' }
  Wait-State 'connected' | Out-Null
  & $adb -s $Device shell input keyevent KEYCODE_HOME | Out-Null
  $probe=Probe 'background'
  Save-Result 'background_forwarding' (Forwarding-OK $probe $expected)
  Save-Result 'background_tun_retained' (Tun-Present)
  & $adb -s $Device shell am start -n com.bluegrass.client/.MainActivity | Out-Null
  Wait-State 'connected' | Out-Null

  $before=(& $adb -s $Device shell dumpsys activity activities) -join "`n"
  $beforeId=[regex]::Match($before,'ActivityRecord\{(\S+)[^\r\n]*com\.bluegrass\.client/\.MainActivity').Groups[1].Value
  $beforePid=(& $adb -s $Device shell pidof com.bluegrass.client) -join ''
  # NEW_TASK | CLEAR_TASK replaces this app's activity without stopping its service.
  & $adb -s $Device shell am start -n com.bluegrass.client/.MainActivity -f 0x10008000 | Out-Null
  Start-Sleep -Seconds 2
  $after=(& $adb -s $Device shell dumpsys activity activities) -join "`n"
  $afterId=[regex]::Match($after,'ActivityRecord\{(\S+)[^\r\n]*com\.bluegrass\.client/\.MainActivity').Groups[1].Value
  $afterPid=(& $adb -s $Device shell pidof com.bluegrass.client) -join ''
  Save-Result 'activity_replaced' ($beforeId -ne '' -and $afterId -ne '' -and $beforeId -ne $afterId)
  Save-Result 'activity_recreation_process_retained' ($beforePid -ne '' -and $beforePid -eq $afterPid)
  Save-Result 'activity_recreation_tun_retained' (Tun-Present)
  Wait-State 'connected' | Out-Null
  $probe=Probe 'activity-recreated'
  Save-Result 'activity_recreated_forwarding' (Forwarding-OK $probe $expected)

  Tap '刷新资源'
  Wait-State 'connected' | Out-Null
  Tap '诊断'
  Tap '开始诊断'
  Wait-State 'connected' | Out-Null
  Save-Result 'refresh_and_diagnostics_keep_vpn' (Tun-Present)
  Tap '首页'

  $networkChanged=$true
  & $adb -s $Device shell svc wifi disable | Out-Null
  & $adb -s $Device shell svc data disable | Out-Null
  Wait-State 'paused' | Out-Null
  Save-Result 'network_loss_paused_tun_retained' (Tun-Present)
  $probe=Probe 'network-offline'
  Save-Result 'network_loss_no_forwarding' (-not $probe.https.ok -and @($probe.stun | Where-Object ok).Count -eq 0)
  if ($wifi -eq '1') { & $adb -s $Device shell svc wifi enable | Out-Null }
  if ($mobile -eq '1') { & $adb -s $Device shell svc data enable | Out-Null }
  $networkChanged=$false
  Wait-State 'connected' | Out-Null
  $probe=Probe 'network-recovered'
  Save-Result 'network_recovery_forwarding' (Forwarding-OK $probe $expected)

  Tap '设置'
  for ($i=0; $i -lt 5; $i++) {
    $state=Status
    if (@($state.buttons | Where-Object { $_.label -eq '退出账号' -and $_.enabled }).Count) { break }
    & $ui -Device $Device -Action scroll-down | Out-Null
  }
  Tap '退出账号'
  Wait-State 'loginVisible' | Out-Null
  Save-Result 'logout_releases_tun' (-not (Tun-Present))
  & $adb -s $Device shell am force-stop com.bluegrass.client | Out-Null
  & $adb -s $Device shell am start -n com.bluegrass.client/.MainActivity | Out-Null
  Wait-State 'loginVisible' | Out-Null
  Save-Result 'logout_persists_after_restart' $true
  $probe=Probe 'final-baseline' '10.0.2.3:53'
  $udp=@($probe.stun | Where-Object ok)
  Save-Result 'logout_physical_network_restored' ($probe.dns.ok -and $probe.https.ok -and
    $probe.https.public_ipv4 -ne $expected -and $udp.Count -gt 0 -and
    @($udp | Where-Object public_ipv4 -ne $probe.https.public_ipv4).Count -eq 0)
} finally {
  if ($networkChanged) {
    if ($wifi -eq '1') { & $adb -s $Device shell svc wifi enable | Out-Null }
    if ($mobile -eq '1') { & $adb -s $Device shell svc data enable | Out-Null }
  }
  & $adb -s $Device shell am start -n com.bluegrass.client/.MainActivity | Out-Null
  $state=Status
  if (-not $state.loginVisible) {
    Tap '首页'
    $state=Status
    if (@($state.buttons | Where-Object { $_.label -eq '断开连接' -and $_.enabled }).Count) { Tap '断开连接' }
  }
}
