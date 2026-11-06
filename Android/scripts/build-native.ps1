param(
  [string]$AndroidSdk = $env:ANDROID_HOME,
  [string]$JavaHome = 'D:\Android\Android Studio\jbr',
  # The distributed Android client targets ARM64 phones only.
  [ValidateSet('arm64')][string[]]$Architecture = @('arm64'),
  [string]$GoExecutable = 'go'
)
$ErrorActionPreference = 'Stop'
if (-not $AndroidSdk) { $AndroidSdk = 'E:\AndroidTools\sdk' }
$nativeRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\native\core'))
$outputDir = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\android\app\libs'))
$architectures = @($Architecture | ForEach-Object { $_.ToLowerInvariant() } | Select-Object -Unique)
if ($architectures.Count -eq 0) { throw 'At least one architecture is required.' }
$abiNames = @{ arm64 = 'arm64-v8a' }
$xrayVersion = 'v1.260327.1-0.20261017152356-92ccc939aaf4'
$savedEnvironment = @{}
foreach ($name in @('Path','JAVA_HOME','ANDROID_HOME','ANDROID_SDK_ROOT','ANDROID_NDK_HOME','GOBIN','GOOS','GOARCH','CGO_ENABLED')) {
  $savedEnvironment[$name] = [Environment]::GetEnvironmentVariable($name, 'Process')
}
$stagingDir = $null
Push-Location $nativeRoot
try {
  $go = (Get-Command $GoExecutable -CommandType Application -ErrorAction Stop).Source
  $env:JAVA_HOME = $JavaHome
  $env:ANDROID_HOME = $AndroidSdk
  $env:ANDROID_SDK_ROOT = $AndroidSdk
  $env:ANDROID_NDK_HOME = Join-Path $AndroidSdk 'ndk\28.2.13676358'
  $env:GOOS = $null
  $env:GOARCH = $null
  $env:CGO_ENABLED = $null
  if (-not (Test-Path (Join-Path $JavaHome 'bin\javac.exe'))) { throw 'A JDK with javac is required.' }
  if (-not (Test-Path (Join-Path $env:ANDROID_NDK_HOME 'source.properties'))) { throw 'Required NDK 28.2.13676358 is missing.' }
  # go.mod requires Go 1.27; GOTOOLCHAIN=auto selects the matching compiler.
  $goVersion = & $go env GOVERSION
  if ($LASTEXITCODE -ne 0 -or $goVersion -notmatch '^go1\.(2[7-9]|[3-9][0-9])(?:\.|$)') { throw 'Xray v26.10.17 requires Go 1.27 or later.' }
  $xray = & $go list -mod=readonly -m -f '{{.Version}}' github.com/xtls/xray-core
  if ($LASTEXITCODE -ne 0 -or $xray.Trim() -ne $xrayVersion) { throw 'Incorrect Xray-core version.' }
  # Check the actual Android graph for every requested ABI, not the host graph.
  $env:GOOS = 'android'
  $env:CGO_ENABLED = '1'
  foreach ($arch in $architectures) {
    $env:GOARCH = $arch
    $dependencies = & $go list -mod=readonly -deps ./...
    if ($LASTEXITCODE -ne 0 -or $dependencies -match '^github.com/sagernet/sing-box(?:/|$)') { throw "Invalid native dependency graph for android/$arch." }
  }
  $env:GOOS = $null
  $env:GOARCH = $null
  $env:CGO_ENABLED = $null
  $goBin = (& $go env GOBIN).Trim()
  if (-not $goBin) { $goBin = Join-Path (((& $go env GOPATH).Trim() -split ';')[0]) 'bin' }
  $env:GOBIN = $goBin
  $env:Path = (Split-Path $go) + ';' + (Join-Path $JavaHome 'bin') + ';' + $goBin + ';' + $env:Path
  foreach ($toolName in @('gomobile','gobind')) {
    $toolPath = Join-Path $goBin "$toolName.exe"
    $version = if (Test-Path $toolPath) { & $go version -m $toolPath } else { '' }
    if (($version -join "`n") -notmatch 'github.com/sagernet/gomobile\s+v0\.1\.12\s' -or ($version -join "`n") -notmatch 'go1\.(2[7-9]|[3-9][0-9])') {
      & $go install "github.com/sagernet/gomobile/cmd/${toolName}@v0.1.12"
      if ($LASTEXITCODE -ne 0) { throw "Unable to install pinned $toolName." }
    }
  }
  New-Item -ItemType Directory -Force $outputDir | Out-Null
  $stagingDir = Join-Path $outputDir ('.native-build-' + [Guid]::NewGuid().ToString('N'))
  New-Item -ItemType Directory $stagingDir | Out-Null
  $aar = Join-Path $stagingDir 'bluegrass.aar'
  $targets = ($architectures | ForEach-Object { "android/$_" }) -join ','
  Write-Host "Building Xray $xrayVersion with $goVersion for $targets"
  # The native Xray TUN stack needs no sing-box feature build tags.
  # Xray's documented Android build needs this flag for anet's net.zoneCache
  # linkname on Go 1.23+. Dependency pins and checksum verification stay enabled.
  # Windows shells may inherit hidden drive entries such as '=E:=E:\'.
  # gomobile treats these as malformed names. Pass only normal variables to
  # this child; do not modify the user's environment or the installed tool.
  $nativeStart = [Diagnostics.ProcessStartInfo]::new((Join-Path $goBin 'gomobile.exe'))
  $nativeStart.UseShellExecute = $false
  $nativeStart.CreateNoWindow = $true
  $nativeStart.WorkingDirectory = $nativeRoot
  $nativeStart.RedirectStandardOutput = $true
  $nativeStart.RedirectStandardError = $true
  $nativeStart.Environment.Clear()
  foreach ($entry in [Environment]::GetEnvironmentVariables('Process').GetEnumerator()) {
    if ($entry.Key -and -not $entry.Key.Contains('=')) { $nativeStart.Environment[$entry.Key] = $entry.Value }
  }
  foreach ($argument in @('bind', '-target', $targets, '-androidapi', '26', '-javapkg', 'com.bluegrass.core', '-libname', 'bluegrass', '-trimpath', '-ldflags', '-s -w -checklinkname=0', '-o', $aar, '.')) {
    $nativeStart.ArgumentList.Add($argument)
  }
  $nativeProcess = [Diagnostics.Process]::Start($nativeStart)
  try {
    $nativeOutput = $nativeProcess.StandardOutput.ReadToEndAsync()
    $nativeError = $nativeProcess.StandardError.ReadToEndAsync()
    $nativeProcess.WaitForExit()
    Write-Host $nativeOutput.GetAwaiter().GetResult()
    Write-Host $nativeError.GetAwaiter().GetResult()
    if ($nativeProcess.ExitCode -ne 0) { throw 'Android Xray AAR build failed.' }
  } finally { $nativeProcess.Dispose() }
  # A failed or incomplete build must never replace the previous usable AAR.
  Add-Type -AssemblyName System.IO.Compression.FileSystem
  $zip = [IO.Compression.ZipFile]::OpenRead($aar)
  try {
    if (-not $zip.GetEntry('classes.jar')) { throw 'AAR is missing Java bindings.' }
    $actualLibraries = @($zip.Entries | Where-Object { $_.FullName -match '^jni/[^/]+/libbluegrass\.so$' } | ForEach-Object FullName)
    if ($actualLibraries.Count -ne 1 -or $actualLibraries[0] -ne 'jni/arm64-v8a/libbluegrass.so') {
      throw 'The Android AAR must contain only the ARM64 native library.'
    }
    foreach ($arch in $architectures) {
      $jniPath = "jni/$($abiNames[$arch])/libbluegrass.so"
      $entry = $zip.GetEntry($jniPath)
      if (-not $entry -or $entry.Length -eq 0) { throw "AAR is missing $jniPath." }
      $library = Join-Path $stagingDir "$arch.so"
      [IO.Compression.ZipFileExtensions]::ExtractToFile($entry, $library)
      $buildInfo = (& $go version -m $library) -join "`n"
      if ($LASTEXITCODE -ne 0 -or $buildInfo -notmatch "github.com/xtls/xray-core\s+$([regex]::Escape($xrayVersion))\s" -or $buildInfo -notmatch "GOARCH=$arch(?:\s|$)" -or $buildInfo -notmatch 'GOOS=android(?:\s|$)' -or $buildInfo -match 'github.com/sagernet/sing-box(?:\s|/)') {
        throw "Unexpected core version, dependency or architecture in $jniPath."
      }
      Write-Host "Verified $jniPath ($($entry.Length) bytes), Xray $xrayVersion"
    }
  } finally { $zip.Dispose() }
  Move-Item -LiteralPath (Join-Path $stagingDir 'bluegrass-sources.jar') -Destination (Join-Path $outputDir 'bluegrass-sources.jar') -Force
  Move-Item -LiteralPath $aar -Destination (Join-Path $outputDir 'bluegrass.aar') -Force
  Get-FileHash (Join-Path $outputDir 'bluegrass.aar') -Algorithm SHA256
} finally {
  Pop-Location
  foreach ($name in $savedEnvironment.Keys) {
    [Environment]::SetEnvironmentVariable($name, $savedEnvironment[$name], 'Process')
  }
  if ($stagingDir -and (Test-Path -LiteralPath $stagingDir)) {
    $stagingPath = [IO.Path]::GetFullPath($stagingDir)
    if ((Split-Path $stagingPath -Parent) -ne $outputDir -or (Split-Path $stagingPath -Leaf) -notlike '.native-build-*') { throw 'Refusing to remove unexpected staging path.' }
    Remove-Item -LiteralPath $stagingPath -Recurse -Force
  }
}
