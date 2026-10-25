; Установщик UWDT для Windows.
; Сборка (из корня репозитория, после сборки build/bin.noindex/UWDT.exe):
;   LC_ALL=en_US.UTF-8 makensis -INPUTCHARSET UTF8 -DVERSION=1.6.5 build/windows/installer/uwdt.nsi
; (brew install makensis; без UTF-8 локали makensis не читает кириллицу)
; Результат: build/bin.noindex/UWDT-setup-windows-amd64.exe
; Имя без .zip, чтобы автообновление по-прежнему брало зип с голым exe.

Unicode true
!include "MUI2.nsh"
!include "x64.nsh"

!ifndef VERSION
  !error "передайте -DVERSION=x.y.z"
!endif

!define APP      "UWDT"
!define EXE      "UWDT.exe"
!define ROOT     "..\..\.."
!define UNINSTKEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APP}"

Name "${APP} ${VERSION}"
OutFile "${ROOT}\build\bin.noindex\UWDT-setup-windows-amd64.exe"
InstallDir "$PROGRAMFILES64\${APP}"
InstallDirRegKey HKLM "${UNINSTKEY}" "InstallLocation"
RequestExecutionLevel admin ; приложению всё равно нужны права администратора (маршруты, Wintun)
SetCompressor /SOLID lzma
BrandingText "${APP} ${VERSION}"

VIProductVersion "${VERSION}.0"
VIAddVersionKey "ProductName" "${APP}"
VIAddVersionKey "ProductVersion" "${VERSION}"
VIAddVersionKey "FileVersion" "${VERSION}"
VIAddVersionKey "FileDescription" "Установщик ${APP}"
VIAddVersionKey "LegalCopyright" "GPL-3.0"

!define MUI_ICON   "${ROOT}\assets\icons\icon.ico"
!define MUI_UNICON "${ROOT}\assets\icons\icon.ico"
!define MUI_ABORTWARNING
!define MUI_FINISHPAGE_RUN "$INSTDIR\${EXE}"
!define MUI_FINISHPAGE_RUN_TEXT "Запустить ${APP}"

!insertmacro MUI_PAGE_LICENSE "${ROOT}\LICENSE"
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "Russian"

Function .onInit
  ${IfNot} ${RunningX64}
    MessageBox MB_ICONSTOP "${APP} работает только на 64-битной Windows."
    Abort
  ${EndIf}
  SetRegView 64
FunctionEnd

Function un.onInit
  SetRegView 64
FunctionEnd

; Запущенный UWDT держит exe и wintun.dll — закрываем перед заменой
!macro CloseApp
  nsExec::Exec 'taskkill /F /IM "${EXE}"'
  Pop $0
  Sleep 1000
!macroend

Section "UWDT" SecMain
  SectionIn RO
  !insertmacro CloseApp
  SetShellVarContext all
  SetOutPath "$INSTDIR"
  File "${ROOT}\build\bin.noindex\${EXE}"
  Delete "$INSTDIR\${EXE}.old"

  WriteUninstaller "$INSTDIR\uninstall.exe"
  CreateShortcut "$SMPROGRAMS\${APP}.lnk" "$INSTDIR\${EXE}"
  CreateShortcut "$DESKTOP\${APP}.lnk" "$INSTDIR\${EXE}"

  WriteRegStr   HKLM "${UNINSTKEY}" "DisplayName" "${APP}"
  WriteRegStr   HKLM "${UNINSTKEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr   HKLM "${UNINSTKEY}" "DisplayIcon" "$INSTDIR\${EXE}"
  WriteRegStr   HKLM "${UNINSTKEY}" "Publisher" "uslulu"
  WriteRegStr   HKLM "${UNINSTKEY}" "URLInfoAbout" "https://github.com/uslulu/uwdt"
  WriteRegStr   HKLM "${UNINSTKEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr   HKLM "${UNINSTKEY}" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegStr   HKLM "${UNINSTKEY}" "QuietUninstallString" '"$INSTDIR\uninstall.exe" /S'
  WriteRegDWORD HKLM "${UNINSTKEY}" "NoModify" 1
  WriteRegDWORD HKLM "${UNINSTKEY}" "NoRepair" 1
SectionEnd

; Настройки и серверы (%APPDATA%\pwdtt) не трогаем — переустановка их сохранит
Section "Uninstall"
  !insertmacro CloseApp
  SetShellVarContext all
  Delete "$SMPROGRAMS\${APP}.lnk"
  Delete "$DESKTOP\${APP}.lnk"
  Delete "$INSTDIR\${EXE}"
  Delete "$INSTDIR\${EXE}.old"
  Delete "$INSTDIR\${EXE}.new"
  Delete "$INSTDIR\wintun.dll" ; приложение распаковывает его рядом с exe при запуске
  Delete "$INSTDIR\uninstall.exe"
  RMDir "$INSTDIR"
  DeleteRegKey HKLM "${UNINSTKEY}"
SectionEnd
