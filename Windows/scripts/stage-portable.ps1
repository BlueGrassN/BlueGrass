param([Parameter(Mandatory)][string]$Bundle)
$ErrorActionPreference = 'Stop'
$clientRoot = [IO.Path]::GetFullPath((Split-Path $PSScriptRoot -Parent))
$bundlePath = (Resolve-Path -LiteralPath $Bundle).Path
$launcher = Join-Path $clientRoot '.artifacts/launcher/Release/BlueGrassLauncher.exe'
if (!(Test-Path -LiteralPath $launcher -PathType Leaf)) { throw 'Portable launcher is missing.' }
. (Join-Path $clientRoot 'installer/payload.ps1')
& "$PSScriptRoot\prepare-wintun.ps1" -Destination $bundlePath -VerifyOnly
& "$PSScriptRoot\verify-native-dependencies.ps1" -BinaryDirectory $bundlePath
foreach ($name in $RequiredPayload) {
    if (!(Test-Path -LiteralPath (Join-Path $bundlePath $name) -PathType Leaf)) {
        throw "Required payload missing: $name"
    }
}

# Reuse the maintained runtime inventory; never ship linker libraries, symbols,
# test bindings or arbitrary files left in the Flutter build directory.
$files = @()
foreach ($name in $RequiredPayload + $OptionalPayload) {
    $source = Join-Path $bundlePath $name
    if (Test-Path -LiteralPath $source -PathType Leaf) {
        $item = Get-Item -LiteralPath $source
        if ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw "Unexpected linked payload: $name" }
        $files += [PSCustomObject]@{ Name=$name; Source=$source }
    }
}
$artifactRoot = [IO.Path]::GetFullPath((Join-Path $clientRoot '.artifacts'))
$stage = [IO.Path]::GetFullPath((Join-Path $artifactRoot 'portable-bundle'))
if ([IO.Path]::GetDirectoryName($stage) -cne $artifactRoot) { throw 'Invalid portable staging directory.' }
if (Test-Path -LiteralPath $stage) {
    $old = Get-Item -LiteralPath $stage -Force
    if (!$old.PSIsContainer -or ($old.Attributes -band [IO.FileAttributes]::ReparsePoint)) { throw 'Expected an ordinary staging directory.' }
    Remove-Item -LiteralPath $stage -Recurse -Force
}
$runtime = Join-Path $stage 'runtime'
New-Item -ItemType Directory -Path $runtime -Force | Out-Null
foreach ($file in $files) {
    $destination = Join-Path $runtime $file.Name
    New-Item -ItemType Directory -Path (Split-Path $destination -Parent) -Force | Out-Null
    Copy-Item -LiteralPath $file.Source -Destination $destination
}
Copy-Item -LiteralPath $launcher -Destination (Join-Path $stage 'BlueGrass.exe')
& "$PSScriptRoot\prepare-wintun.ps1" -Destination $runtime -VerifyOnly
Write-Output $stage
