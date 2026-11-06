param(
    [string]$AccountFile,
    [string]$ApiProxy = '',
    [string]$ControlHttpIp = ''
)
$ErrorActionPreference = 'Stop'
. "$PSScriptRoot\toolchain.ps1"
$clientRoot = Split-Path $PSScriptRoot -Parent
if (!$AccountFile) { $AccountFile = Join-Path (Split-Path $clientRoot -Parent) 'testaccount.txt' }
$AccountFile = (Resolve-Path -LiteralPath $AccountFile).Path
$screenshots = Join-Path $clientRoot '.artifacts\screenshots\observed-exit'
New-Item -ItemType Directory -Force -Path $screenshots | Out-Null
& "$PSScriptRoot\build-native.ps1"
Push-Location (Join-Path $clientRoot 'app')
try {
    $tests = @('integration_test/live_resources_test.dart', 'integration_test/live_connection_test.dart')
    if ($ControlHttpIp) {
        $parsed = $null
        if (![Net.IPAddress]::TryParse($ControlHttpIp, [ref]$parsed) -or $parsed.AddressFamily -ne [Net.Sockets.AddressFamily]::InterNetwork -or [Net.IPAddress]::IsLoopback($parsed)) { throw 'ControlHttpIp must be the explicit resolved IPv4 address of the fixed API host.' }
        if ($ApiProxy) { throw 'The physically bound HTTPS smoke does not use a proxy.' }
        $tests += 'integration_test/control_http_test.dart'
    }
    # Give each target its own Flutter launcher and VM-service lifetime for
    # this single-instance desktop application.
    foreach ($test in $tests) {
        & $Flutter test $test -d windows --reporter expanded "--dart-define=BLUEGRASS_TEST_ACCOUNT_FILE=$AccountFile" "--dart-define=BLUEGRASS_API_PROXY=$ApiProxy" "--dart-define=BLUEGRASS_CONTROL_HTTP_SMOKE_IP=$ControlHttpIp" "--dart-define=BLUEGRASS_SCREENSHOTS=$screenshots"
        if ($LASTEXITCODE -ne 0) { throw "Live Windows integration failed: $test" }
    }
} finally { Pop-Location }
