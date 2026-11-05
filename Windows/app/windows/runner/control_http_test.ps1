param([string]$SmokeIp = '')
$ErrorActionPreference = 'Stop'
$vs = 'C:\Program Files\Microsoft Visual Studio\2022\Community\Common7\Tools\VsDevCmd.bat'
$environment = & $env:ComSpec /d /c "call `"$vs`" -no_logo -arch=x64 >nul && set"
if ($LASTEXITCODE -ne 0) { throw 'MSVC environment unavailable' }
foreach ($line in $environment) {
  if ($line -match '^([^=]+)=(.*)$') { [Environment]::SetEnvironmentVariable($matches[1], $matches[2], 'Process') }
}
$out = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..\build\control_http_tests'))
New-Item -ItemType Directory -Force -Path $out | Out-Null
Push-Location $out
try {
  $source = @('control_http_test.cpp', 'control_http.cpp', 'control_http_protocol.cpp', 'control_http_route.cpp') |
    ForEach-Object { Join-Path $PSScriptRoot $_ }
  & cl.exe /nologo /std:c++17 /EHsc /W4 /WX /wd4100 /utf-8 /DNOMINMAX /DWIN32_LEAN_AND_MEAN /D_WIN32_WINNT=0x0A00 $source /Fe:control_http_test.exe /link ws2_32.lib iphlpapi.lib secur32.lib crypt32.lib ncrypt.lib
  if ($LASTEXITCODE -ne 0) { throw 'Native transport compilation failed' }
  & .\control_http_test.exe
  if ($LASTEXITCODE -ne 0) { throw 'Native transport tests failed' }
  $flutterHeaders = Join-Path $PSScriptRoot '..\flutter\ephemeral\cpp_client_wrapper\include'
  $engineHeaders = Join-Path $PSScriptRoot '..\flutter\ephemeral'
  & cl.exe /nologo /std:c++17 /EHsc /W4 /WX /wd4100 /utf-8 /DNOMINMAX /DWIN32_LEAN_AND_MEAN /c /I $flutterHeaders /I $engineHeaders (Join-Path $PSScriptRoot 'control_http_bridge.cpp')
  if ($LASTEXITCODE -ne 0) { throw 'Native Flutter bridge compilation failed' }
  if ($SmokeIp) {
    & .\control_http_test.exe --smoke $SmokeIp
    if ($LASTEXITCODE -ne 0) { throw 'Read-only HTTPS smoke failed' }
  }
} finally { Pop-Location }
