param(
    [string]$Cache = "$env:USERPROFILE/.gradle/caches/modules-2/files-2.1",
    [switch]$Platform,
    [string]$Sdk = 'E:/AndroidTools/sdk'
)
$ErrorActionPreference = 'Stop'
# Reuse the installed Kotlin compiler without invoking Gradle or needing the Go AAR.
function Find-Jar([string]$group, [string]$name, [string]$version) {
    $file = Get-ChildItem -LiteralPath "$Cache/$group/$name/$version" -Filter "$name-$version.jar" -Recurse | Select-Object -First 1
    if (!$file) { throw "Missing cached dependency: $group/$name/$version" }
    return $file.FullName
}
$stdlib = Find-Jar 'org.jetbrains.kotlin' 'kotlin-stdlib' '2.4.0'
$junit = Find-Jar 'junit' 'junit' '4.12'
$annotations = Find-Jar 'org.jetbrains' 'annotations' '13.0'
$compiler = @(
    (Find-Jar 'org.jetbrains.kotlin' 'kotlin-compiler-embeddable' '2.4.0'),
    $stdlib,
    (Find-Jar 'org.jetbrains.kotlin' 'kotlin-script-runtime' '2.4.0'),
    (Find-Jar 'org.jetbrains.kotlin' 'kotlin-reflect' '2.2.21'),
    (Find-Jar 'org.jetbrains.kotlinx' 'kotlinx-coroutines-core-jvm' '1.8.0'),
    $annotations
) -join [IO.Path]::PathSeparator
$output = Join-Path $PSScriptRoot '.native-vpn-check'
New-Item -ItemType Directory -Force -Path $output | Out-Null
$sources = @(@(
    "$PSScriptRoot/app/src/main/kotlin/com/bluegrass/client/vpn/VpnController.kt",
    "$PSScriptRoot/app/src/main/kotlin/com/bluegrass/client/vpn/VpnTraffic.kt",
    "$PSScriptRoot/app/src/androidTest/kotlin/com/bluegrass/client/vpn/VpnControllerTest.kt"
) | Where-Object { Test-Path -LiteralPath $_ })
$classpath = "$stdlib;$junit;$annotations"
if ($Platform) {
    $sources += @(
        "$PSScriptRoot/app/src/main/kotlin/com/bluegrass/client/SecureSessionStorage.kt",
        "$PSScriptRoot/app/src/androidTest/kotlin/com/bluegrass/client/SecureSessionStorageTest.kt",
        "$PSScriptRoot/app/src/main/kotlin/com/bluegrass/client/vpn/VpnRequestParser.kt",
        "$PSScriptRoot/app/src/main/kotlin/com/bluegrass/client/vpn/PhysicalNetworks.kt",
        "$PSScriptRoot/app/src/androidTest/kotlin/com/bluegrass/client/vpn/VpnPlatformContractTest.kt"
    )
    $classpath += ";$Sdk/platforms/android-36/android.jar;$Sdk/platforms/android-36/optional/android.test.base.jar;$Sdk/platforms/android-36/optional/android.test.runner.jar"
}
$target = if ($Platform) { "$output/platform-tests.jar" } else { "$output/state-tests.jar" }
& java -cp $compiler org.jetbrains.kotlin.cli.jvm.K2JVMCompiler -no-stdlib -no-reflect -jvm-target 17 -classpath $classpath -d $target @sources
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
if ($Platform) { Write-Output 'Android-dependent contract tests compiled; not executed (device required).'; exit 0 }
& java -cp "$output/state-tests.jar;$stdlib;$junit" junit.textui.TestRunner com.bluegrass.client.vpn.VpnControllerTest
exit $LASTEXITCODE
