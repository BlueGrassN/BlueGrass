; Fixed machine install. Compile candidates are intentionally non-installable.
; Release signing and VM install/upgrade/repair validation are separate gates.
#ifndef UnsignedCandidate
  #define UnsignedCandidate 1
#endif
#define IsCandidate Int(UnsignedCandidate)
#if (IsCandidate != 0) && (IsCandidate != 1)
  #error UnsignedCandidate must be 0 or 1
#endif
#ifndef StageDir
  #error StageDir is required
#endif
#ifndef PayloadInclude
  #error PayloadInclude is required
#endif

[Setup]
AppId={{D1C01EE2-9737-48A8-A760-77D9E13C3F8D}
AppName=BlueGrass
AppVersion={#AppVersion}
VersionInfoVersion={#VersionResource}
AppPublisher=BlueGrass
DefaultDirName={autopf}\BlueGrass
DisableDirPage=yes
UsePreviousAppDir=no
DefaultGroupName=BlueGrass
DisableProgramGroupPage=yes
PrivilegesRequired=admin
ArchitecturesAllowed=x64os
ArchitecturesInstallIn64BitMode=x64os
MinVersion=10.0.17763
OutputDir={#OutputDir}
#if IsCandidate
OutputBaseFilename=BlueGrass-{#AppVersion}-Windows-x64-unsigned-candidate
#else
OutputBaseFilename=BlueGrass-{#AppVersion}-Windows-x64
SignTool=bluegrass
SignedUninstaller=yes
SignedUninstallerDir={#OutputDir}\signed-uninstaller
SignToolRetryCount=0
SignToolRunMinimized=yes
#endif
Compression=lzma2
SolidCompression=yes
CloseApplications=no
RestartApplications=no
UninstallDisplayIcon={app}\BlueGrass.exe
SetupLogging=no
WizardStyle=modern

[Dirs]
Name: "{app}"; Permissions: system-full admins-full users-readexec

#include PayloadInclude

[Files]
Source: "{#StageDir}\BlueGrass.SetupTrust.exe"; Flags: dontcopy

[Icons]
Name: "{commonprograms}\BlueGrass"; Filename: "{app}\BlueGrass.exe"; WorkingDir: "{app}"; AppUserModelID: "com.bluegrass.client"; AppUserModelToastActivatorCLSID: "9221CCD9-CFE9-404B-BBB4-CDA9CA8BB484"
Name: "{group}\Network repair"; Filename: "{app}\BlueGrass.Repair.exe"; WorkingDir: "{app}"
Name: "{group}\Uninstall BlueGrass"; Filename: "{app}\BlueGrass.UserCleanup.exe"; Parameters: "--uninstall"; WorkingDir: "{app}"

[Registry]
Root: HKLM64; Subkey: "Software\Classes\CLSID\{{9221CCD9-CFE9-404B-BBB4-CDA9CA8BB484}\LocalServer32"; ValueType: string; ValueName: ""; ValueData: """{app}\BlueGrass.exe"" --toast-activated"; Flags: uninsdeletekey
Root: HKLM64; Subkey: "Software\Classes\AppUserModelId\com.bluegrass.client"; ValueType: string; ValueName: "DisplayName"; ValueData: "BlueGrass"; Flags: uninsdeletekey
Root: HKLM64; Subkey: "Software\Classes\AppUserModelId\com.bluegrass.client"; ValueType: string; ValueName: "CustomActivator"; ValueData: "{{9221CCD9-CFE9-404B-BBB4-CDA9CA8BB484}"

[Code]
type
  TServiceStatus = record
    ServiceType, CurrentState, ControlsAccepted, Win32ExitCode,
    ServiceSpecificExitCode, CheckPoint, WaitHint: LongWord;
  end;

function OpenSCManager(MachineName, DatabaseName: string; Access: LongWord): THandle;
  external 'OpenSCManagerW@advapi32.dll stdcall';
function OpenService(Manager: THandle; ServiceName: string; Access: LongWord): THandle;
  external 'OpenServiceW@advapi32.dll stdcall';
function CloseServiceHandle(Handle: THandle): Boolean;
  external 'CloseServiceHandle@advapi32.dll stdcall';
function QueryServiceStatus(Service: THandle; var Status: TServiceStatus): Boolean;
  external 'QueryServiceStatus@advapi32.dll stdcall';
function ControlService(Service: THandle; Control: LongWord; var Status: TServiceStatus): Boolean;
  external 'ControlService@advapi32.dll stdcall';
function StartService(Service: THandle; Count: LongWord; Arguments: NativeUInt): Boolean;
  external 'StartServiceW@advapi32.dll stdcall';
function ConvertSDDL(SDDL: string; Revision: LongWord; var Descriptor: NativeUInt; Size: NativeUInt): Boolean;
  external 'ConvertStringSecurityDescriptorToSecurityDescriptorW@advapi32.dll stdcall';
function SetFileSecurity(Path: string; Information: LongWord; Descriptor: NativeUInt): Boolean;
  external 'SetFileSecurityW@advapi32.dll stdcall';
function LocalFree(Memory: NativeUInt): NativeUInt;
  external 'LocalFree@kernel32.dll stdcall';
function GetFileAttributes(Path: string): LongWord;
  external 'GetFileAttributesW@kernel32.dll stdcall';

var
  TrustHelper: string;

function FixedPath: Boolean;
begin
  Result := CompareText(ExpandConstant('{app}'), ExpandConstant('{autopf}\BlueGrass')) = 0;
end;

function RunChecked(FileName, Arguments: string): Boolean;
var Code: Integer;
begin
  Result := Exec(FileName, Arguments, ExtractFileDir(FileName), SW_HIDE, ewWaitUntilTerminated, Code) and (Code = 0);
end;

function Trusted(FileName: string): Boolean;
begin
  Result := RunChecked(TrustHelper, '"' + FileName + '" "{#PublisherSHA256}"');
end;

#include "wintun.iss"

function ProtectTree(Path: string): Boolean;
var
  SD: NativeUInt;
  Find: TFindRec;
begin
  Result := False;
  { Reject junctions/symlinks before recursively setting owner and protected ACL. }
  if (GetFileAttributes(Path) and $400) <> 0 then exit;
  SD := 0;
  if not ConvertSDDL('O:BAG:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;GRGX;;;BU)', 1, SD, 0) then exit;
  try
    if not SetFileSecurity(Path, $80000007, SD) then exit;
  finally
    LocalFree(SD);
  end;
  if DirExists(Path) and FindFirst(Path + '\*', Find) then begin
    try
      repeat
        if (Find.Name <> '.') and (Find.Name <> '..') then
          if not ProtectTree(Path + '\' + Find.Name) then exit;
      until not FindNext(Find);
    finally
      FindClose(Find);
    end;
  end;
  Result := True;
end;

function SetServiceRunning(Start: Boolean): Boolean;
var
  Manager, Service: THandle;
  Status: TServiceStatus;
  Attempt: Integer;
  Desired: LongWord;
begin
  Result := False;
  Manager := OpenSCManager('', 'ServicesActive', $1);
  if Manager = 0 then exit;
  try
    Service := OpenService(Manager, 'BlueGrassService', $34);
    if Service = 0 then exit;
    try
      if not QueryServiceStatus(Service, Status) then exit;
      if Start then Desired := 4 else Desired := 1;
      if Status.CurrentState = Desired then begin Result := True; exit; end;
      if Start then begin
        if not StartService(Service, 0, 0) then exit;
      end else if not ControlService(Service, 1, Status) then exit;
      for Attempt := 1 to 120 do begin
        if not QueryServiceStatus(Service, Status) then exit;
        if Status.CurrentState = Desired then begin Result := True; exit; end;
        Sleep(250);
      end;
    finally CloseServiceHandle(Service); end;
  finally CloseServiceHandle(Manager); end;
end;

function InitializeSetup: Boolean;
begin
#if IsCandidate
  MsgBox('This unsigned compile candidate cannot install BlueGrass. It is not a distributable Beta.', mbError, MB_OK);
  Result := False;
#else
  ExtractTemporaryFile('BlueGrass.SetupTrust.exe');
  TrustHelper := ExpandConstant('{tmp}\BlueGrass.SetupTrust.exe');
  Result := Trusted(ExpandConstant('{srcexe}'));
#endif
end;

function PrepareToInstall(var NeedsRestart: Boolean): string;
var ServicePath: string;
begin
  Result := '';
  if not FixedPath then begin Result := 'BlueGrass requires the fixed Program Files directory.'; exit; end;
  if not ForceDirectories(ExpandConstant('{app}')) or not ProtectTree(ExpandConstant('{app}')) then begin
    Result := 'Cannot establish the required protected installation directory.'; exit;
  end;
  ServicePath := ExpandConstant('{app}\BlueGrass.Service.exe');
  if RegKeyExists(HKLM64, 'SYSTEM\CurrentControlSet\Services\BlueGrassService') and not FileExists(ServicePath) then begin
    Result := 'Installed service binary is missing. Use independent repair before reinstalling.'; exit;
  end;
  if FileExists(ServicePath) then begin
    if not Trusted(ServicePath) then begin Result := 'Existing service publisher verification failed.'; exit; end;
    if not RunChecked(ServicePath, 'maintenance') then begin Result := 'Maintenance acknowledgement failed; existing protection is retained.'; exit; end;
    if not SetServiceRunning(False) then begin Result := 'Service did not stop safely; protection is retained.'; exit; end;
  end;
end;

procedure CurStepChanged(Step: TSetupStep);
var ServicePath: string;
begin
  if Step = ssDone then begin
    if not RegWriteStringValue(HKLM64, 'Software\Microsoft\Windows\CurrentVersion\Uninstall\{D1C01EE2-9737-48A8-A760-77D9E13C3F8D}_is1', 'UninstallString', '"' + ExpandConstant('{app}\BlueGrass.UserCleanup.exe') + '" --uninstall') then RaiseException('Cannot register the original-user uninstall phase.');
    RegDeleteValue(HKLM64, 'Software\Microsoft\Windows\CurrentVersion\Uninstall\{D1C01EE2-9737-48A8-A760-77D9E13C3F8D}_is1', 'QuietUninstallString');
  end;
  if Step = ssPostInstall then begin
    if not FixedPath or not ProtectTree(ExpandConstant('{app}')) then RaiseException('Protected install directory verification failed.');
    if not TrustedWintun(ExpandConstant('{app}\wintun.dll'), TrustHelper) then
      RaiseException('Installed Wintun hash, version or vendor signature verification failed; service start is blocked.');
    ServicePath := ExpandConstant('{app}\BlueGrass.Service.exe');
    if not Trusted(ServicePath) then RaiseException('Installed service publisher verification failed.');
    if not RunChecked(ServicePath, 'install') then RaiseException('Service registration failed; protection is retained. Use independent repair.');
    if not SetServiceRunning(True) then RaiseException('Service start failed; protection is retained. Use independent repair.');
    { No GUI launch, no token/settings reset, and no business-session resume. }
  end;
end;

function InitializeUninstall: Boolean;
var ServicePath: string;
begin
  Result := False;
  if ExpandConstant('{param:USERPHASE|}') <> 'complete' then begin
    MsgBox('Run Uninstall BlueGrass from the original user desktop first. Current-user token and proxy cleanup cannot run under another administrator profile.', mbError, MB_OK); exit;
  end;
  if not FixedPath then exit;
  TrustHelper := ExpandConstant('{app}\BlueGrass.SetupTrust.exe');
  ServicePath := ExpandConstant('{app}\BlueGrass.Service.exe');
  if not Trusted(ServicePath) then exit;
  { Only explicit uninstall reaches this command. The original user phase
    handles tokens/proxy; service uninstall handles owned WFP/TUN and SCM. }
  Result := RunChecked(ServicePath, 'uninstall');
  if not Result then MsgBox('Cleanup failed. Files are retained; use the independent network repair tool.', mbError, MB_OK);
end;
