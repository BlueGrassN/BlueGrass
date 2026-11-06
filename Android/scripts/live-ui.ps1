param(
  [ValidateSet('status','tap','allow-vpn','scroll-down','scroll-up')][string]$Action = 'status',
  [string]$Label,
  [int]$Index = 0,
  [string]$Device = 'emulator-5554'
)
$ErrorActionPreference='Stop'
$adb='E:\AndroidTools\sdk\platform-tools\adb.exe'
if ($Device -notmatch '^emulator-\d+$') { throw 'Designated emulator required.' }
$tree=$null
for ($attempt=0; $attempt -lt 3; $attempt++) {
  & $adb -s $Device shell uiautomator dump /sdcard/bg-live-ui.xml > $null 2>&1
  $raw=((& $adb -s $Device shell cat /sdcard/bg-live-ui.xml 2>$null) -join '')
  & $adb -s $Device shell rm -f /sdcard/bg-live-ui.xml > $null
  if ($raw.StartsWith('<?xml')) {
    [xml]$tree=$raw
    if ($tree.SelectSingleNode('//node')) { break }
  }
  Start-Sleep -Milliseconds 500
}
if (-not $tree -or -not $tree.SelectSingleNode('//node')) { throw 'UI unavailable.' }
$nodes=@($tree.SelectNodes('//node'))
if ($Action -eq 'tap' -or $Action -eq 'allow-vpn') {
  $matchesForTap = if ($Action -eq 'allow-vpn') {
    @($nodes | Where-Object { $_.package -eq 'com.android.vpndialogs' -and $_.'resource-id' -eq 'android:id/button1' -and $_.enabled -eq 'true' })
  } else {
    @($nodes | Where-Object { $_.clickable -eq 'true' -and $_.enabled -eq 'true' -and
      ($_.text -eq $Label -or $_.'content-desc' -eq $Label -or $_.'content-desc'.StartsWith($Label+"`n")) })
  }
  if ($Index -lt 0 -or $Index -ge $matchesForTap.Count) { throw 'Requested visible enabled control unavailable.' }
  $n=@($matchesForTap)[$Index]
  if ($n.bounds -notmatch '^\[(\d+),(\d+)\]\[(\d+),(\d+)\]$') { throw 'Invalid UI bounds.' }
  & $adb -s $Device shell input tap ([int](([int]$Matches[1]+[int]$Matches[3])/2)) ([int](([int]$Matches[2]+[int]$Matches[4])/2))
  'Tapped requested control.'
} elseif ($Action -like 'scroll-*') {
  $n=@($nodes | Where-Object scrollable -eq 'true') | Select-Object -First 1
  if (-not $n -or $n.bounds -notmatch '^\[(\d+),(\d+)\]\[(\d+),(\d+)\]$') { throw 'Scrollable content unavailable.' }
  $x=[int](([int]$Matches[1]+[int]$Matches[3])/2)
  $top=[int]$Matches[2]+80; $bottom=[int]$Matches[4]-80
  $middle=[int](($bottom+$top)/2)
  if ($Action -eq 'scroll-down') { & $adb -s $Device shell input swipe $x $bottom $x $middle 700 }
  else { & $adb -s $Device shell input swipe $x $top $x $middle 700 }
} else {
  $labels=@($nodes | ForEach-Object { $_.'content-desc'; $_.text } | Where-Object { $_ })
  $safeButtons=@('登录','连接','切换并连接','断开连接','重新连接','检测出口','选择资源','刷新资源','我的 IP','首页','设置','诊断','连接资源','全部资源','全部订阅','全部资产','下一页','加载更多','选择','已选择','退出账号','开始诊断','资源详情','关闭','国内外分流','全局代理','立即更新','正在更新…','读取状态中…')
  $buttons=@($nodes | Where-Object { $_.clickable -eq 'true' -or $_.'content-desc' -in @('国内外分流','全局代理') } | ForEach-Object {
    $title=($_.'content-desc' -split "`n")[0]; if (-not $title) { $title=$_.text }
    if ($title -in $safeButtons) { [pscustomobject]@{label=$title;enabled=$_.enabled -eq 'true';selected=($_.selected -eq 'true' -or $_.checked -eq 'true');bounds=$_.bounds} }
  })
  $text=$labels -join "`n"
  [pscustomobject]@{
    loginVisible=$text.Contains('欢迎回来')
    connected=$text.Contains('已连接'); paused=$text.Contains('已暂停'); idle=$text.Contains('未连接')
    busy=($labels -contains '连接中' -or $labels -contains '正在处理…')
    pendingSwitch=$text.Contains('待切换资源：')
    protocols=@([regex]::Matches($text,'\b(?:SOCKS5|VLESS|VMESS|TROJAN|SHADOWSOCKS|HY2|HYSTERIA2)\b') | ForEach-Object Value | Select-Object -Unique)
    counts=@($labels | Where-Object { $_ -match '^已加载 \d+ 项$' })
    safeErrors=@($labels | Where-Object { $_ -match '^(网络连接失败|服务暂不可用|资源同步未完成|请求较频繁|连接未完成|VPN 连接失败|VPN 停止失败|暂未获取|已保留)' })
    observedIpv4=@($labels | Where-Object { $_ -match '^\d{1,3}(\.\d{1,3}){3}$' })
    rulesDownloaded=$text.Contains('已下载规则')
    rulesBundled=$text.Contains('内置规则')
    rulesUpdating=$text.Contains('正在更新…')
    ruleVersions=@([regex]::Matches($text,'(?m)^规则版本\r?\n(\d{12})(?:\r?\n|$)') | ForEach-Object { $_.Groups[1].Value } | Select-Object -Unique)
    buttons=$buttons
  } | ConvertTo-Json -Depth 4 -Compress
}
