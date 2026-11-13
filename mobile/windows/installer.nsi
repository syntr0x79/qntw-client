Unicode true
!include "MUI2.nsh"
!include "LogicLib.nsh"
!include "WinVer.nsh"
Name "Quantway"
OutFile "${OUTPUT}"
InstallDir "$PROGRAMFILES64\Quantway"
RequestExecutionLevel admin
SetCompressor /SOLID zlib
Icon "${BUNDLE}/assets/quantway.ico"
UninstallIcon "${BUNDLE}/assets/quantway.ico"
VIProductVersion "0.7.1.0"
VIAddVersionKey "ProductName" "Quantway"
VIAddVersionKey "FileDescription" "Quantway Windows ${ARCH} installer"
VIAddVersionKey "FileVersion" "${VERSION}"
VIAddVersionKey "ProductVersion" "${VERSION}"
Var QctLogHandle
Var QctLogPath
!define MUI_ABORTWARNING
!define MUI_FINISHPAGE_TEXT "Quantway установлен. Откройте его из меню Пуск. При первом подключении VPN Windows один раз запросит разрешение администратора; до закрытия Quantway повторных запросов не будет."
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "Russian"
!insertmacro MUI_LANGUAGE "English"

!macro QctLog TEXT
 ${If} $QctLogHandle != ""
  FileWriteUTF16LE $QctLogHandle '${TEXT}$\r$\n'
 ${EndIf}
!macroend

; A /S failure must return to the caller rather than wait on an invisible
; Session 0 MessageBox. Logs contain setup steps/return codes, never credentials.
!macro QctFail CODE MESSAGE
 !insertmacro QctLog 'event=failure code=${CODE}'
 IfSilent +2
 MessageBox MB_OK|MB_ICONSTOP '${MESSAGE}$\r$\nЖурнал: $QctLogPath'
 SetErrorLevel 1
 Abort
!macroend

!macro DefineInitLog PREFIX MODE
Function ${PREFIX}QctInitLog
 System::Call 'kernel32::GetCurrentProcessId()i.r4'
 StrCpy $QctLogPath "$TEMP\Quantway-Setup-${ARCH}-$4.log"
 StrCpy $QctLogHandle ""
 ClearErrors
 FileOpen $QctLogHandle "$QctLogPath" w
 IfErrors qct_log_failed qct_log_opened
 qct_log_failed:
 IfSilent qct_log_abort qct_log_show
 qct_log_show:
 MessageBox MB_OK|MB_ICONSTOP "Не удалось создать журнал установки в $TEMP."
 qct_log_abort:
 SetErrorLevel 2
 Abort
 qct_log_opened:
 FileWriteByte $QctLogHandle 0xFF
 FileWriteByte $QctLogHandle 0xFE
 !insertmacro QctLog 'event=begin mode=${MODE} target=${ARCH} version=${VERSION} pid=$4 exedir=$EXEDIR temp=$TEMP'
 IfSilent qct_mode_silent qct_mode_interactive
 qct_mode_silent:
 !insertmacro QctLog 'event=mode silent=1'
 Goto qct_mode_done
 qct_mode_interactive:
 !insertmacro QctLog 'event=mode silent=0'
 qct_mode_done:
FunctionEnd
!macroend
!insertmacro DefineInitLog "" "install"
!insertmacro DefineInitLog "un." "uninstall"

!macro QctRun STEP TIMEOUT COMMAND MESSAGE
 !insertmacro QctLog 'event=exec_begin step=${STEP}'
 nsExec::ExecToStack /TIMEOUT=${TIMEOUT} '${COMMAND}'
 Pop $0
 Pop $1
 !insertmacro QctLog 'event=exec_end step=${STEP} exit=$0 output=$1'
 ${If} $0 != 0
  !insertmacro QctFail '${STEP}' '${MESSAGE}'
 ${EndIf}
!macroend

; OI/CI grants apply to this DIRECTORY only. Applying inheritance:r /T together
; with OI/CI grants leaves FILES with empty protected DACLs. Descendants must
; instead reset to inherit this known-good root DACL, then receive BA ownership.
!macro QctSecureRoot ROOT STEP
 !insertmacro QctRun '${STEP}.reset' 30000 '"$SYSDIR\icacls.exe" "${ROOT}" /reset /Q' 'Не удалось подготовить права каталога.'
 !insertmacro QctRun '${STEP}.grant' 30000 '"$SYSDIR\icacls.exe" "${ROOT}" /inheritance:r /grant:r "*S-1-5-18:(OI)(CI)F" "*S-1-5-32-544:(OI)(CI)F" "*S-1-5-32-545:(OI)(CI)RX" /Q' 'Не удалось защитить каталог.'
 !insertmacro QctRun '${STEP}.owner' 30000 '"$SYSDIR\icacls.exe" "${ROOT}" /setowner *S-1-5-32-544 /Q' 'Не удалось назначить владельца каталога.'
!macroend
!macro QctSecureDescendants ROOT STEP
 !insertmacro QctRun '${STEP}.inherit' 300000 '"$SYSDIR\icacls.exe" "${ROOT}\*" /reset /T /Q' 'Не удалось настроить наследование прав файлов.'
 !insertmacro QctRun '${STEP}.owner_all' 300000 '"$SYSDIR\icacls.exe" "${ROOT}" /setowner *S-1-5-32-544 /T /Q' 'Не удалось назначить владельца файлов.'
!macroend

!macro CheckIdle
 !insertmacro QctLog 'event=temp_begin'
 InitPluginsDir
 !insertmacro QctLog 'event=temp_created path=$PLUGINSDIR'
 SetOutPath "$PLUGINSDIR"
 ClearErrors
 File /oname=quantway-net-helper.exe "${BUNDLE}/bin/quantway-net-helper.exe"
 ${If} ${Errors}
  !insertmacro QctFail 'temp.extract' 'Не удалось подготовить помощник проверки.'
 ${EndIf}
 !insertmacro QctLog 'event=temp_helper_extracted'
 !insertmacro QctSecureRoot '$PLUGINSDIR' 'temp.root'
 !insertmacro QctSecureDescendants '$PLUGINSDIR' 'temp.children'
 SetOutPath "$SYSDIR"
 !insertmacro QctRun 'vpn.check_idle' 30000 '"$PLUGINSDIR\quantway-net-helper.exe" --check-idle' 'Отключите VPN и закройте Quantway перед установкой или удалением.'
 FindWindow $0 "" "Quantway"
 !insertmacro QctLog 'event=window_check hwnd=$0'
 ${If} $0 != 0
  !insertmacro QctFail 'gui.active' 'Закройте окно Quantway и запустите установку снова.'
 ${EndIf}
 !insertmacro QctLog 'event=preflight_complete'
!macroend

Function .onInit
 Call QctInitLog
 ${IfNot} ${AtLeastWin10}
  !insertmacro QctFail 'os.unsupported' 'Quantway требует Windows 10 или новее.'
 ${EndIf}
 StrCpy $1 0
 StrCpy $2 0
 StrCpy $3 0
 System::Call 'kernel32::IsWow64Process2(p -1, *i .r2, *i .r1)i.r3'
 !insertmacro QctLog 'event=architecture_query result=$3 process=$2 native=$1'
 ${If} $3 == 0
  !insertmacro QctFail 'architecture.unknown' 'Не удалось определить архитектуру Windows.'
 ${EndIf}
 IntOp $1 $1 & 0xffff
!if "${ARCH}" == "arm64"
 ${If} $1 != 43620
!else
 ${If} $1 != 34404
!endif
  !insertmacro QctFail 'architecture.mismatch' 'Этот установщик не соответствует архитектуре Windows. Выберите пакет ${ARCH}.'
 ${EndIf}
 SetRegView 64
 StrCpy $INSTDIR "$PROGRAMFILES64\Quantway"
 System::Call 'kernel32::GetFileAttributesW(w "$INSTDIR")i.r0'
 !insertmacro QctLog 'event=install_root path=$INSTDIR attributes=$0'
 ${If} $0 != -1
  IntOp $1 $0 & 0x400
  ${If} $1 != 0
   !insertmacro QctFail 'install_root.reparse' 'Каталог установки не может быть ссылкой.'
  ${EndIf}
  IfFileExists "$INSTDIR\*.*" 0 root_ok
  ReadRegStr $0 HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\Quantway" "InstallLocation"
  !insertmacro QctLog 'event=previous_install registry_path=$0'
  ${If} $0 != $INSTDIR
   !insertmacro QctFail 'install_root.unmanaged' 'Каталог Quantway уже существует и не принадлежит этому установщику. Переместите его перед установкой.'
  ${EndIf}
 ${EndIf}
 root_ok:
 !insertmacro CheckIdle
!ifdef QCT_PREFLIGHT_ONLY
 !insertmacro QctLog 'event=result status=preflight_success'
 SetErrorLevel 0
 Quit
!endif
FunctionEnd

Section "Quantway" SEC_MAIN
 !insertmacro QctLog 'event=install_begin'
 ClearErrors
 CreateDirectory "$INSTDIR"
 ${If} ${Errors}
  !insertmacro QctFail 'install_root.create' 'Не удалось создать каталог установки.'
 ${EndIf}
 !insertmacro QctSecureRoot '$INSTDIR' 'install.root'
 SetOutPath "$INSTDIR"
 ClearErrors
!ifndef QCT_PREFLIGHT_ONLY
 File /r "${BUNDLE}/*"
!endif
 ${If} ${Errors}
  !insertmacro QctFail 'install.extract' 'Не удалось записать файлы приложения.'
 ${EndIf}
 !insertmacro QctLog 'event=files_extracted'
 WriteUninstaller "$INSTDIR\Uninstall.exe"
 !insertmacro QctSecureDescendants '$INSTDIR' 'install.children'
 SetShellVarContext all
 CreateDirectory "$SMPROGRAMS\Quantway"
 CreateShortcut "$SMPROGRAMS\Quantway\Quantway.lnk" "$INSTDIR\quantway.exe" "" "$INSTDIR\assets\quantway.ico"
 WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\Quantway" "DisplayName" "Quantway"
 WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\Quantway" "DisplayVersion" "${VERSION}"
 WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\Quantway" "Publisher" "Quantway"
 WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\Quantway" "InstallLocation" "$INSTDIR"
 WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\Quantway" "UninstallString" '$\"$INSTDIR\Uninstall.exe$\"'
 WriteRegDWORD HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\Quantway" "NoModify" 1
 WriteRegDWORD HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\Quantway" "NoRepair" 1
 !insertmacro QctLog 'event=install_registered'
SectionEnd
Function .onInstSuccess
 !insertmacro QctLog 'event=result status=success'
 SetErrorLevel 0
FunctionEnd
Function .onInstFailed
 !insertmacro QctLog 'event=result status=failed'
 SetErrorLevel 1
FunctionEnd

Function un.onInit
 Call un.QctInitLog
 SetRegView 64
 StrCpy $INSTDIR "$PROGRAMFILES64\Quantway"
 !insertmacro CheckIdle
FunctionEnd
Section "Uninstall"
 !insertmacro QctLog 'event=uninstall_begin'
 SetShellVarContext all
 Delete "$SMPROGRAMS\Quantway\Quantway.lnk"
 RMDir "$SMPROGRAMS\Quantway"
 DeleteRegKey HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\Quantway"
 ; Device keys and user sessions are outside Program Files and are preserved.
 RMDir /r "$INSTDIR"
 !insertmacro QctLog 'event=uninstall_complete'
SectionEnd
Function un.onUninstSuccess
 !insertmacro QctLog 'event=result status=success'
 SetErrorLevel 0
FunctionEnd
Function un.onUninstFailed
 !insertmacro QctLog 'event=result status=failed'
 SetErrorLevel 1
FunctionEnd
