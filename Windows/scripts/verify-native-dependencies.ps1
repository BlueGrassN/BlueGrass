param([string]$BinaryDirectory = '', [string]$Go = '')
$ErrorActionPreference = 'Stop'
$clientRoot = Split-Path $PSScriptRoot -Parent
$repoRoot = Split-Path $clientRoot -Parent
if (!$Go) { $Go = (Get-Command go -ErrorAction Stop).Source }
$corePin = (Get-Content -LiteralPath (Join-Path $clientRoot 'toolchain.json') -Raw | ConvertFrom-Json).embeddedCoreModule
if (!$corePin) { throw 'The approved Xray-core pin is required.' }
# Standalone sing utilities and bbolt are intentionally allowed.
$retired = '^(github\.com/sagernet/(sing-box|sing-tun|gvisor)(/|$)|gvisor\.dev(/|$))'
if (!$BinaryDirectory) {
    Push-Location (Join-Path $repoRoot 'core')
    try {
        $actualCore = & $Go list -mod=readonly -m -f '{{.Version}}{{if .Replace}} replaced{{end}}' github.com/xtls/xray-core
        if ($LASTEXITCODE -ne 0 -or "$actualCore".Trim() -cne $corePin) { throw 'The approved Xray-core pin is required.' }
        $dependencies = & $Go list -mod=readonly -deps ./cmd/...
        if ($LASTEXITCODE -ne 0) { throw 'Unable to inspect native dependencies.' }
        if ($dependencies -match $retired) { throw 'Retired sing-box/sing-tun/gVisor runtime dependency detected.' }
        if ($dependencies -cnotcontains 'github.com/xtls/xray-core/proxy/tun') { throw 'Xray native TUN is missing from native dependencies.' }
    } finally { Pop-Location }
    return
}
. (Join-Path $clientRoot 'installer/payload.ps1')
foreach ($name in $NativePayload) {
    $info = & $Go version -m (Join-Path $BinaryDirectory $name)
    if ($LASTEXITCODE -ne 0) { throw "Unable to inspect native binary: $name" }
    $hasPinnedCore = $false
    $previousModule = ''
    foreach ($line in $info) {
        $fields = $line.Trim() -split '\s+'
        if ($fields[0] -in @('dep', '=>') -and $fields.Count -ge 2) {
            if ($fields[1] -match $retired) { throw "Retired sing-box/sing-tun/gVisor dependency in $name" }
            if ($fields[0] -eq '=>' -and $previousModule -ceq 'github.com/xtls/xray-core') { throw 'Replaced Xray-core is not the approved upstream pin.' }
            if ($fields[0] -eq 'dep') {
                $previousModule = $fields[1]
                if ($fields[1] -ceq 'github.com/xtls/xray-core') {
                    if ($fields.Count -lt 3 -or $fields[2] -cne $corePin) { throw "Unapproved Xray-core in $name" }
                    $hasPinnedCore = $true
                }
            }
        }
    }
    $text = $info -join "`n"
    if ($text -notmatch '(?m)^\s*build\s+GOOS=windows\s*$' -or $text -notmatch '(?m)^\s*build\s+GOARCH=amd64\s*$') {
        throw "Native payload must target windows/amd64: $name"
    }
    if ($name -in @('BlueGrass.Native.exe', 'BlueGrass.Service.exe') -and !$hasPinnedCore) {
        throw "Missing pinned Xray engine: $name"
    }
}
