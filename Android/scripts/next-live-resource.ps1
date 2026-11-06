param([string]$Device='emulator-5554')
$ErrorActionPreference='Stop'
$ui=Join-Path $PSScriptRoot 'live-ui.ps1'
for ($attempt=0; $attempt -lt 3; $attempt++) {
  $state=(& $ui -Device $Device -Action status) | ConvertFrom-Json
  $selected=@($state.buttons | Where-Object label -eq '已选择')
  if ($selected.Count -ne 1 -or $selected[0].bounds -notmatch '^\[\d+,(\d+)\]') {
    throw 'Current selection must be visible before advancing.'
  }
  $selectedY=[int]$Matches[1]
  $choices=@($state.buttons | Where-Object label -eq '选择')
  for ($i=0; $i -lt $choices.Count; $i++) {
    if ($choices[$i].bounds -match '^\[\d+,(\d+)\]' -and [int]$Matches[1] -gt $selectedY) {
      & $ui -Device $Device -Action tap -Label '选择' -Index $i
      return
    }
  }
  & $ui -Device $Device -Action scroll-down
}
throw 'No next resource became visible.'
