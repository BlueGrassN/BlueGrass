param([string]$Device='emulator-5554',[ValidateSet('global','bypass_cn')][string[]]$Modes=@('global','bypass_cn'),[string]$ReportDirectory='')
$ErrorActionPreference='Stop'
$adb='E:\AndroidTools\sdk\platform-tools\adb.exe'
$ui=Join-Path $PSScriptRoot 'live-routing-ui.ps1'
$reportDir=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\.artifacts\routing-20261024'))
if ($ReportDirectory) { $reportDir=[IO.Path]::GetFullPath($ReportDirectory) }
$baseline=Get-Content (Join-Path $reportDir 'baseline.json') -Raw | ConvertFrom-Json
if (-not $baseline.https.ok -or $baseline.uid -ne 2000) { throw 'A valid independent physical baseline is required.' }
$physicalExit=$baseline.https.public_ipv4
$domesticBaseline=Get-Content (Join-Path $reportDir 'domestic-baseline.json') -Raw|ConvertFrom-Json
$domesticDomain=@($domesticBaseline.routing.china|Where-Object {$_.domain -eq 'myip.ipip.net' -and $_.path -eq 'domain' -and $_.ok})
if ($domesticBaseline.uid -ne 2000 -or $domesticDomain.Count -ne 1) { throw 'A physical domain baseline using the domestic resolver is required.' }
$results=@()
try {
  foreach ($mode in $Modes) {
    & $ui -Device $Device -Action mode -Mode $mode | Out-Null
    & $ui -Device $Device -Action connect | Out-Null
    $state=(& $ui -Device $Device -Action status) | ConvertFrom-Json
    $options=@($state.buttons | Where-Object label -in @('国内外分流','全局代理'))
    $label=if ($mode -eq 'bypass_cn') { '国内外分流' } else { '全局代理' }
    $selected=@($options | Where-Object { $_.label -eq $label -and $_.selected })
    $locked=($options.Count -eq 2 -and @($options | Where-Object enabled).Count -eq 0 -and $selected.Count -eq 1)
    if (-not $locked) { throw 'Active mode was not reflected and locked in UI.' }
    $raw=(& $adb -s $Device shell /data/local/tmp/bluegrass-network-probe --dns-server 172.19.0.2:53 --routing-check --routing-ip myip.ipip.net=117.149.203.79 --routing-ip stun.miwifi.com=111.206.174.2 --routing-ip stun.qq.com=101.35.50.98) -join ''
    [IO.File]::WriteAllText((Join-Path $reportDir "$mode.json"),$raw)
    $probe=$raw|ConvertFrom-Json
    $observation=(& $ui -Device $Device -Action observe) | ConvertFrom-Json
    if ($observation.observedIpv4.Count -ne 1) { throw 'Selected exit observation unavailable.' }
    $selectedExit=$observation.observedIpv4[0]
    $udp=@($probe.stun|Where-Object ok)
    $china=@($probe.routing.china|Where-Object domain -in @('myip.ipip.net','stun.miwifi.com'))
    $expectedChina=if ($mode -eq 'bypass_cn') { $physicalExit } else { $selectedExit }
    # The domestic resolver selects a different CDN origin for ipip. Compare
    # that domain response with its physical baseline under the same resolver.
    $expectedDomainHttps=if ($mode -eq 'bypass_cn') { $domesticDomain[0].public_ipv4 } else { $selectedExit }
    $result=[ordered]@{
      mode=$mode; independent_probe_uid_2000=($probe.uid -eq 2000); active_selector_locked=$locked
      selected_exit_differs_from_physical=($selectedExit -ne $physicalExit)
      public_dns=$probe.dns.ok
      foreign_https_via_selected=($probe.https.ok -and $probe.https.public_ipv4 -eq $selectedExit)
      foreign_udp_via_selected=($udp.Count -gt 0 -and @($udp|Where-Object public_ipv4 -ne $selectedExit).Count -eq 0)
      china_domain_https=(@($china|Where-Object {$_.protocol -eq 'https' -and $_.path -eq 'domain' -and $_.ok -and $_.public_ipv4 -eq $expectedDomainHttps}).Count -eq 1)
      china_literal_https=(@($china|Where-Object {$_.protocol -eq 'https' -and $_.path -eq 'literal_ip' -and $_.ok -and $_.public_ipv4 -eq $expectedChina}).Count -eq 1)
      china_domain_udp=(@($china|Where-Object {$_.protocol -eq 'stun' -and $_.path -eq 'domain' -and $_.ok -and $_.public_ipv4 -eq $expectedChina}).Count -eq 1)
      china_literal_udp=(@($china|Where-Object {$_.protocol -eq 'stun' -and $_.path -eq 'literal_ip' -and $_.ok -and $_.public_ipv4 -eq $expectedChina}).Count -eq 1)
    }
    $results+=,[pscustomobject]$result
    $results|ConvertTo-Json -Depth 4|Set-Content (Join-Path $reportDir 'routing-summary.json') -Encoding utf8
    $result|ConvertTo-Json -Compress
    if (@($result.Keys | Where-Object { $_ -ne 'mode' -and -not $result[$_] }).Count) { throw 'Actual routing did not match the selected policy.' }
    & $ui -Device $Device -Action disconnect | Out-Null
  }
} finally {
  & (Join-Path $PSScriptRoot 'live-ui.ps1') -Device $Device -Action tap -Label '首页' | Out-Null
  $state=(& (Join-Path $PSScriptRoot 'live-ui.ps1') -Device $Device -Action status) | ConvertFrom-Json
  if (-not $state.idle -and -not $state.loginVisible) { & $ui -Device $Device -Action disconnect | Out-Null }
}
