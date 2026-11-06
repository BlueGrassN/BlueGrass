$ErrorActionPreference = 'Stop'
. "$PSScriptRoot\toolchain.ps1"
$clientRoot = Split-Path $PSScriptRoot -Parent
$repoRoot = Split-Path $clientRoot -Parent
$experiment = Join-Path $clientRoot ('.artifacts\ablation\controller-' + (Get-Date -Format 'yyyyMMdd-HHmmss'))
$contracts = Join-Path $experiment 'contracts'
New-Item -ItemType Directory -Force -Path $contracts | Out-Null
Copy-Item -LiteralPath (Join-Path $repoRoot 'contracts\fixtures') -Destination $contracts -Recurse
$app = Join-Path $experiment 'Windows\app'
New-Item -ItemType Directory -Force -Path $app | Out-Null
foreach ($name in @('lib', 'test', 'assets', 'pubspec.yaml', 'pubspec.lock')) {
    Copy-Item -LiteralPath (Join-Path $clientRoot "app\$name") -Destination $app -Recurse
}
foreach ($package in @('domain', 'data_source', 'client')) {
    $target = Join-Path $experiment "packages\$package"
    New-Item -ItemType Directory -Force -Path $target | Out-Null
    foreach ($name in @('lib', 'pubspec.yaml', 'pubspec.lock')) {
        $source = Join-Path $repoRoot "packages\$package\$name"
        if (Test-Path -LiteralPath $source) { Copy-Item -LiteralPath $source -Destination $target -Recurse }
    }
}
$target = Join-Path $experiment 'packages\client\lib\app_controller.dart'
$bytes = [IO.File]::ReadAllBytes($target)
# Normalize only the disposable mutation text; restore the exact saved bytes.
$text = [Text.Encoding]::UTF8.GetString($bytes).Replace("`r`n", "`n")
$variants = @(
    @{Name='remove-update-build-check'; Before="if ((marker['build'] as int) >= installedBuild) return true;"; After='';
      Test='test/update_resume_test.dart'; Pattern='changed build: false'; Expected='Actual: <true>'},
    @{Name='remove-diagnostic-context-check';
      Before=") !=`n              context ||";
      After=") ==`n              context ||";
      Test='test/selected_diagnostics_test.dart'; Pattern='stale=true'; Expected='Expected: empty'},
    @{Name='remove-accepted-attempt-identity';
      Before="_nativeContext = context is Map<String, dynamic>`n          ? domain.ControlContext.fromJson(context)`n          : null;";
      After='// Experimental removal: retain the preceding resource identity.';
      Test='test/protected_switch_test.dart'; Pattern='accepted failed attempt retains current resource identity: switch-prepare-retry-tun'; Expected='Actual: <false>'}
)
function Invoke-Case($Variant, [string]$Phase) {
    $log = Join-Path $experiment "$($Variant.Name)-$Phase.log"
    & $Flutter test $Variant.Test '--plain-name' $Variant.Pattern '--reporter' 'expanded' *> $log
    return $LASTEXITCODE
}
$results = @()
Push-Location $app
try {
    & $Flutter pub get *> (Join-Path $experiment 'dependencies.log')
    if ($LASTEXITCODE -ne 0) { throw 'Isolated dependency setup failed.' }
    foreach ($variant in $variants) {
        if (!$text.Contains($variant.Before)) { throw "Ablation anchor changed: $($variant.Name)" }
        if ((Invoke-Case $variant 'baseline') -ne 0) { throw 'Baseline behavior failed.' }
        try {
            [IO.File]::WriteAllText($target, $text.Replace($variant.Before, $variant.After), [Text.UTF8Encoding]::new($false))
            $code = Invoke-Case $variant 'ablated'
            $log = Get-Content -Raw -LiteralPath (Join-Path $experiment "$($variant.Name)-ablated.log")
            if ($code -eq 0 -or !$log.Contains($variant.Expected) -or $log.Contains('Compilation failed')) {
                throw "Expected behavioral failure absent: $($variant.Name)"
            }
        } finally { [IO.File]::WriteAllBytes($target, $bytes) }
        if ((Invoke-Case $variant 'restored') -ne 0) { throw 'Restored behavior failed.' }
        $results += @{ name=$variant.Name; baseline='pass'; ablated='behavioral failure'; restored='pass'; decision='retain' }
    }
    if ((Get-FileHash -LiteralPath $target).Hash -ne (Get-FileHash -LiteralPath (Join-Path $repoRoot 'packages\client\lib\app_controller.dart')).Hash) {
        throw 'Shared controller changed during isolated experiment; rebase evidence before claiming final coverage.'
    }
} finally { Pop-Location }
$results | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $experiment 'results.json') -Encoding utf8
Write-Host "$($variants.Count) isolated controller behavior ablations verified: $experiment"
