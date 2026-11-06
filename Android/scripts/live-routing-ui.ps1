param(
  [ValidateSet('mode','connect','disconnect','observe','status')][string]$Action='status',
  [ValidateSet('global','bypass_cn')][string]$Mode='global',
  [string]$Device='emulator-5554'
)
$ErrorActionPreference='Stop'
$ui=Join-Path $PSScriptRoot 'live-ui.ps1'
function Status { (& $ui -Device $Device -Action status) | ConvertFrom-Json }
function Tap([string]$label) { & $ui -Device $Device -Action tap -Label $label | Out-Null }
function Reveal([string]$label,[string]$direction='down') {
  for ($i=0; $i -lt 6; $i++) {
    $state=Status
    if (@($state.buttons | Where-Object label -eq $label).Count) { return $state }
    & $ui -Device $Device -Action "scroll-$direction" | Out-Null
  }
  throw 'Requested control did not become visible.'
}
if ($Action -eq 'mode') {
  Tap '设置'
  $label=if ($Mode -eq 'bypass_cn') { '国内外分流' } else { '全局代理' }
  $state=Reveal $label 'up'
  $option=@($state.buttons | Where-Object label -eq $label)[0]
  if (-not $option.enabled) { throw 'Routing changes are disabled while VPN is active.' }
  if (-not $option.selected) { Tap $label }
  $state=Status
  $option=@($state.buttons | Where-Object label -eq $label)[0]
  if (-not $option.selected) { throw 'Routing selection was not confirmed.' }
  $state | ConvertTo-Json -Depth 5 -Compress
} elseif ($Action -eq 'connect' -or $Action -eq 'disconnect') {
  Tap '首页'
  $label=if ($Action -eq 'connect') { '连接' } else { '断开连接' }
  Reveal $label 'up' | Out-Null
  Tap $label
  $deadline=[DateTime]::UtcNow.AddSeconds(45)
  do {
    $state=Status
    if (($Action -eq 'connect' -and $state.connected -and -not $state.busy) -or
        ($Action -eq 'disconnect' -and $state.idle)) {
      $state | ConvertTo-Json -Depth 5 -Compress
      return
    }
    Start-Sleep -Milliseconds 500
  } while ([DateTime]::UtcNow -lt $deadline)
  throw 'VPN state did not settle.'
} elseif ($Action -eq 'observe') {
  Tap '首页'
  Reveal '检测出口' | Out-Null
  Tap '检测出口'
  # The result is on the connection card above the mode selector; reveal it.
  & $ui -Device $Device -Action scroll-up | Out-Null
  $deadline=[DateTime]::UtcNow.AddSeconds(30)
  do {
    $state=Status
    if ($state.observedIpv4.Count) { $state | ConvertTo-Json -Depth 5 -Compress; return }
    Start-Sleep -Milliseconds 500
  } while ([DateTime]::UtcNow -lt $deadline)
  throw 'Exit observation did not complete.'
} else {
  Tap '设置'
  (Reveal '国内外分流' 'up') | ConvertTo-Json -Depth 5 -Compress
}
