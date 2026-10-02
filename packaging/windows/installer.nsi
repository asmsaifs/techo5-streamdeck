; NSIS installer. makensis -DVERSION=1.0.0 -DSRC=dist/win packaging/windows/installer.nsi
; SRC holds techo5-streamdeck.exe and deckcap-win.exe.
!include "MUI2.nsh"

!ifndef VERSION
  !define VERSION "0.0.0"
!endif
!ifndef SRC
  !define SRC "dist/win"
!endif

Name "TECHO5 Stream Deck"
OutFile "..\..\dist\TECHO5-Stream-Deck-Setup-v${VERSION}.exe"
InstallDir "$LOCALAPPDATA\Programs\TECHO5 Stream Deck"
; Per user: no administrator needed. The firewall rule is the one step that does, so it is asked for
; at the end and skipped when declined (Windows then asks on first start instead) and when the
; installer runs silently (/S, as the app's own update does): the rule from the first install stays.
RequestExecutionLevel user
Icon "..\icon.ico"
UninstallIcon "..\icon.ico"

!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"

Section "Install"
  SetOutPath "$INSTDIR"
  File "${SRC}\techo5-streamdeck.exe"
  File "${SRC}\deckcap-win.exe"
  WriteUninstaller "$INSTDIR\uninstall.exe"
  CreateShortCut "$SMPROGRAMS\TECHO5 Stream Deck.lnk" "$INSTDIR\techo5-streamdeck.exe"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\TECHO5StreamDeck" "DisplayName" "TECHO5 Stream Deck"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\TECHO5StreamDeck" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\TECHO5StreamDeck" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\TECHO5StreamDeck" "DisplayIcon" "$INSTDIR\techo5-streamdeck.exe"
  ; Inbound rule for the program on private networks, any port: the listen port is a setting.
  MessageBox MB_YESNO "Let your Echo Show reach the app through the Windows firewall (needs administrator approval)?" /SD IDNO IDNO nofw
  ExecShell "runas" "netsh" 'advfirewall firewall add rule name="TECHO5 Stream Deck" dir=in action=allow program="$INSTDIR\techo5-streamdeck.exe" profile=private enable=yes' SW_HIDE
  nofw:
SectionEnd

Section "Uninstall"
  ExecShell "runas" "netsh" 'advfirewall firewall delete rule name="TECHO5 Stream Deck"' SW_HIDE
  Delete "$INSTDIR\techo5-streamdeck.exe"
  Delete "$INSTDIR\deckcap-win.exe"
  Delete "$INSTDIR\uninstall.exe"
  RMDir "$INSTDIR"
  Delete "$SMPROGRAMS\TECHO5 Stream Deck.lnk"
  DeleteRegKey HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\TECHO5StreamDeck"
SectionEnd
