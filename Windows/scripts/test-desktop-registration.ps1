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
$output = Join-Path $clientRoot '.artifacts/desktop-registration-test'
New-Item -ItemType Directory -Force -Path $output | Out-Null
Push-Location $output
try {
    $sources = @(
        (Join-Path $clientRoot 'app/test/desktop_notifications_test.cpp'),
        (Join-Path $clientRoot 'app/windows/runner/desktop_notifications.cpp')
    )
    & cl.exe /nologo /std:c++17 /EHsc /W4 /WX /wd4100 /utf-8 /DNOMINMAX $sources /Fe:desktop_notifications_test.exe /link windowsapp.lib ole32.lib shell32.lib advapi32.lib propsys.lib bcrypt.lib user32.lib
    if ($LASTEXITCODE -ne 0) { throw 'Desktop registration test compilation failed.' }
    & ./desktop_notifications_test.exe --registration
    if ($LASTEXITCODE -ne 0) { throw 'Desktop registration tests failed.' }
} finally { Pop-Location }
