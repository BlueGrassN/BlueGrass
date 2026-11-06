$ErrorActionPreference = 'Stop'
$clientRoot = [IO.Path]::GetFullPath((Split-Path $PSScriptRoot -Parent))
$distRoot = [IO.Path]::GetFullPath((Join-Path $clientRoot 'dist'))
$archive = Join-Path $distRoot 'BlueGrass-Windows-x64-dev.zip'
$expanded = [IO.Path]::GetFullPath((Join-Path $distRoot 'BlueGrass-Windows-x64'))
if (!(Test-Path -LiteralPath $archive -PathType Leaf)) { throw "Client archive is missing: $archive" }

# Delete only the fixed extracted bundle inside this client's dist directory.
if ([IO.Path]::GetDirectoryName($expanded) -cne $distRoot) {
    throw 'The extracted bundle must be directly inside Windows/dist.'
}
if (Test-Path -LiteralPath $expanded) {
    $oldBundle = Get-Item -LiteralPath $expanded -Force
    if (!$oldBundle.PSIsContainer -or ($oldBundle.Attributes -band [IO.FileAttributes]::ReparsePoint)) {
        throw "Expected an ordinary extracted bundle directory: $expanded"
    }
    # Reject before deleting any file, rather than discovering a locked EXE
    # after part of the previous bundle has already been removed.
    foreach ($process in @(Get-Process -Name 'BlueGrass*' -ErrorAction SilentlyContinue)) {
        $image = $null
        try { $image = $process.Path } catch { continue }
        if ($image -and $image.StartsWith($expanded + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
            throw "Close the client from the extracted bundle before replacing it (PID $($process.Id))."
        }
    }
    Remove-Item -LiteralPath $expanded -Recurse -Force
}
Expand-Archive -LiteralPath $archive -DestinationPath $expanded
if (!(Test-Path -LiteralPath (Join-Path $expanded 'BlueGrass.exe') -PathType Leaf)) {
    throw 'The extracted bundle is missing BlueGrass.exe.'
}
if (!(Test-Path -LiteralPath (Join-Path $expanded 'runtime/BlueGrass.exe') -PathType Leaf)) {
    throw 'The extracted bundle is missing the runtime client.'
}
if (@(Get-ChildItem -LiteralPath $expanded -Force | Where-Object { $_.Name -notin @('BlueGrass.exe', 'runtime') }).Count) {
    throw 'Unexpected files at the portable bundle root.'
}
Write-Host "Extracted client: $expanded"
