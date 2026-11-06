param([switch]$Race)
$ErrorActionPreference = 'Stop'
$nativeRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\native\core'))
Push-Location $nativeRoot
try {
  $xray = & go list -m -f '{{.Version}}' github.com/xtls/xray-core
  if ($LASTEXITCODE -ne 0 -or $xray.Trim() -ne 'v1.260327.1-0.20261017152356-92ccc939aaf4') { throw 'The approved Xray-core pin is required.' }
  $dependencies = & go list -deps ./...
  if ($LASTEXITCODE -ne 0) { throw 'Unable to inspect native dependencies.' }
  if ($dependencies -match '^github.com/sagernet/sing-box(?:/|$)') { throw 'A second proxy core is still linked.' }
  $arguments = @('test', '-count=1')
  if ($Race) { $arguments += '-race' }
  & go @arguments ./...
  if ($LASTEXITCODE -ne 0) { throw 'Native tests failed.' }
  & go vet ./...
  if ($LASTEXITCODE -ne 0) { throw 'Native vet failed.' }
} finally { Pop-Location }
