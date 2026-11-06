# Preserve setup-go/setup-python/flutter-action selections at the front. Repeated
# script calls must not duplicate registry PATHs beyond cmd.exe's environment limit.
$blueGrassPathEntries = [Collections.Generic.List[string]]::new()
$blueGrassSeenPaths = [Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
foreach ($blueGrassPathSource in @($env:Path,
    [Environment]::GetEnvironmentVariable('Path', 'Machine'),
    [Environment]::GetEnvironmentVariable('Path', 'User'))) {
    foreach ($blueGrassPathPart in ($blueGrassPathSource -split ';')) {
        $blueGrassPathPart = [Environment]::ExpandEnvironmentVariables($blueGrassPathPart).Trim().Trim('"')
        $blueGrassPathKey = $blueGrassPathPart.Replace('/', '\').TrimEnd('\')
        if ($blueGrassPathPart -and $blueGrassSeenPaths.Add($blueGrassPathKey)) {
            $blueGrassPathEntries.Add($blueGrassPathPart)
        }
    }
}
$env:Path = $blueGrassPathEntries -join ';'
