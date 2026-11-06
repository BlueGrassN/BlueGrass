param(
  [string]$GoExecutable = 'D:\Program Files\Go\bin\go.exe',
  [string]$OutputPath = ''
)

$ErrorActionPreference = 'Stop'
$moduleRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\tools\network-probe'))
$outputDir = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\.artifacts\live-functional-20261024'))
$output = Join-Path $outputDir 'network-probe'
if ($OutputPath) {
  $output = [IO.Path]::GetFullPath($OutputPath)
  $outputDir = Split-Path -Parent $output
}
$runCache = Join-Path $moduleRoot ('.cache\build-' + [guid]::NewGuid().ToString('N'))
$cacheDir = Join-Path $runCache 'go-build'
$tempDir = Join-Path $runCache 'tmp'
$savedEnvironment = @{}
$envNames = @('GOCACHE','GOTMPDIR','GOWORK','GOTOOLCHAIN','GOENV','GOFLAGS','GOEXPERIMENT','GOOS','GOARCH','GOAMD64','CGO_ENABLED','GOPROXY','GOSUMDB')
foreach ($name in $envNames) { $savedEnvironment[$name] = [Environment]::GetEnvironmentVariable($name, 'Process') }

Push-Location $moduleRoot
try {
  $go = (Get-Command $GoExecutable -CommandType Application -ErrorAction Stop).Source
  New-Item -ItemType Directory -Force -Path $cacheDir, $tempDir, $outputDir | Out-Null
  $env:GOCACHE = $cacheDir
  $env:GOTMPDIR = $tempDir
  $env:GOWORK = 'off'
  $env:GOTOOLCHAIN = 'local'
  $env:GOENV = 'off'
  $env:GOFLAGS = ''
  $env:GOEXPERIMENT = ''
  $env:GOOS = $null
  $env:GOARCH = $null
  $env:GOAMD64 = 'v1'
  $env:CGO_ENABLED = '0'
  $env:GOPROXY = 'off'
  $env:GOSUMDB = 'off'
  & $go version
  if ($LASTEXITCODE -ne 0) { throw 'Go version check failed.' }
  & $go test -count=1 -timeout=60s ./...
  if ($LASTEXITCODE -ne 0) { throw 'Network probe unit tests failed.' }
  $env:GOOS = 'linux'
  $env:GOARCH = 'amd64'
  & $go vet ./...
  if ($LASTEXITCODE -ne 0) { throw 'Network probe Linux vet failed.' }
  & $go build -mod=readonly -trimpath -buildvcs=false -ldflags '-s -w' -o $output .
  if ($LASTEXITCODE -ne 0) { throw 'Network probe Linux build failed.' }

  # Verify the actual ELF header and program table: x86-64, no dynamic loader.
  $elf = [IO.File]::ReadAllBytes($output)
  if ($elf.Length -lt 64 -or $elf[0] -ne 127 -or $elf[1] -ne 69 -or $elf[2] -ne 76 -or $elf[3] -ne 70 -or $elf[4] -ne 2 -or $elf[5] -ne 1 -or [BitConverter]::ToUInt16($elf, 18) -ne 62) {
    throw 'Expected a little-endian x86-64 ELF executable.'
  }
  $programOffset = [BitConverter]::ToUInt64($elf, 32)
  $entrySize = [BitConverter]::ToUInt16($elf, 54)
  $entryCount = [BitConverter]::ToUInt16($elf, 56)
  if ($entrySize -lt 56 -or $entryCount -eq 0 -or ($programOffset + $entrySize * $entryCount) -gt $elf.Length) { throw 'Invalid ELF program table.' }
  for ($i = 0; $i -lt $entryCount; $i++) {
    $kind = [BitConverter]::ToUInt32($elf, [int]($programOffset + $i * $entrySize))
    if ($kind -eq 2 -or $kind -eq 3) { throw 'Executable has a dynamic section or interpreter.' }
  }
  & $go version -m $output
  if ($LASTEXITCODE -ne 0) { throw 'Unable to read Go build metadata.' }
  Write-Output ('Built static linux/amd64 ELF: ' + $output)
  Write-Output ('SHA256: ' + (Get-FileHash -LiteralPath $output -Algorithm SHA256).Hash.ToLowerInvariant())
}
finally {
  Pop-Location
  foreach ($name in $envNames) { [Environment]::SetEnvironmentVariable($name, $savedEnvironment[$name], 'Process') }
}
