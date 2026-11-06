$env:Path = $env:Path + ';' + [Environment]::GetEnvironmentVariable('Path', 'Machine') + ';' +
    [Environment]::GetEnvironmentVariable('Path', 'User')
$Flutter = (Get-Command flutter -ErrorAction Stop).Source
$Dart = (Get-Command dart -ErrorAction Stop).Source

function Get-BlueGrassPython {
    $executable = if ($env:BLUEGRASS_PYTHON) { $env:BLUEGRASS_PYTHON } else {
        (Get-Command python -All -ErrorAction Stop | Where-Object {
            $_.Source -notmatch '[\\/]msys64[\\/]|[\\/]WindowsApps[\\/]'
        } | Select-Object -First 1).Source
    }
    if (!$executable -or !(Test-Path -LiteralPath $executable)) {
        throw 'Set BLUEGRASS_PYTHON to a CPython executable with contracts/requirements-test.txt installed.'
    }
    return $executable
}
