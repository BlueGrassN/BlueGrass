param(
  [string]$Device = 'emulator-5554',
  [string]$AccountFile = (Join-Path $PSScriptRoot '..\..\testaccount.txt'),
  [string]$Adb = 'E:\AndroidTools\sdk\platform-tools\adb.exe'
)
$ErrorActionPreference = 'Stop'
if ($Device -notmatch '^emulator-\d+$') { throw 'A designated emulator is required.' }
function Read-Layout {
  & $Adb -s $Device shell uiautomator dump /sdcard/bluegrass-live-layout.xml > $null
  if ($LASTEXITCODE -ne 0) { throw 'UI snapshot failed.' }
  $wire = (& $Adb -s $Device shell cat /sdcard/bluegrass-live-layout.xml) -join ''
  & $Adb -s $Device shell rm /sdcard/bluegrass-live-layout.xml > $null
  return [xml]$wire
}
function Tap-Node($node) {
  if (-not $node -or $node.bounds -notmatch '^\[(\d+),(\d+)\]\[(\d+),(\d+)\]$') { throw 'Required UI control unavailable.' }
  $x=[int](([int]$Matches[1]+[int]$Matches[3])/2)
  $y=[int](([int]$Matches[2]+[int]$Matches[4])/2)
  & $Adb -s $Device shell input tap $x $y > $null
}
$credentials = @([IO.File]::ReadAllLines((Resolve-Path -LiteralPath $AccountFile).Path) | Where-Object { $_.Trim() })
try {
  if ($credentials.Count -ne 2) { throw 'Test account file requires two lines.' }
  # ADB input's shell and percent-space syntax require a bounded safe alphabet.
  # Credentials are never placed in source, APK defines, logs or screenshots.
  if (@($credentials | Where-Object { $_ -notmatch '^[A-Za-z0-9._@!+%-]+$' -or $_.Contains('%s') }).Count) {
    throw 'Credential requires a different input transport; no text was entered.'
  }
  $tree=Read-Layout
  Tap-Node $tree.SelectSingleNode("//node[@class='android.widget.EditText' and @password='false']")
  & $Adb -s $Device shell input text ("'"+$credentials[0].Trim()+"'") > $null
  $tree=Read-Layout
  Tap-Node $tree.SelectSingleNode("//node[@class='android.widget.EditText' and @password='true']")
  & $Adb -s $Device shell input text ("'"+$credentials[1]+"'") > $null
  & $Adb -s $Device shell input keyevent 4 > $null
  $tree=Read-Layout
  Tap-Node $tree.SelectSingleNode("//node[@content-desc='登录']")
  'Submitted the designated test account through the normal app login UI.'
} finally {
  $credentials = $null
  $tree = $null
}
