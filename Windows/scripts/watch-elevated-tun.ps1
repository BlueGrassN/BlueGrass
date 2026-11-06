<#
Internal, staged companion to test-elevated-tun.ps1. Do not invoke manually.
The watchdog is an independent elevated process, not a job/thread in its parent.
Only the compiled Recovery test owns WFP/adapter cleanup. This script never
repairs proxies, routes, DNS, services, or other processes.
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory)][ValidateSet('Supervisor', 'Watchdog', 'Snapshot')][string]$Role,
    [Parameter(Mandatory)][string]$RunDirectory,
    [ValidateSet('before', 'after')][string]$SnapshotPhase = 'before'
)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$RunDirectory = [IO.Path]::GetFullPath($RunDirectory).TrimEnd('\', '/')
$manifest = Get-Content -LiteralPath (Join-Path $RunDirectory 'manifest.json') -Raw | ConvertFrom-Json
$shellPath = if ($PSVersionTable.PSVersion.Major -ge 6) { Join-Path $PSHOME 'pwsh.exe' } else { Join-Path $PSHOME 'powershell.exe' }
$testPath = Join-Path $RunDirectory 'BlueGrass.TunAcceptance.exe'
$helperPath = Join-Path $RunDirectory 'watch-elevated-tun.ps1'

function Write-State([string]$Name, $Value) {
    $path = Join-Path $RunDirectory $Name
    $temporary = $path + '.' + $PID + '.tmp'
    [IO.File]::WriteAllText($temporary, ($Value | ConvertTo-Json -Depth 20), [Text.UTF8Encoding]::new($false))
    # A parent polling JSON can briefly hold a read handle without delete sharing.
    for ($attempt = 0; $attempt -lt 8; $attempt++) {
        try {
            if (Test-Path -LiteralPath $path) { [IO.File]::Replace($temporary, $path, [NullString]::Value) }
            else { [IO.File]::Move($temporary, $path) }
            return
        } catch [IO.IOException] {
            if ($attempt -eq 7) { throw }
            Start-Sleep -Milliseconds 25
        }
    }
}
function Read-State([string]$Name) {
    $path = Join-Path $RunDirectory $Name
    if (Test-Path -LiteralPath $path) { Get-Content -LiteralPath $path -Raw | ConvertFrom-Json }
}
function ConvertTo-UtcInstant($Value) {
    # PowerShell 7 can deserialize ISO JSON timestamps into DateTime. Preserve
    # that object's Kind instead of coercing it to a culture-specific string.
    if ($Value -is [DateTime]) { return $Value.ToUniversalTime() }
    if ($Value -is [DateTimeOffset]) { return $Value.UtcDateTime }
    [DateTime]::Parse([string]$Value, [Globalization.CultureInfo]::InvariantCulture,
        [Globalization.DateTimeStyles]::RoundtripKind).ToUniversalTime()
}
function Quote-Argument([string]$Value) {
    if ($Value -match '["\r\n]' -or $Value.EndsWith('\')) { throw 'Unsupported process argument.' }
    '"' + $Value + '"'
}
function Get-Identity($Process) {
    Initialize-NativeLauncher
    $null = $Process.Handle # Pin the handle before examining identity or terminating.
    [pscustomobject]@{
        pid = $Process.Id; executablePath = [BlueGrassAcceptance.Child]::ImagePath($Process)
        startTimeUtcTicks = $Process.StartTime.ToUniversalTime().Ticks.ToString()
    }
}
function Get-ExactProcess($Identity) {
    if (!$Identity) { return $null }
    $process = Get-Process -Id ([int]$Identity.pid) -ErrorAction SilentlyContinue
    if (!$process) { return $null }
    try {
        $actual = Get-Identity $process
        if ($actual.executablePath -ine $Identity.executablePath -or
            $actual.startTimeUtcTicks -cne $Identity.startTimeUtcTicks) {
            $process.Dispose()
            # The original process is gone. Its reused PID is never a target.
            return $null
        }
        return $process
    } catch {
        # A process may exit between Get-Process and opening/querying its handle.
        try {
            if ($process.HasExited) { $process.Dispose(); return $null }
        } catch {
            if (!(Get-Process -Id ([int]$Identity.pid) -ErrorAction SilentlyContinue)) {
                $process.Dispose()
                return $null
            }
        }
        $process.Dispose()
        throw
    }
}
function Stop-ExactProcess($Identity) {
    $process = Get-ExactProcess $Identity
    if (!$process) { return }
    try {
        if (!$process.HasExited) { $process.Kill() }
        if (!$process.WaitForExit(2000)) { throw 'Exact child did not terminate.' }
    } finally { $process.Dispose() }
}
function Assert-Prepared {
    $principal = [Security.Principal.WindowsPrincipal]::new([Security.Principal.WindowsIdentity]::GetCurrent())
    if (!$principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        throw 'The harness requires an elevated administrator token.'
    }
    if (![Environment]::Is64BitProcess -or
        [Security.Principal.WindowsIdentity]::GetCurrent().User.Value -cne $manifest.initiatingUserSid) {
        throw 'Elevation must retain the initiating user SID in a 64-bit process.'
    }
    if ($manifest.schemaVersion -ne 1 -or $manifest.runDirectory -ine $RunDirectory -or
        $manifest.powershell -ine $shellPath -or $manifest.maxSeconds -lt 180 -or $manifest.maxSeconds -gt 240 -or
        [IO.Path]::GetFileName($RunDirectory) -cne $manifest.runId -or
        $PSCommandPath -ine $helperPath) { throw 'Invalid run identity.' }
    $current = $RunDirectory
    while ($current) {
        if ((Get-Item -LiteralPath $current -Force).Attributes -band [IO.FileAttributes]::ReparsePoint) {
            throw 'The run directory contains a reparse point.'
        }
        $current = [IO.Path]::GetDirectoryName($current)
    }
    foreach ($name in @('BlueGrass.TunAcceptance.exe', 'BlueGrass.TunProbe.exe', 'wintun.dll', 'LICENSE.wintun', 'watch-elevated-tun.ps1')) {
        $entries = @($manifest.files | Where-Object { $_.name -ceq $name })
        $path = Join-Path $RunDirectory $name
        if ($entries.Count -ne 1 -or ((Get-Item -LiteralPath $path -Force).Attributes -band [IO.FileAttributes]::ReparsePoint) -or
            (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash -cne $entries[0].sha256) {
            throw 'Prepared artifact integrity check failed.'
        }
    }
}
function Get-Digest($Value) {
    $bytes = [Text.Encoding]::UTF8.GetBytes((ConvertTo-Json -InputObject $Value -Depth 30 -Compress))
    $sha = [Security.Cryptography.SHA256]::Create()
    try { ([BitConverter]::ToString($sha.ComputeHash($bytes))).Replace('-', '').ToLowerInvariant() }
    finally { $sha.Dispose() }
}
function Get-RegistrySnapshot($Hive, $View, [string]$Subkey, [string[]]$Names) {
    $root = [Microsoft.Win32.RegistryKey]::OpenBaseKey($Hive, $View)
    try {
        $key = $root.OpenSubKey($Subkey, $false)
        if (!$key) { return [ordered]@{ key = "$Hive/$View/$Subkey"; exists = $false } }
        try {
            $values = @()
            $present = $key.GetValueNames()
            $selected = if ($Names) { $Names } else { $present }
            foreach ($name in ($selected | Sort-Object)) {
                $exists = $present -contains $name
                $value = $null
                $kind = $null
                if ($exists) {
                    $kind = $key.GetValueKind($name).ToString()
                    $value = $key.GetValue($name, $null, [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
                    if ($value -is [byte[]]) { $value = [Convert]::ToBase64String($value) }
                }
                $values += [ordered]@{ name = $name; exists = $exists; kind = $kind; value = $value }
            }
            return [ordered]@{ key = "$Hive/$View/$Subkey"; exists = $true; values = $values }
        } finally { $key.Dispose() }
    } finally { $root.Dispose() }
}
function Save-Snapshot {
    # Values, addresses and DNS names exist only in memory and are never logged.
    $proxy = @()
    $internet = 'Software\Microsoft\Windows\CurrentVersion\Internet Settings'
    $policy = 'Software\Policies\Microsoft\Windows\CurrentVersion\Internet Settings'
    $names = @('ProxyEnable', 'ProxyServer', 'ProxyOverride', 'AutoConfigURL', 'AutoDetect', 'ProxySettingsPerUser', 'MigrateProxy')
    foreach ($hive in @([Microsoft.Win32.RegistryHive]::CurrentUser, [Microsoft.Win32.RegistryHive]::LocalMachine)) {
        foreach ($view in @([Microsoft.Win32.RegistryView]::Registry64, [Microsoft.Win32.RegistryView]::Registry32)) {
            $proxy += Get-RegistrySnapshot $hive $view $internet $names
            $proxy += Get-RegistrySnapshot $hive $view "$internet\Connections" @()
            $proxy += Get-RegistrySnapshot $hive $view $policy @()
        }
    }
    $routes = @(Get-NetRoute -ErrorAction Stop |
        Select-Object AddressFamily, DestinationPrefix, InterfaceIndex, InterfaceAlias, NextHop, RouteMetric, Protocol, PolicyStore |
        Sort-Object AddressFamily, DestinationPrefix, InterfaceIndex, NextHop, RouteMetric, Protocol)
    $dns = @(Get-DnsClientServerAddress -ErrorAction Stop |
        Select-Object InterfaceIndex, InterfaceAlias, AddressFamily, ServerAddresses |
        Sort-Object InterfaceIndex, AddressFamily)
    $listeners = @(Get-NetTCPConnection -State Listen -ErrorAction Stop |
        Where-Object { $_.LocalPort -eq 10808 } | Select-Object -ExpandProperty OwningProcess -Unique | Sort-Object)
    $listenerIdentities = @()
    foreach ($listener in $listeners) {
        $process = Get-Process -Id $listener -ErrorAction Stop
        try {
            $null = $process.Handle
            $listenerIdentities += [ordered]@{ pid = $process.Id; startTimeUtcTicks = $process.StartTime.ToUniversalTime().Ticks.ToString() }
        } finally { $process.Dispose() }
    }
    Write-State "baseline.$SnapshotPhase.json" ([ordered]@{
        schemaVersion = 1; utc = [DateTime]::UtcNow.ToString('o')
        proxySha256 = Get-Digest $proxy; routeSha256 = Get-Digest $routes; dnsSha256 = Get-Digest $dns
        routeCount = $routes.Count; dnsCount = $dns.Count; listener10808 = $listenerIdentities
        listener10808Sha256 = Get-Digest $listenerIdentities
    })
}

function Initialize-NativeLauncher {
    if ('BlueGrassAcceptance.Child' -as [type]) { return }
    # Create suspended so a worker cannot mutate the network before its exact PID
    # identity is durably published for the independent watchdog. A crash before
    # publication leaves at worst a suspended, never-executed child.
    Add-Type -TypeDefinition @'
using System;
using System.ComponentModel;
using System.Diagnostics;
using System.Runtime.InteropServices;
using System.Text;
namespace BlueGrassAcceptance {
 public sealed class Child : IDisposable {
  [StructLayout(LayoutKind.Sequential)] struct SA { public int size; public IntPtr security; public int inherit; }
  [StructLayout(LayoutKind.Sequential, CharSet=CharSet.Unicode)] struct SI {
   public int cb; public string reserved, desktop, title;
   public int x,y,xSize,ySize,xChars,yChars,fill,flags; public short show,reserved2;
   public IntPtr reservedPtr,stdin,stdout,stderr;
  }
  [StructLayout(LayoutKind.Sequential)] struct PI { public IntPtr process,thread; public int pid,tid; }
  [DllImport("kernel32.dll", CharSet=CharSet.Unicode, SetLastError=true)]
  static extern IntPtr CreateFile(string path,uint access,uint share,ref SA sa,uint creation,uint flags,IntPtr template);
  [DllImport("kernel32.dll", CharSet=CharSet.Unicode, SetLastError=true)]
  static extern bool CreateProcess(string app,StringBuilder command,IntPtr pa,IntPtr ta,bool inherit,uint flags,IntPtr env,string cwd,ref SI si,out PI pi);
  [DllImport("kernel32.dll", SetLastError=true)] static extern uint ResumeThread(IntPtr thread);
  [DllImport("kernel32.dll", SetLastError=true)] static extern bool TerminateProcess(IntPtr process,uint code);
  [DllImport("kernel32.dll", CharSet=CharSet.Unicode, SetLastError=true)]
  static extern bool QueryFullProcessImageName(IntPtr process,int flags,StringBuilder path,ref int size);
  [DllImport("kernel32.dll")] static extern bool CloseHandle(IntPtr handle);
  IntPtr processHandle,threadHandle;
  public Process Process { get; private set; }
  public static string ImagePath(Process process) {
   int length=32768; StringBuilder path=new StringBuilder(length);
   if(!QueryFullProcessImageName(process.Handle,0,path,ref length)) throw new Win32Exception();
   return path.ToString();
  }
  public static Child Start(string app,string arguments,string cwd,string stdout,string stderr) {
   SA sa = new SA { size=Marshal.SizeOf(typeof(SA)), inherit=1 };
   IntPtr output=IntPtr.Zero,error=IntPtr.Zero,input=IntPtr.Zero;
   PI pi=new PI();
   try {
    output=CreateFile(stdout,0x40000000,3,ref sa,2,0x80,IntPtr.Zero);
    error=CreateFile(stderr,0x40000000,3,ref sa,2,0x80,IntPtr.Zero);
    input=CreateFile("NUL",0x80000000,3,ref sa,3,0x80,IntPtr.Zero);
    if(output==new IntPtr(-1)||error==new IntPtr(-1)||input==new IntPtr(-1)) throw new Win32Exception();
    SI si=new SI {cb=Marshal.SizeOf(typeof(SI)),flags=0x101,show=0,stdin=input,stdout=output,stderr=error};
    if(!CreateProcess(app,new StringBuilder("\""+app+"\" "+arguments),IntPtr.Zero,IntPtr.Zero,true,
      0x08000004,IntPtr.Zero,cwd,ref si,out pi)) throw new Win32Exception();
    Child child=new Child {processHandle=pi.process,threadHandle=pi.thread,Process=Process.GetProcessById(pi.pid)};
    return child;
   } catch {
    if(pi.process!=IntPtr.Zero) { TerminateProcess(pi.process,93); CloseHandle(pi.process); }
    if(pi.thread!=IntPtr.Zero) CloseHandle(pi.thread);
    throw;
   } finally {
    foreach(IntPtr h in new[]{output,error,input}) if(h!=IntPtr.Zero&&h!=new IntPtr(-1)) CloseHandle(h);
   }
  }
  public void Resume() { if(ResumeThread(threadHandle)==0xffffffff) throw new Win32Exception(); }
  public void Abort() { if(processHandle!=IntPtr.Zero&&!Process.HasExited) TerminateProcess(processHandle,93); }
  public void Dispose() {
   if(threadHandle!=IntPtr.Zero) { CloseHandle(threadHandle); threadHandle=IntPtr.Zero; }
   if(processHandle!=IntPtr.Zero) { CloseHandle(processHandle); processHandle=IntPtr.Zero; }
   if(Process!=null) Process.Dispose();
  }
 }
}
'@
}
function Invoke-Test([string]$Phase, [string]$Test, [int]$TimeoutSeconds, [DateTime]$Deadline) {
    Initialize-NativeLauncher
    $child = $null
    try {
        if ([DateTime]::UtcNow -ge $Deadline) { throw 'No time remains for this test phase.' }
        $env:BLUEGRASS_ELEVATED_TUN_RUN = $RunDirectory
        $env:BLUEGRASS_ELEVATED_TUN_ACCOUNT = $manifest.accountFile
        $env:BLUEGRASS_ELEVATED_TUN_ROUTING = if ($manifest.PSObject.Properties['routingMode']) { $manifest.routingMode } else { 'global' }
        $child = [BlueGrassAcceptance.Child]::Start($testPath,
            "-test.run=$Test -test.v -test.count=1 -test.timeout=$($TimeoutSeconds)s", $RunDirectory,
            (Join-Path $RunDirectory "$Phase.stdout.log"), (Join-Path $RunDirectory "$Phase.stderr.log"))
        $identity = Get-Identity $child.Process
        $record = [ordered]@{ phase = $Phase; identity = $identity; utc = [DateTime]::UtcNow.ToString('o') }
        Write-State "$Phase.pid.json" $record
        Write-State 'worker.pid.json' $record
        $child.Resume()
        while (!$child.Process.WaitForExit(200)) {
            if ([DateTime]::UtcNow -ge $Deadline) {
                Stop-ExactProcess $identity
                Write-State "$Phase.result.json" @{ state = 'timeout'; exitCode = $null }
                return $null
            }
            if ($Role -eq 'Supervisor' -and $Phase -ne 'recovery') {
                $watchdog = Read-State 'watchdog.ready.json'
                $watchProcess = Get-ExactProcess $watchdog.identity
                if (!$watchProcess) { throw 'Independent watchdog exited before completion.' }
                $watchProcess.Dispose()
            }
        }
        $code = $child.Process.ExitCode
        Write-State "$Phase.result.json" @{ state = 'exited'; exitCode = $code }
        return $code
    } finally {
        if ($child) {
            # Normal exceptions must not leave a live worker ahead of recovery.
            $child.Abort()
            $child.Process.WaitForExit(2000) | Out-Null
            $child.Dispose()
        }
    }
}
function Invoke-Snapshot([string]$Phase, [DateTime]$Deadline) {
    if ([DateTime]::UtcNow -ge $Deadline) { throw 'No time remains for baseline capture.' }
    $arguments = @('-NoLogo', '-NoProfile', '-NonInteractive', '-File', (Quote-Argument $helperPath),
        '-Role', 'Snapshot', '-RunDirectory', (Quote-Argument $RunDirectory), '-SnapshotPhase', $Phase) -join ' '
    $process = Start-Process -FilePath $shellPath -ArgumentList $arguments -WindowStyle Hidden -PassThru `
        -RedirectStandardOutput (Join-Path $RunDirectory "snapshot.$Phase.stdout.log") `
        -RedirectStandardError (Join-Path $RunDirectory "snapshot.$Phase.stderr.log")
    $identity = Get-Identity $process
    Write-State 'snapshot.pid.json' @{ phase = $Phase; identity = $identity }
    try {
        while (!$process.WaitForExit(200)) {
            if ([DateTime]::UtcNow -ge $Deadline) { throw 'Baseline capture timed out.' }
        }
        if ($process.ExitCode -ne 0) { throw 'Baseline capture failed.' }
    } finally { Stop-ExactProcess $identity; $process.Dispose() }
}
function Get-Comparison {
    $before = Read-State 'baseline.before.json'
    $after = Read-State 'baseline.after.json'
    if (!$before -or !$after) { return @{ available = $false; unchanged = $false } }
    $proxySame = $before.proxySha256 -ceq $after.proxySha256
    $routeSame = $before.routeSha256 -ceq $after.routeSha256
    $dnsSame = $before.dnsSha256 -ceq $after.dnsSha256
    $listenerSame = $before.listener10808Sha256 -ceq $after.listener10808Sha256
    return [ordered]@{
        available = $true; proxyUnchanged = $proxySame; routesUnchanged = $routeSame
        dnsUnchanged = $dnsSame; listener10808Unchanged = $listenerSame
        unchanged = $proxySame -and $routeSame -and $dnsSame -and $listenerSame
    }
}
function Stop-RecordedWorker {
    $record = Read-State 'worker.pid.json'
    if ($record) {
        if ($record.identity.executablePath -ine $testPath) { throw 'Recorded worker has an unexpected executable path.' }
        Stop-ExactProcess $record.identity
    }
}
function Invoke-Supervisor {
    $started = [DateTime]::UtcNow
    $hardDeadline = $started.AddSeconds($manifest.maxSeconds)
    $takeover = $hardDeadline.AddSeconds(-35)
    Write-State 'supervisor.pid.json' @{
        identity = Get-Identity (Get-Process -Id $PID)
        startedUtc = $started.ToString('o'); hardDeadlineUtc = $hardDeadline.ToString('o')
        takeoverUtc = $takeover.ToString('o')
    }
    $acceptance = $null; $crashCheck = $null; $recovery = $null
    $errorType = $null; $watchReady = $false; $beforeCaptured = $false
    try {
        $arguments = @('-NoLogo', '-NoProfile', '-NonInteractive', '-File', (Quote-Argument $helperPath),
            '-Role', 'Watchdog', '-RunDirectory', (Quote-Argument $RunDirectory)) -join ' '
        $watch = Start-Process -FilePath $shellPath -ArgumentList $arguments -WindowStyle Hidden -PassThru `
            -RedirectStandardOutput (Join-Path $RunDirectory 'watchdog.stdout.log') `
            -RedirectStandardError (Join-Path $RunDirectory 'watchdog.stderr.log')
        $expectedWatch = Get-Identity $watch
        Write-State 'watchdog.pid.json' @{ identity = $expectedWatch }
        $readyDeadline = $started.AddSeconds(20)
        while (!(Test-Path -LiteralPath (Join-Path $RunDirectory 'watchdog.ready.json'))) {
            if ($watch.HasExited -or [DateTime]::UtcNow -ge $readyDeadline) { throw 'Watchdog did not become ready.' }
            Start-Sleep -Milliseconds 100
        }
        $ready = Read-State 'watchdog.ready.json'
        if ($ready.identity.pid -ne $expectedWatch.pid -or
            $ready.identity.startTimeUtcTicks -cne $expectedWatch.startTimeUtcTicks) { throw 'Watchdog ready identity mismatch.' }
        $watchReady = $true
        Write-State 'status.json' @{ state = 'baseline_before'; utc = [DateTime]::UtcNow.ToString('o') }
        Invoke-Snapshot 'before' ([DateTime]::UtcNow.AddSeconds(12))
        $beforeCaptured = $true
        Write-State 'status.json' @{ state = 'acceptance'; utc = [DateTime]::UtcNow.ToString('o') }
        # Leave 20s for crash-check, 30s for recovery, 12s for a snapshot,
        # and a small process-exit margin before the watchdog's reserved window.
        $acceptance = Invoke-Test 'acceptance' $manifest.acceptanceTest 150 ($takeover.AddSeconds(-65))
        if ($acceptance -eq 92 -and (Test-Path -LiteralPath (Join-Path $RunDirectory 'crash-ready.json'))) {
            Write-State 'status.json' @{ state = 'crash_check'; utc = [DateTime]::UtcNow.ToString('o') }
            $crashDeadline = [DateTime]::UtcNow.AddSeconds(20)
            if ($crashDeadline -gt $takeover.AddSeconds(-45)) { $crashDeadline = $takeover.AddSeconds(-45) }
            $crashCheck = Invoke-Test 'crashcheck' $manifest.crashCheckTest 20 $crashDeadline
        }
    } catch { $errorType = $_.Exception.GetType().Name }
    finally {
        try {
            Stop-RecordedWorker
            Write-State 'status.json' @{ state = 'recovery'; utc = [DateTime]::UtcNow.ToString('o') }
            # Recovery itself checks owned.json and the dead original acceptance PID.
            if ($watchReady) {
                $recoveryDeadline = [DateTime]::UtcNow.AddSeconds(30)
                if ($recoveryDeadline -gt $takeover.AddSeconds(-15)) { $recoveryDeadline = $takeover.AddSeconds(-15) }
                $recovery = Invoke-Test 'recovery' $manifest.recoveryTest 25 $recoveryDeadline
            }
            if ($beforeCaptured -and [DateTime]::UtcNow -lt $takeover) {
                $snapshotDeadline = [DateTime]::UtcNow.AddSeconds(12)
                if ($snapshotDeadline -gt $takeover.AddSeconds(-3)) { $snapshotDeadline = $takeover.AddSeconds(-3) }
                Invoke-Snapshot 'after' $snapshotDeadline
            }
        } catch { $errorType = $_.Exception.GetType().Name }
        $comparison = Get-Comparison
        $normalPassed = $acceptance -eq 0
        $crashPassed = $acceptance -eq 92 -and $crashCheck -eq 0
        $passed = ($normalPassed -or $crashPassed) -and $recovery -eq 0 -and $comparison.unchanged -and !$errorType
        $summary = [ordered]@{
            state = if ($passed) { 'passed' } else { 'failed' }; owner = 'supervisor'
            acceptanceExitCode = $acceptance; crashCheckExitCode = $crashCheck; recoveryExitCode = $recovery
            crashVerified = $crashPassed; comparison = $comparison; errorType = $errorType
            recoveryPending = $watchReady -and $recovery -ne 0
            utc = [DateTime]::UtcNow.ToString('o'); runDirectory = $RunDirectory
        }
        Write-State 'summary.json' $summary
        # A failed/unknown recovery is not completion: the watchdog must retry.
        if ($recovery -eq 0) {
            Write-State 'completed.json' @{ recoverySucceeded = $true; utc = [DateTime]::UtcNow.ToString('o') }
        }
        Write-State 'status.json' $summary
    }
}
function Invoke-Watchdog {
    $supervisor = Read-State 'supervisor.pid.json'
    if (!$supervisor) { throw 'Supervisor identity is missing.' }
    if ($supervisor.identity.executablePath -ine $shellPath) { throw 'Supervisor executable path is invalid.' }
    $hardDeadline = ConvertTo-UtcInstant $supervisor.hardDeadlineUtc
    $takeover = ConvertTo-UtcInstant $supervisor.takeoverUtc
    # Compile recovery launcher before announcing readiness.
    Initialize-NativeLauncher
    $ready = @{
        identity = Get-Identity (Get-Process -Id $PID); supervisorIdentity = $supervisor.identity
        readyUtc = [DateTime]::UtcNow.ToString('o'); takeoverUtc = $takeover.ToString('o')
        hardDeadlineUtc = $hardDeadline.ToString('o'); maxSeconds = $manifest.maxSeconds
    }
    # Publish the Go test's hyphenated alias before the supervisor's ready gate.
    Write-State 'watchdog-ready.json' $ready
    Write-State 'watchdog.ready.json' $ready
    $reason = 'supervisor_exited'
    try {
        while ($true) {
            if (Test-Path -LiteralPath (Join-Path $RunDirectory 'completed.json')) {
                Write-State 'watchdog.status.json' @{ state = 'completed'; recoveryOwner = 'supervisor'; utc = [DateTime]::UtcNow.ToString('o') }
                return
            }
            $process = Get-ExactProcess $supervisor.identity
            if (!$process) { break }
            $process.Dispose()
            if ([DateTime]::UtcNow -ge $takeover) { $reason = 'deadline'; break }
            Start-Sleep -Milliseconds 200
        }
    } catch { $reason = 'watchdog_monitor_failed' }
    $recovery = $null; $errorType = $null
    try {
        try {
            Write-State 'watchdog.status.json' @{ state = 'recovering'; reason = $reason; utc = [DateTime]::UtcNow.ToString('o') }
        } catch { } # A status-file failure must not prevent cleanup.
        # Stop the exact supervisor first so it cannot launch another worker or
        # race recovery. Never kill a process tree, a service, or the 10808 owner.
        Stop-ExactProcess $supervisor.identity
        Stop-RecordedWorker
        $snapshot = Read-State 'snapshot.pid.json'
        if ($snapshot) {
            if ($snapshot.identity.executablePath -ine $shellPath) { throw 'Snapshot executable path is invalid.' }
            Stop-ExactProcess $snapshot.identity
        }
        $recoveryDeadline = [DateTime]::UtcNow.AddSeconds(30)
        if ($recoveryDeadline -gt $hardDeadline.AddSeconds(-3)) { $recoveryDeadline = $hardDeadline.AddSeconds(-3) }
        $recovery = Invoke-Test 'watchdog-recovery' $manifest.recoveryTest 25 $recoveryDeadline
        if ([DateTime]::UtcNow -lt $hardDeadline.AddSeconds(-5)) {
            $snapshotDeadline = [DateTime]::UtcNow.AddSeconds(12)
            if ($snapshotDeadline -gt $hardDeadline.AddSeconds(-2)) { $snapshotDeadline = $hardDeadline.AddSeconds(-2) }
            Invoke-Snapshot 'after' $snapshotDeadline
        }
    } catch { $errorType = $_.Exception.GetType().Name }
    finally {
        $summary = [ordered]@{
            state = if ($recovery -eq 0) { 'recovered_after_failure' } else { 'recovery_failed' }
            owner = 'watchdog'; reason = $reason; recoveryExitCode = $recovery
            comparison = Get-Comparison; errorType = $errorType; recoveryPending = $false
            utc = [DateTime]::UtcNow.ToString('o'); runDirectory = $RunDirectory
        }
        Write-State 'summary.json' $summary
        Write-State 'status.json' $summary
        Write-State 'watchdog.status.json' $summary
        if ($recovery -eq 0) {
            Write-State 'completed.json' @{ recoverySucceeded = $true; recoveryOwner = 'watchdog'; utc = [DateTime]::UtcNow.ToString('o') }
        }
    }
}

try {
    Assert-Prepared
    switch ($Role) {
        'Snapshot' { Save-Snapshot }
        'Supervisor' { Invoke-Supervisor }
        'Watchdog' {
            $runMutex = [Threading.Mutex]::new($false, 'Global\BlueGrass.TunAcceptance')
            $acquired = $false
            try {
                try { $acquired = $runMutex.WaitOne(0) }
                catch [Threading.AbandonedMutexException] { $acquired = $true }
                if (!$acquired) { throw 'Another elevated acceptance run owns the watchdog lock.' }
                Invoke-Watchdog
            } finally {
                if ($acquired) { $runMutex.ReleaseMutex() }
                $runMutex.Dispose()
            }
        }
    }
} catch {
    Write-State "$($Role.ToLowerInvariant()).fatal.json" @{
        state = 'fatal'; errorType = $_.Exception.GetType().Name; utc = [DateTime]::UtcNow.ToString('o')
        scriptLine = $_.InvocationInfo.ScriptLineNumber
        operationCode = $Role.ToUpperInvariant() + '_FAILED'
        hResult = $_.Exception.HResult
        innerHResult = $_.Exception.GetBaseException().HResult
    }
    exit 1
}
