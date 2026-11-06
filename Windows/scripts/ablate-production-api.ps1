# Two exact regression tests; all mutations and dependency output stay in a fresh copy.
$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false
. "$PSScriptRoot\toolchain.ps1"
$clientRoot = Split-Path $PSScriptRoot -Parent
$flutterBin = Split-Path $Flutter -Parent
$dartExe = Join-Path $flutterBin 'cache\dart-sdk\bin\dart.exe'
$flutterSnapshot = Join-Path $flutterBin 'cache\flutter_tools.snapshot'
foreach ($tool in @($dartExe, $flutterSnapshot)) {
    if (!(Test-Path -LiteralPath $tool)) { throw "Cached SDK tool missing: $tool" }
}
$experiment = Join-Path $clientRoot ('.artifacts\ablation\production-api-' +
    (Get-Date -Format 'yyyyMMdd-HHmmss') + '-' + [guid]::NewGuid().ToString('N').Substring(0, 8))
New-Item -ItemType Directory -Path $experiment | Out-Null
& git -C $clientRoot check-ignore --quiet -- $experiment
if ($LASTEXITCODE -ne 0) { throw 'Experiment directory must be ignored by Git.' }
$app = Join-Path $experiment 'app'
$dataSource = Join-Path $experiment 'packages\data_source'
$utf8 = [Text.UTF8Encoding]::new($false)
$manifest = [Collections.Generic.List[object]]::new()
$commands = [Collections.Generic.List[object]]::new()
$results = [Collections.Generic.List[object]]::new()
$completed = $false
$failure = $null

function Write-JsonFile($Value, [string]$Path) {
    [IO.File]::WriteAllText($Path, (ConvertTo-Json -InputObject $Value -Depth 12), $utf8)
}

function Copy-Snapshot([string]$RelativeRoot, [string[]]$Names) {
    foreach ($name in $Names) {
        $source = Join-Path $clientRoot "$RelativeRoot\$name"
        if (!(Test-Path -LiteralPath $source)) {
            if ($name -eq 'pubspec.lock') { continue }
            throw "Required snapshot input missing: $source"
        }
        $files = if ((Get-Item -LiteralPath $source).PSIsContainer) {
            @(Get-ChildItem -LiteralPath $source -File -Recurse)
        } else { @(Get-Item -LiteralPath $source) }
        foreach ($file in $files) {
            $relative = [IO.Path]::GetRelativePath($clientRoot, $file.FullName)
            $destination = Join-Path $experiment $relative
            $before = (Get-FileHash -LiteralPath $file.FullName -Algorithm SHA256).Hash
            New-Item -ItemType Directory -Force -Path (Split-Path $destination -Parent) | Out-Null
            Copy-Item -LiteralPath $file.FullName -Destination $destination
            if ((Get-FileHash -LiteralPath $destination -Algorithm SHA256).Hash -ne $before) {
                throw "Source changed while copying: $relative"
            }
            $manifest.Add([ordered]@{ path=$relative; before=$before; after=$null; unchanged=$false })
        }
    }
}

function Invoke-Logged([string]$WorkingDirectory, [string[]]$Arguments, [string]$Name) {
    $log = Join-Path $experiment "$Name.log"
    $timer = [Diagnostics.Stopwatch]::StartNew()
    Push-Location $WorkingDirectory
    try {
        # Avoid flutter.bat bootstrap work. The tool still takes its normal
        # short cache-preparation lock, released before running test bodies.
        & $dartExe @Arguments *> $log
        $code = $LASTEXITCODE
    } finally { Pop-Location; $timer.Stop() }
    $run = [ordered]@{
        name=$Name; executable=$dartExe; arguments=$Arguments; cwd=$WorkingDirectory
        exitCode=$code; durationMs=$timer.ElapsedMilliseconds; log=$log
    }
    $commands.Add($run)
    return $run
}

function Assert-IsolatedPackages([string]$Root, [hashtable]$Expected) {
    $config = Join-Path $Root '.dart_tool\package_config.json'
    $parsed = Get-Content -Raw -LiteralPath $config | ConvertFrom-Json
    foreach ($name in $Expected.Keys) {
        $entries = @($parsed.packages | Where-Object name -eq $name)
        if ($entries.Count -ne 1) { throw "Missing isolated package: $name" }
        $resolved = [Uri]::new([Uri]::new($config), [string]$entries[0].rootUri).LocalPath
        if ([IO.Path]::GetFullPath($resolved).TrimEnd('\', '/') -ne
            [IO.Path]::GetFullPath($Expected[$name]).TrimEnd('\', '/')) {
            throw "Package resolves outside its snapshot: $name"
        }
    }
}

function Invoke-Case($Variant, [string]$Phase) {
    $exactName = '^' + [regex]::Escape($Variant.testName) + '$'
    $arguments = if ($Variant.runner -eq 'flutter') {
        @($flutterSnapshot, '--suppress-analytics', 'test', '--no-pub')
    } else { @('--suppress-analytics', 'test') }
    $arguments += @($Variant.testFile, '--name', $exactName, '--reporter', 'expanded')
    $run = Invoke-Logged $Variant.root $arguments "$($Variant.name)-$Phase"
    $text = Get-Content -Raw -LiteralPath $run.log
    $setupFailure = $text -match 'Compilation failed|Failed to load|Error when reading|No tests (ran|match|were found)|Could not resolve|Error: Not found'
    if ($Phase -eq 'ablated') {
        # A nonzero process alone is insufficient: require the selected test's
        # runtime matcher failure, expected guard error, and one failed test.
        $runtimeStack = $text.Replace('\', '/') -match
            ([regex]::Escape($Variant.testFile) + '\s+\d+:\d+\s+main\.<fn>')
        $behavioral = !$setupFailure -and $run.exitCode -ne 0 -and
            $text.Contains($Variant.testName) -and $text.Contains($Variant.expectedError) -and
            $text.Contains('Expected:') -and $text.Contains('Actual:') -and
            $runtimeStack -and $text.Contains('Which: emitted') -and
            $text.Contains('+0 -1:') -and
            $text.Contains('[E]') -and $text.Contains('Some tests failed.')
        if (!$behavioral) { throw "Expected compiled behavioral failure absent: $($run.log)" }
        $run['outcome'] = 'compiled; selected test reached runtime assertion and failed'
    } else {
        if ($setupFailure -or $run.exitCode -ne 0 -or
            !$text.Contains($Variant.testName) -or !$text.Contains('+1: All tests passed!')) {
            throw "Exact single-test pass absent: $($run.log)"
        }
        $run['outcome'] = 'compiled; one selected test passed'
    }
    return $run
}

try {
    Copy-Snapshot 'app' @('lib', 'test', 'assets', 'pubspec.yaml', 'pubspec.lock')
    foreach ($package in @('domain', 'data_source')) {
        Copy-Snapshot "packages\$package" @('lib', 'test', 'pubspec.yaml', 'pubspec.lock')
    }
    Write-JsonFile $manifest (Join-Path $experiment 'shared-hashes.json')
    foreach ($setup in @(
        @{ root=$dataSource; args=@('--suppress-analytics', 'pub', 'get', '--offline'); name='data-source-dependencies' },
        @{ root=$app; args=@($flutterSnapshot, '--suppress-analytics', 'pub', 'get', '--offline'); name='app-dependencies' }
    )) {
        $run = Invoke-Logged $setup.root $setup.args $setup.name
        if ($run.exitCode -ne 0) { throw "Offline dependency setup failed: $($run.log)" }
    }
    $domain = Join-Path $experiment 'packages\domain'
    Assert-IsolatedPackages $app @{bluegrass_client=$app; bluegrass_domain=$domain; bluegrass_data_source=$dataSource}
    Assert-IsolatedPackages $dataSource @{bluegrass_domain=$domain; bluegrass_data_source=$dataSource}
    $variants = @(
        @{
            name='remove-control-uri-host-pin'; runner='flutter'; root=$app
            target='app\lib\control_http_transport.dart'; testFile='test/control_http_transport_test.dart'
            testName='retired API origin is rejected in both network modes'; expectedError='insecure_endpoint'
            patterns=@("(?m)^        uri\.host != _baseUri\.host \|\|\r?\n")
        },
        @{
            name='remove-both-session-environment-guards'; runner='dart'; root=$dataSource
            target='packages\data_source\lib\src\session_coordinator.dart'; testFile='test/production_origin_login_test.dart'
            testName='production never sends former environment credentials: valid_access'; expectedError='environment_mismatch'
            patterns=@(
                "(?m)^    if \(stored\['environment'\] != _environment\)\r?\n      throw const ApiFailure\('environment_mismatch'\);\r?\n",
                "(?m)^        data\['environment'\] != _environment \|\|\r?\n"
            )
        }
    )
    foreach ($variant in $variants) {
        $target = Join-Path $experiment $variant.target
        $bytes = [IO.File]::ReadAllBytes($target)
        $originalHash = (Get-FileHash -LiteralPath $target -Algorithm SHA256).Hash
        $text = [Text.Encoding]::UTF8.GetString($bytes)
        $mutant = $text
        $removed = @()
        foreach ($pattern in $variant.patterns) {
            $matches = [regex]::Matches($mutant, $pattern)
            if ($matches.Count -ne 1) { throw "Mutation anchor is not unique: $($variant.name)" }
            $removed += $matches[0].Value
            $mutant = [regex]::Replace($mutant, $pattern, '')
        }
        Write-JsonFile @{target=$variant.target; removed=$removed} (Join-Path $experiment "$($variant.name)-mutation.json")
        $baseline = Invoke-Case $variant 'baseline'
        try {
            [IO.File]::WriteAllText($target, $mutant, $utf8)
            $mutantHash = (Get-FileHash -LiteralPath $target -Algorithm SHA256).Hash
            if ($mutantHash -eq $originalHash) { throw 'Mutation did not change the isolated source.' }
            $ablated = Invoke-Case $variant 'ablated'
        } finally { [IO.File]::WriteAllBytes($target, $bytes) }
        $restored = Invoke-Case $variant 'restored'
        $restoredHash = (Get-FileHash -LiteralPath $target -Algorithm SHA256).Hash
        if ($restoredHash -ne $originalHash) { throw 'Isolated byte-for-byte restoration failed.' }
        $results.Add([ordered]@{
            name=$variant.name; test=$variant.testName; target=$variant.target
            baseline=$baseline; ablated=$ablated; restored=$restored
            originalHash=$originalHash; mutantHash=$mutantHash; restoredHash=$restoredHash; decision='retain'
        })
        Write-Host "Verified $($variant.name): pass -> compiled behavioral failure -> restored pass"
    }
    $completed = $true
} catch {
    $failure = $_.Exception.Message
    throw
} finally {
    $changed = @()
    foreach ($entry in $manifest) {
        $source = Join-Path $clientRoot $entry.path
        $entry.after = if (Test-Path -LiteralPath $source) {
            (Get-FileHash -LiteralPath $source -Algorithm SHA256).Hash
        } else { 'MISSING' }
        $entry.unchanged = $entry.before -eq $entry.after
        if (!$entry.unchanged) { $changed += $entry.path }
    }
    Write-JsonFile $manifest (Join-Path $experiment 'shared-hashes.json')
    Write-JsonFile $commands (Join-Path $experiment 'commands.json')
    Write-JsonFile ([ordered]@{
        completed=($completed -and $changed.Count -eq 0); failure=$failure
        experiment=$experiment; sharedFilesChecked=$manifest.Count; changedSharedFiles=$changed
        dependencyMode='offline'; packageResolution='isolated app/domain/data_source'
        cases=$results
    }) (Join-Path $experiment 'results.json')
    if ($changed.Count -ne 0) { throw 'Shared snapshot inputs changed; evidence must be refreshed. See shared-hashes.json.' }
}
Write-Host "Two isolated production API ablations verified: $experiment"
