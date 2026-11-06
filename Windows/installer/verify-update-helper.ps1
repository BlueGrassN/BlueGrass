param([Parameter(Mandatory)][string]$Executable)
$ErrorActionPreference = 'Stop'
$exe = (Resolve-Path -LiteralPath $Executable).Path
if ([IO.Path]::GetFileName($exe) -cne 'BlueGrass.Update.exe') { throw 'Expected the actual update helper executable.' }
$mt = Get-ChildItem 'C:\Program Files (x86)\Windows Kits\10\bin\*\x64\mt.exe' -ErrorAction SilentlyContinue | Sort-Object FullName | Select-Object -Last 1
if (!$mt) { throw 'Windows SDK mt.exe required for read-only resource verification.' }
$manifest = Join-Path (Split-Path $exe -Parent) 'update-execution.manifest.xml'
& $mt.FullName '-nologo' ("-inputresource:" + $exe + ';#1') ("-out:" + $manifest)
if ($LASTEXITCODE -ne 0) { throw 'Update manifest extraction failed.' }
[xml]$xml = Get-Content -Raw -LiteralPath $manifest
if ($xml.SelectSingleNode("//*[local-name()='requestedExecutionLevel']").level -ne 'asInvoker') { throw 'Update helper must explicitly use asInvoker.' }
$metadata = @(& 'D:\Program Files\Go\bin\go.exe' version -m $exe)
if ($LASTEXITCODE -ne 0 -or !($metadata -match 'path\s+bluegrass/native/core/cmd/bluegrass-update')) { throw 'Expected compiled update command metadata.' }
# Only the built-in empty release configuration is exercised. Never invoke
# --apply, elevation, download or any setup/service/repair entry point here.
$start = [Diagnostics.ProcessStartInfo]::new()
$start.FileName = $exe
$start.ArgumentList.Add('--rpc')
$start.UseShellExecute = $false
$start.CreateNoWindow = $true
$start.RedirectStandardInput = $true
$start.RedirectStandardOutput = $true
$start.RedirectStandardError = $true
$process = [Diagnostics.Process]::new()
$process.StartInfo = $start
try {
    if (!$process.Start()) { throw 'Update smoke process did not start.' }
    $stdout = $process.StandardOutput.ReadToEndAsync()
    $stderr = $process.StandardError.ReadToEndAsync()
    $process.StandardInput.WriteLine('{"v":1,"id":"0123456789abcdef0123456789abcdef","op":"check"}')
    $process.StandardInput.Close()
    if (!$process.WaitForExit(15000)) { $process.Kill(); throw 'Update smoke exceeded 15 seconds.' }
    $text = $stdout.GetAwaiter().GetResult()
    $errors = $stderr.GetAwaiter().GetResult()
    if ($process.ExitCode -ne 0 -or $errors.Length -ne 0 -or $text.Length -gt 4096) { throw 'Unexpected update smoke process result.' }
    $reply = $text | ConvertFrom-Json
    if ($reply.v -ne 1 -or $reply.id -ne '0123456789abcdef0123456789abcdef' -or $reply.ok -ne $false -or $reply.error -cne 'UPDATE_UNCONFIGURED' -or $reply.snapshot) { throw 'Default release config must explicitly return UPDATE_UNCONFIGURED.' }
} finally { $process.Dispose() }
$result = [ordered]@{ Executable=$exe; SHA256=(Get-FileHash -LiteralPath $exe -Algorithm SHA256).Hash.ToLowerInvariant(); ExecutionLevel='asInvoker'; Smoke='UPDATE_UNCONFIGURED'; InstallOrApplyExecuted=$false; Distributable=$false; NativeBuildMetadata=$metadata }
$result | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath (Join-Path (Split-Path $exe -Parent) 'update-verification.json') -Encoding utf8
[PSCustomObject]$result | Select-Object Executable,SHA256,ExecutionLevel,Smoke,Distributable | ConvertTo-Json
