<#
.SYNOPSIS
Prepare, then asynchronously launch the opt-in, elevated TUN/WFP acceptance run.
.EXAMPLE
.\test-elevated-tun.ps1 -PrepareOnly
.EXAMPLE
.\test-elevated-tun.ps1 -Run -RunDirectory <directory printed by PrepareOnly>
.NOTES
Preparation never elevates or executes a test. Run requests one UAC prompt.
Poll summary.json and watchdog.status.json; launch.json is not a test result.
When summary.recoveryPending is true, wait for the watchdog's final result.
The account file is passed by path only. No service is installed.
#>
[CmdletBinding(DefaultParameterSetName = 'Prepare')]
param(
    [Parameter(Mandatory, ParameterSetName = 'Prepare')][switch]$PrepareOnly,
    [Parameter(Mandatory, ParameterSetName = 'Run')][switch]$Run,
    [Parameter(Mandatory, ParameterSetName = 'Run')][string]$RunDirectory,
    [Parameter(ParameterSetName = 'Prepare')][string]$AccountFile = '',
    [Parameter(ParameterSetName = 'Prepare')][ValidateRange(180, 240)][int]$MaxSeconds = 240,
    [Parameter(ParameterSetName = 'Prepare')][ValidateSet('global', 'bypass_cn')][string]$RoutingMode = 'global'
)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
if (!$PrepareOnly -and !$Run) { throw 'Select -PrepareOnly or -Run.' }
$clientRoot = [IO.Path]::GetFullPath((Split-Path $PSScriptRoot -Parent))
$repositoryRoot = Split-Path $clientRoot -Parent
$artifactRoot = Join-Path $clientRoot '.artifacts/verification/elevated-tun'
$runsRoot = Join-Path $artifactRoot 'runs'
$shellPath = if ($PSVersionTable.PSVersion.Major -ge 6) { Join-Path $PSHOME 'pwsh.exe' } else { Join-Path $PSHOME 'powershell.exe' }

function Assert-OrdinaryPath([string]$Path) {
    $current = [IO.Path]::GetFullPath($Path)
    while ($current) {
        if ((Test-Path -LiteralPath $current) -and
            ((Get-Item -LiteralPath $current -Force).Attributes -band [IO.FileAttributes]::ReparsePoint)) {
            throw 'An acceptance path contains a reparse point.'
        }
        $current = [IO.Path]::GetDirectoryName($current)
    }
}
function Write-Json([string]$Path, $Value) {
    [IO.File]::WriteAllText($Path, ($Value | ConvertTo-Json -Depth 12), [Text.UTF8Encoding]::new($false))
}
function Quote-Argument([string]$Value) {
    if ($Value -match '["\r\n]' -or $Value.EndsWith('\')) { throw 'Unsupported process argument.' }
    '"' + $Value + '"'
}
if (![Environment]::Is64BitProcess -or $env:OS -ne 'Windows_NT') {
    throw 'Run this script in 64-bit Windows PowerShell.'
}
Assert-OrdinaryPath $artifactRoot
Assert-OrdinaryPath $shellPath

if ($PrepareOnly) {
    if (!$AccountFile) { $AccountFile = Join-Path $repositoryRoot 'testaccount.txt' }
    $AccountFile = [IO.Path]::GetFullPath($AccountFile)
    Assert-OrdinaryPath $AccountFile
    if (!(Test-Path -LiteralPath $AccountFile -PathType Leaf)) { throw 'The account file is missing.' }
    New-Item -ItemType Directory -Path $runsRoot -Force | Out-Null
    Assert-OrdinaryPath (Join-Path $artifactRoot 'prepare.lock')
    # Retain the lock file; the exclusive handle, not deletion, controls preparation.
    $prepareLock = [IO.File]::Open((Join-Path $artifactRoot 'prepare.lock'), 'OpenOrCreate', 'ReadWrite', 'None')
    try {
        $env:Path = [Environment]::GetEnvironmentVariable('Path', 'Machine') + ';' +
            [Environment]::GetEnvironmentVariable('Path', 'User') + ';' + $env:Path
        $goPath = (Get-Command go -ErrorAction Stop).Source
        $executable = Join-Path $artifactRoot 'BlueGrass.TunAcceptance.exe'
        $probe = Join-Path $artifactRoot 'BlueGrass.TunProbe.exe'
        Assert-OrdinaryPath $executable
        Assert-OrdinaryPath $probe
        $oldGoos = $env:GOOS
        $oldGoarch = $env:GOARCH
        $oldGoCache = $env:GOCACHE
        Push-Location (Join-Path $clientRoot 'native/core')
        try {
            $env:GOOS = 'windows'
            $env:GOARCH = 'amd64'
            # Keep this verification independent of a damaged shared build cache,
            # while retaining an explicit caller-supplied cache if present.
            if (!$env:GOCACHE) { $env:GOCACHE = Join-Path $artifactRoot 'go-cache' }
            & $goPath test -c -o $executable ./winservice
            if ($LASTEXITCODE -ne 0) { throw 'Acceptance test compilation failed.' }
        } finally {
            $env:GOOS = $oldGoos
            $env:GOARCH = $oldGoarch
            $env:GOCACHE = $oldGoCache
            Pop-Location
        }
        & (Join-Path $PSScriptRoot 'prepare-wintun.ps1') -Destination $artifactRoot
        Copy-Item -LiteralPath $executable -Destination $probe -Force
        # Listing does not execute tests or read the account. Refuse to launch a
        # binary built while the parent is still adding the opt-in test cases.
        $oldRun = $env:BLUEGRASS_ELEVATED_TUN_RUN
        $oldAccount = $env:BLUEGRASS_ELEVATED_TUN_ACCOUNT
        try {
            $env:BLUEGRASS_ELEVATED_TUN_RUN = $null
            $env:BLUEGRASS_ELEVATED_TUN_ACCOUNT = $null
            $testNames = @(& $executable '-test.list=^TestElevatedTUN(Acceptance|BusinessProbe|CrashCheck|Recovery)$')
            if ($LASTEXITCODE -ne 0) { throw 'Could not list the compiled acceptance tests.' }
        } finally {
            $env:BLUEGRASS_ELEVATED_TUN_RUN = $oldRun
            $env:BLUEGRASS_ELEVATED_TUN_ACCOUNT = $oldAccount
        }
        $requiredTests = @('TestElevatedTUNAcceptance', 'TestElevatedTUNBusinessProbe', 'TestElevatedTUNCrashCheck', 'TestElevatedTUNRecovery')
        $missingTests = @($requiredTests | Where-Object { $_ -cnotin $testNames })
        $runId = [Guid]::NewGuid().ToString('N')
        $RunDirectory = Join-Path $runsRoot $runId
        New-Item -ItemType Directory -Path $RunDirectory | Out-Null
        $files = @()
        foreach ($name in @('BlueGrass.TunAcceptance.exe', 'BlueGrass.TunProbe.exe', 'wintun.dll', 'LICENSE.wintun')) {
            $destination = Join-Path $RunDirectory $name
            Copy-Item -LiteralPath (Join-Path $artifactRoot $name) -Destination $destination
            $files += [ordered]@{ name = $name; sha256 = (Get-FileHash -LiteralPath $destination -Algorithm SHA256).Hash }
        }
        $helperName = 'watch-elevated-tun.ps1'
        Copy-Item -LiteralPath (Join-Path $PSScriptRoot $helperName) -Destination (Join-Path $RunDirectory $helperName)
        $files += [ordered]@{ name = $helperName; sha256 = (Get-FileHash -LiteralPath (Join-Path $RunDirectory $helperName) -Algorithm SHA256).Hash }
        $manifest = [ordered]@{
            schemaVersion = 1; runId = $runId; runDirectory = $RunDirectory
            repositoryRoot = $repositoryRoot; accountFile = $AccountFile
            initiatingUserSid = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
            powershell = $shellPath; maxSeconds = $MaxSeconds; routingMode = $RoutingMode
            preparedUtc = [DateTime]::UtcNow.ToString('o'); files = $files
            acceptanceTest = '^TestElevatedTUNAcceptance$'
            crashCheckTest = '^TestElevatedTUNCrashCheck$'
            recoveryTest = '^TestElevatedTUNRecovery$'
            readyMarker = 'watchdog-ready.json'; readyMarkerAlias = 'watchdog.ready.json'; ownershipMarker = 'owned.json'
            crashReadyMarker = 'crash-ready.json'; completedMarker = 'completed.json'
            runnable = $missingTests.Count -eq 0; missingTests = $missingTests
        }
        Write-Json (Join-Path $RunDirectory 'manifest.json') $manifest
        Write-Json (Join-Path $RunDirectory 'status.json') @{
            state = 'prepared'; networkTestsExecuted = $false; utc = [DateTime]::UtcNow.ToString('o')
        }
        [pscustomobject]@{
            State = if ($missingTests.Count) { 'PreparedMissingTests' } else { 'Prepared' }
            RunDirectory = $RunDirectory; MaxSeconds = $MaxSeconds; MissingTests = $missingTests
            Manifest = Join-Path $RunDirectory 'manifest.json'
            NetworkTestsExecuted = $false
        }
    } finally { $prepareLock.Dispose() }
    return
}

$RunDirectory = [IO.Path]::GetFullPath($RunDirectory).TrimEnd('\', '/')
if ([IO.Path]::GetDirectoryName($RunDirectory) -ine [IO.Path]::GetFullPath($runsRoot) -or
    [IO.Path]::GetFileName($RunDirectory) -notmatch '^[0-9a-f]{32}$') {
    throw 'RunDirectory must be an exact prepared run beneath the acceptance runs directory.'
}
Assert-OrdinaryPath $RunDirectory
$manifest = Get-Content -LiteralPath (Join-Path $RunDirectory 'manifest.json') -Raw | ConvertFrom-Json
if ($manifest.schemaVersion -ne 1 -or $manifest.runDirectory -ine $RunDirectory -or
    $manifest.powershell -ine $shellPath) { throw 'Invalid prepared run manifest.' }
if (!$manifest.runnable) { throw 'The compiled binary is missing required tests; prepare again after the Go tests are ready.' }
if (@($manifest.files).Count -ne 5 -or @($manifest.files.name | Select-Object -Unique).Count -ne 5) {
    throw 'The manifest must contain exactly the five prepared artifacts.'
}
foreach ($file in $manifest.files) {
    if ($file.name -notin @('BlueGrass.TunAcceptance.exe', 'BlueGrass.TunProbe.exe', 'wintun.dll', 'LICENSE.wintun', 'watch-elevated-tun.ps1')) {
        throw 'Unexpected prepared artifact.'
    }
    $filePath = Join-Path $RunDirectory $file.name
    Assert-OrdinaryPath $filePath
    if ((Get-FileHash -LiteralPath $filePath -Algorithm SHA256).Hash -cne $file.sha256) {
        throw 'Prepared artifact changed; prepare a fresh run.'
    }
}
$claim = [IO.File]::Open((Join-Path $RunDirectory 'launch.claim'), 'CreateNew', 'Write', 'None')
$claim.Dispose()
try {
    $arguments = @('-NoLogo', '-NoProfile', '-NonInteractive', '-File',
        (Quote-Argument (Join-Path $RunDirectory 'watch-elevated-tun.ps1')),
        '-Role', 'Supervisor', '-RunDirectory', (Quote-Argument $RunDirectory)) -join ' '
    # No ExecutionPolicy override; a policy/signature refusal remains a refusal.
    $process = Start-Process -FilePath $shellPath -ArgumentList $arguments -Verb RunAs -WindowStyle Hidden -PassThru
    $launch = [ordered]@{
        state = 'launched'; pid = $process.Id; executablePath = $shellPath
        startTimeUtcTicks = $process.StartTime.ToUniversalTime().Ticks.ToString()
        utc = [DateTime]::UtcNow.ToString('o'); runDirectory = $RunDirectory
    }
    Write-Json (Join-Path $RunDirectory 'launch.json') $launch
    [pscustomobject]$launch
} catch {
    Write-Json (Join-Path $RunDirectory 'launch.json') @{
        state = 'launch_failed'; errorType = $_.Exception.GetType().Name
        utc = [DateTime]::UtcNow.ToString('o')
    }
    throw
}
