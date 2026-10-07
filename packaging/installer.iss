; Inno Setup script for SubsplashGenerator-Setup.exe. CI builds it from
; dist\ (see .github/workflows/release.yml):
;   iscc /DAppVersion=v1.2.3 /DNumericVersion=1.2.3.0 packaging\installer.iss
;
; Installs for the current user only, so it never needs an administrator.

#ifndef AppVersion
  #define AppVersion "dev"
#endif
#ifndef NumericVersion
  #define NumericVersion "0.0.0.0"
#endif
#define AppName "Subsplash Generator"
#define AppExe "SubsplashGenerator.exe"
#define Dist "..\dist"

[Setup]
AppId={{6E0B1F2C-5A7D-4C1B-9E3A-2F8D4B6C9A10}
AppName={#AppName}
AppVersion={#AppVersion}
AppVerName={#AppName} {#AppVersion}
AppPublisher=Summit Limestone
AppPublisherURL=https://github.com/summitlimestone/subsplash-generator-v2
VersionInfoVersion={#NumericVersion}
PrivilegesRequired=lowest
DefaultDirName={autopf}\{#AppName}
DisableDirPage=yes
DisableProgramGroupPage=yes
DisableReadyPage=yes
WizardStyle=modern
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
MinVersion=10.0
SetupIconFile=icon.ico
UninstallDisplayIcon={app}\{#AppExe}
UninstallDisplayName={#AppName}
Compression=lzma2/ultra64
SolidCompression=yes
OutputDir={#Dist}
OutputBaseFilename=SubsplashGenerator-Setup
CloseApplications=yes

[Tasks]
Name: desktopicon; Description: "Create a &desktop shortcut"

[Files]
Source: "{#Dist}\{#AppExe}"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#Dist}\LICENSE.txt"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#Dist}\THIRD_PARTY_NOTICES.txt"; DestDir: "{app}"; Flags: ignoreversion
; Not emptied first: a failed upgrade rolls back what it copied but not what
; it deleted. Libraries a newer ffmpeg no longer uses are harmless, and the
; uninstaller remembers and removes them too.
Source: "{#Dist}\ffmpeg\*"; DestDir: "{app}\ffmpeg"; Flags: ignoreversion recursesubdirs
Source: "MicrosoftEdgeWebview2Setup.exe"; Flags: dontcopy

[Icons]
Name: "{autoprograms}\{#AppName}"; Filename: "{app}\{#AppExe}"
Name: "{autodesktop}\{#AppName}"; Filename: "{app}\{#AppExe}"; Tasks: desktopicon

[Run]
Filename: "{app}\{#AppExe}"; Description: "Start {#AppName}"; Flags: nowait postinstall skipifsilent

[Code]
const
  WebView2Key = 'Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}';

function HasVersion(Root: Integer; Key: String): Boolean;
var
  V: String;
begin
  Result := RegQueryStringValue(Root, Key, 'pv', V) and (V <> '') and (V <> '0.0.0.0');
end;

// The app's window is Microsoft Edge WebView2, part of Windows 11 and of
// almost every Windows 10. Where it's missing, Microsoft's small
// bootstrapper downloads and installs it.
function WebView2Installed: Boolean;
begin
  Result := HasVersion(HKLM, 'SOFTWARE\WOW6432Node\' + WebView2Key) or
    HasVersion(HKLM, 'SOFTWARE\' + WebView2Key) or
    HasVersion(HKCU, 'Software\' + WebView2Key);
end;

procedure CurStepChanged(CurStep: TSetupStep);
var
  Code: Integer;
begin
  if (CurStep <> ssPostInstall) or WebView2Installed then
    Exit;
  WizardForm.StatusLabel.Caption := 'Installing Microsoft Edge WebView2...';
  ExtractTemporaryFile('MicrosoftEdgeWebview2Setup.exe');
  if not Exec(ExpandConstant('{tmp}\MicrosoftEdgeWebview2Setup.exe'), '/silent /install', '',
      SW_HIDE, ewWaitUntilTerminated, Code) or (Code <> 0) then
    SuppressibleMsgBox('Microsoft Edge WebView2 couldn''t be installed, and ' +
      '{#AppName} needs it to show its window. Connect to the internet and ' +
      'run this setup again, or install "WebView2 Runtime" from Microsoft.',
      mbError, MB_OK, IDOK);
end;
