param(
    [string]$Device = 'emulator-5554',
    [string]$AndroidSdk = 'E:\AndroidTools\sdk',
    [string]$Flutter = 'E:\tools\flutter-3.47.9\bin\flutter.bat'
)
$ErrorActionPreference = 'Stop'
$projectRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$adb = Join-Path $AndroidSdk 'platform-tools\adb.exe'
if ($Device -notmatch '^emulator-\d+$' -or ((& $adb -s $Device shell getprop ro.kernel.qemu) -join '').Trim() -ne '1') {
    throw 'This fixture/consent test is restricted to an Android Emulator.'
}
$logs = Join-Path $projectRoot '.artifacts\emulator-20261024'
New-Item -ItemType Directory -Force $logs | Out-Null
$log = Join-Path $logs 'flutter-drive.log'
[IO.File]::WriteAllText($log, '')
# Reset only this test app's VPN consent, so both actual dialog paths run again.
& $adb -s $Device shell am force-stop com.bluegrass.client
& $adb -s $Device shell cmd appops set com.bluegrass.client ACTIVATE_VPN default 2>$null
$job = Start-Job -ArgumentList $projectRoot,$Flutter,$Device,$log -ScriptBlock {
    param($projectRoot,$Flutter,$Device,$log)
    Set-Location $projectRoot
    $env:JAVA_HOME = 'D:\Android\Android Studio\jbr'
    $env:Path = [Environment]::GetEnvironmentVariable('Path','Machine') + ';' + [Environment]::GetEnvironmentVariable('Path','User')
    & $Flutter drive --no-pub --driver=test_driver/emulator_driver.dart --target=integration_test/ui_emulator_test.dart -d $Device > $log 2>&1
    if ($LASTEXITCODE -ne 0) { throw "Flutter emulator driver failed ($LASTEXITCODE). See $log" }
}
try {
    $handled = @{}
    $deadline = [DateTime]::UtcNow.AddMinutes(12)
    while ($job.State -eq 'Running') {
        if ([DateTime]::UtcNow -gt $deadline) { throw 'Emulator test timed out.' }
        $output = ''
        if (Test-Path $log) {
            try { $output = Get-Content -LiteralPath $log -Raw -ErrorAction Stop }
            catch [IO.IOException] { $output = '' }
        }
        if ($null -eq $output) { $output = '' }
        foreach ($phase in @('DENY','ALLOW')) {
            if ($handled[$phase] -or -not $output.Contains("BG_VPN_PERMISSION_$phase")) { continue }
            & $adb -s $Device shell uiautomator dump /sdcard/bluegrass-consent.xml > $null 2>&1
            $wire = (& $adb -s $Device shell cat /sdcard/bluegrass-consent.xml 2>$null) -join ''
            if ($wire -notmatch 'com.android.vpndialogs') { continue }
            [xml]$ui = $wire
            $buttonId = if ($phase -eq 'DENY') { 'android:id/button2' } else { 'android:id/button1' }
            $button = $ui.SelectSingleNode("//node[@package='com.android.vpndialogs' and @resource-id='$buttonId' and @enabled='true']")
            if (-not $button -or $button.bounds -notmatch '^\[(\d+),(\d+)\]\[(\d+),(\d+)\]$') { continue }
            $x = [int](([int]$Matches[1] + [int]$Matches[3]) / 2)
            $y = [int](([int]$Matches[2] + [int]$Matches[4]) / 2)
            [IO.File]::WriteAllText((Join-Path $logs "consent-$phase.xml"),$wire)
            & $adb -s $Device shell screencap -p "/sdcard/bluegrass-consent-$phase.png"
            & $adb -s $Device pull "/sdcard/bluegrass-consent-$phase.png" (Join-Path $logs "consent-$phase.png") > $null 2>&1
            & $adb -s $Device shell input tap $x $y
            $handled[$phase] = $true
            Write-Output "Clicked real VPN consent: $phase"
        }
        Start-Sleep -Milliseconds 500
    }
    Receive-Job $job -ErrorAction Stop
    if ($job.State -ne 'Completed') { throw 'Flutter driver job failed.' }
    if (-not $handled.DENY -or -not $handled.ALLOW) { throw 'Both real VPN dialogs must have been exercised.' }
    Get-Content $log -Tail 12
} finally {
    if ($job.State -eq 'Running') { Stop-Job $job }
    Remove-Job $job -Force
    # Flutter failures/timeouts must not leave this fixture app's TUN behind.
    & $adb -s $Device shell am force-stop com.bluegrass.client
    & $adb -s $Device shell cmd appops set com.bluegrass.client ACTIVATE_VPN default 2>$null
}
