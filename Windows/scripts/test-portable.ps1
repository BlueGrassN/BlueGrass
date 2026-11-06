$ErrorActionPreference = 'Stop'
$clientRoot = [IO.Path]::GetFullPath((Split-Path $PSScriptRoot -Parent))
$vswhere = Join-Path ${env:ProgramFiles(x86)} 'Microsoft Visual Studio/Installer/vswhere.exe'
$vs = & $vswhere -latest -products '*' -requires Microsoft.VisualStudio.Component.VC.Tools.x86.x64 -property installationPath
if (!$vs) { throw 'MSVC is required.' }
$developerShell = Join-Path $vs 'Common7/Tools/VsDevCmd.bat'
$environment = & $env:ComSpec /d /c "call `"$developerShell`" -no_logo -arch=x64 >nul && set"
if ($LASTEXITCODE -ne 0) { throw 'MSVC environment unavailable.' }
foreach ($line in $environment) {
    if ($line -match '^([^=]+)=(.*)$') {
        [Environment]::SetEnvironmentVariable($matches[1], $matches[2], 'Process')
    }
}
$output = Join-Path $clientRoot ('.artifacts/portable-tests/启动 测试 ' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path (Join-Path $output 'runtime') -Force | Out-Null
Push-Location $output
try {
    $flags = @('/nologo','/std:c++17','/EHsc','/MT','/W4','/WX','/wd4100','/utf-8','/DNOMINMAX','/DUNICODE','/D_UNICODE')
    & cl.exe @flags (Join-Path $clientRoot 'app/windows/runner/portable_launcher.cpp') /Fe:BlueGrass.exe /link /SUBSYSTEM:WINDOWS user32.lib
    if ($LASTEXITCODE -ne 0) { throw 'Launcher compilation failed.' }
    $imports = (& dumpbin.exe /nologo /dependents BlueGrass.exe) -join "`n"
    if ($LASTEXITCODE -ne 0 -or $imports -match '(?i)(flutter_windows|msvcp\d|vcruntime\d|ucrtbase)\.dll') { throw 'Launcher requires adjacent runtime DLLs.' }
    & cl.exe @flags (Join-Path $clientRoot 'app/test/portable_launcher_probe.cpp') /Fe:runtime/BlueGrass.exe
    if ($LASTEXITCODE -ne 0) { throw 'Probe compilation failed.' }
    $arguments = '--autostart --toast-activated "" "中文 空格" "quote\"value" "trailing\\"'
    $launcher = Start-Process -FilePath (Join-Path $output 'BlueGrass.exe') -ArgumentList $arguments -WorkingDirectory $env:SystemRoot -WindowStyle Hidden -PassThru
    if (!$launcher.WaitForExit(5000) -or $launcher.ExitCode -ne 0) { throw 'Launcher failed or did not exit.' }
    $record = Join-Path $output 'runtime/launch.txt'
    for ($i = 0; $i -lt 50 -and !(Test-Path -LiteralPath $record); $i++) { Start-Sleep -Milliseconds 100 }
    if (!(Test-Path -LiteralPath $record)) { throw 'Child process did not run.' }
    $actual = [IO.File]::ReadAllText($record, [Text.Encoding]::Unicode).Split("`n")
    $expected = @((Join-Path $output 'runtime'), '--autostart', '--toast-activated', '', '中文 空格', 'quote"value', 'trailing\', '')
    if (@(Compare-Object $actual $expected -SyncWindow 0).Count) { throw 'Child CWD or arguments changed.' }
    Write-Output 'PASS: Unicode/space installation path, foreign CWD, empty/quoted/backslash arguments, autostart and toast flags; launcher needs only Windows DLLs.'

    & cl.exe @flags (Join-Path $clientRoot 'app/test/owned_autostart_test.cpp') /Fe:autostart_test.exe /link advapi32.lib
    if ($LASTEXITCODE -ne 0) { throw 'Autostart compilation failed.' }
    & ./autostart_test.exe
    if ($LASTEXITCODE -ne 0) { throw 'Flat autostart failed.' }
    Copy-Item -LiteralPath ./autostart_test.exe -Destination ./runtime/BlueGrass.exe -Force
    & ./runtime/BlueGrass.exe
    if ($LASTEXITCODE -ne 0) { throw 'Portable autostart failed.' }

    $sources = @((Join-Path $clientRoot 'app/test/desktop_notifications_test.cpp'), (Join-Path $clientRoot 'app/windows/runner/desktop_notifications.cpp'))
    & cl.exe @flags $sources /Fe:notifications_test.exe /link windowsapp.lib ole32.lib shell32.lib advapi32.lib propsys.lib bcrypt.lib user32.lib
    if ($LASTEXITCODE -ne 0) { throw 'Notification compilation failed.' }
    & ./notifications_test.exe --registration
    if ($LASTEXITCODE -ne 0) { throw 'Flat notification registration failed.' }
    Copy-Item -LiteralPath ./notifications_test.exe -Destination ./runtime/BlueGrass.exe -Force
    & ./runtime/BlueGrass.exe --registration
    if ($LASTEXITCODE -ne 0) { throw 'Portable notification registration failed.' }
    Write-Output 'PASS: flat and portable startup/notification registrations; isolated test keys cleaned up; no notification sent.'
} finally { Pop-Location }
