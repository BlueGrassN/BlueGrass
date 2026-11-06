param(
  [string]$AndroidSdk = $env:ANDROID_HOME,
  [string]$JavaHome = $env:JAVA_HOME,
  [ValidateSet('arm64')][string[]]$Architecture = @('arm64'),
  [string]$GoExecutable = 'go'
)
$ErrorActionPreference = 'Stop'
# Keep the legacy entry point, but share all pins, checks and ABI defaults.
& (Join-Path $PSScriptRoot '..\scripts\build-native.ps1') -AndroidSdk $AndroidSdk -JavaHome $JavaHome -Architecture $Architecture -GoExecutable $GoExecutable
