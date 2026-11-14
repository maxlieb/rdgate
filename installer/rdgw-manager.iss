; Inno Setup script for RD Gateway Manager.
;
; Produces a normal Windows installer with an Add/Remove Programs entry whose
; uninstaller calls the app's own teardown first. Without that, uninstalling
; would delete the files and leave the scheduled tasks registered and the
; machine still published to the internet - the worst possible leftover.
;
; Build:  iscc installer\rdgw-manager.iss
; Expects build\rdgw-manager.exe and build\rdgwctl.exe to exist.

#define AppName       "RD Gateway Manager"
#define AppVersion    "0.1.8"
#define AppPublisher  "Max Lieberman"
#define AppExe        "rdgw-manager.exe"
#define CtlExe        "rdgwctl.exe"

[Setup]
AppId={{8F3A1C62-5D47-4E2B-9A10-7C4E2F9B6D31}
AppName={#AppName}
AppVersion={#AppVersion}
AppPublisher={#AppPublisher}
DefaultDirName={autopf}\RD Gateway Manager
DefaultGroupName={#AppName}
UninstallDisplayName={#AppName}
UninstallDisplayIcon={app}\{#AppExe}
OutputDir=..\dist
OutputBaseFilename=RDGatewayManager-Setup-{#AppVersion}
Compression=lzma2
SolidCompression=yes
WizardStyle=modern

; Installs services and writes under ProgramData, so elevation is required
; rather than requested opportunistically.
PrivilegesRequired=admin

; The gateway is 64-bit only; rdpgw and Gitea publish no 32-bit Windows builds.
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
MinVersion=10.0

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "startup"; Description: "Start {#AppName} when I sign in"; GroupDescription: "Startup"

[Files]
Source: "..\build\{#AppExe}"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\build\{#CtlExe}"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\build\rdpgw.exe";         DestDir: "{app}"; Flags: ignoreversion
Source: "..\build\rdpgw-LICENSE.txt"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\README.md";       DestDir: "{app}"; Flags: ignoreversion isreadme

[Dirs]
; The config holds the OIDC client secret and the Gitea admin token, so the
; data directory is restricted to Administrators and SYSTEM. Users inherit
; nothing: this is not a per-user application.
Name: "{commonappdata}\RDGateway"; Permissions: admins-full system-full

[Icons]
Name: "{group}\{#AppName}";                 Filename: "{app}\{#AppExe}"
Name: "{group}\Uninstall {#AppName}";       Filename: "{uninstallexe}"
Name: "{autodesktop}\{#AppName}";           Filename: "{app}\{#AppExe}"; Tasks: startup

[Run]
Filename: "{app}\{#AppExe}"; Description: "Open {#AppName}"; Flags: nowait postinstall skipifsilent

[UninstallRun]
; Runs while the files still exist. Stops both scheduled tasks and withdraws
; the Funnel publication; data is kept unless the user asks otherwise below.
Filename: "{app}\{#CtlExe}"; Parameters: "uninstall"; Flags: runhidden; RunOnceId: "TeardownServices"

[Code]
var
  PurgePage: TInputOptionWizardPage;

procedure InitializeUninstallProgressForm();
begin
  { nothing: kept as a hook for future progress reporting }
end;

function InitializeUninstall(): Boolean;
var
  Response: Integer;
begin
  Result := True;
  Response := MsgBox(
    'Also delete your settings and the sign-in account?' + #13#10#13#10 +
    'This removes the account and its two-factor enrolment, so setting up ' +
    'again later means starting from scratch.' + #13#10#13#10 +
    'Choose No to keep them.',
    mbConfirmation, MB_YESNOCANCEL);

  if Response = IDCANCEL then
  begin
    Result := False;
    Exit;
  end;

  if Response = IDYES then
    RegWriteStringValue(HKLM, 'Software\RDGatewayManager', 'PurgeOnUninstall', '1')
  else
    RegDeleteValue(HKLM, 'Software\RDGatewayManager', 'PurgeOnUninstall');
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  Purge: String;
  ResultCode: Integer;
  DataDir: String;
begin
  if CurUninstallStep = usPostUninstall then
  begin
    DataDir := ExpandConstant('{commonappdata}\RDGateway');
    if RegQueryStringValue(HKLM, 'Software\RDGatewayManager', 'PurgeOnUninstall', Purge)
       and (Purge = '1') then
    begin
      DelTree(DataDir, True, True, True);
      RegDeleteKeyIncludingSubkeys(HKLM, 'Software\RDGatewayManager');
    end
    else
    begin
      { Say where it was left, rather than leaving people to wonder whether
        the uninstall was incomplete. }
      MsgBox('Your settings were kept in:' + #13#10 + DataDir,
             mbInformation, MB_OK);
    end;
  end;
end;
