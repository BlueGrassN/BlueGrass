$env:Path = [Environment]::GetEnvironmentVariable('Path', 'Machine') + ';' +
    [Environment]::GetEnvironmentVariable('Path', 'User') + ';' + $env:Path
$Flutter = (Get-Command flutter -ErrorAction Stop).Source
$Dart = (Get-Command dart -ErrorAction Stop).Source
