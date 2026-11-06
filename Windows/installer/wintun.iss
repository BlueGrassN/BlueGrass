{ Shared by the production installer and the isolated validator test harness.
  These are the unchanged official Wintun 0.14.1 amd64 DLL and the SHA256 of
  its verified WireGuard signer certificate DER, not the BlueGrass publisher. }
function WintunFileAttributes(Path: string): LongWord;
  external 'GetFileAttributesW@kernel32.dll stdcall';

function TrustedWintun(FileName, Verifier: string): Boolean;
var
  Version: string;
  Code: Integer;
begin
  Result := False;
  if not FileExists(FileName) then exit;
  if (WintunFileAttributes(FileName) and $400) <> 0 then exit;
  try
    if not GetVersionNumbersString(FileName, Version) then exit;
    if Version <> '0.14.1.0' then exit;
    if CompareText(GetSHA256OfFile(FileName),
      'e5da8447dc2c320edc0fc52fa01885c103de8c118481f683643cacc3220dafce') <> 0 then exit;
    { SetupTrust performs WinVerifyTrust and compares the actual signer DER
      fingerprint. A missing/untrusted vendor signature never falls back to
      the product signer. No Wintun entry point is invoked during validation. }
    Result := Exec(Verifier, '"' + FileName +
      '" "c9e1b3127c2f1312056d49a93ac4bd700393fd323d2bf3b2235aff52bea8d136"',
      ExtractFileDir(Verifier), SW_HIDE, ewWaitUntilTerminated, Code) and (Code = 0);
  except
    Result := False;
  end;
end;
