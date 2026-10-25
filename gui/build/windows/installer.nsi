Unicode true
!include "MUI2.nsh"
!include "LogicLib.nsh"
!include "x64.nsh"

!ifndef APP_ID
!define APP_ID "FDUConnect"
!endif
!ifndef PRODUCT_NAME
!define PRODUCT_NAME "FDU Connect"
!endif
!ifndef DEPENDENCY_TIMEOUT
!define DEPENDENCY_TIMEOUT 300000
!endif
!ifndef RUNTIME_WAIT
!define RUNTIME_WAIT 10000
!endif
!define UNINSTALL_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APP_ID}"

Name "${PRODUCT_NAME}"
OutFile "${OUTPUT_FILE}"
InstallDir "$LOCALAPPDATA\Programs\${PRODUCT_NAME}"
InstallDirRegKey HKCU "${UNINSTALL_KEY}" "InstallLocation"
RequestExecutionLevel user
SetCompressor /SOLID lzma
AllowSkipFiles off
ShowInstDetails show
ShowUninstDetails show
VIProductVersion "${PRODUCT_VERSION}"
VIAddVersionKey "ProductName" "${PRODUCT_NAME}"
VIAddVersionKey "FileDescription" "${PRODUCT_NAME} Setup"
VIAddVersionKey "FileVersion" "${APP_VERSION}"
VIAddVersionKey "LegalCopyright" "FDU Connect contributors"

!define MUI_ABORTWARNING
!define MUI_FINISHPAGE_RUN "$INSTDIR\FDU-Connect-GUI.exe"
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_LICENSE "${LICENSE_FILE}"
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_UNPAGE_FINISH
!insertmacro MUI_LANGUAGE "SimpChinese"
!insertmacro MUI_LANGUAGE "English"

LangString AppRunning ${LANG_SIMPCHINESE} "请先退出此目录中的 FDU Connect 及核心程序，再重新运行安装或卸载。"
LangString AppRunning ${LANG_ENGLISH} "Close FDU Connect and its core running from this folder, then run setup or uninstall again."
LangString UnsupportedWindows ${LANG_SIMPCHINESE} "此安装包需要 64 位 Windows。"
LangString UnsupportedWindows ${LANG_ENGLISH} "This package requires 64-bit Windows."
LangString PreparingRuntime ${LANG_SIMPCHINESE} "正在离线安装 Microsoft WebView2 Runtime，可能需要几分钟……"
LangString PreparingRuntime ${LANG_ENGLISH} "Installing Microsoft WebView2 Runtime offline. This may take a few minutes..."
LangString RuntimeFailed ${LANG_SIMPCHINESE} "无法完成 Microsoft WebView2 Runtime 安装。请稍后重新运行此安装包。"
LangString RuntimeFailed ${LANG_ENGLISH} "Microsoft WebView2 Runtime could not be installed. Run this setup again later."

Function .onInit
    SetShellVarContext current
    ${IfNot} ${RunningX64}
        MessageBox MB_OK|MB_ICONSTOP "$(UnsupportedWindows)" /SD IDOK
        SetErrorLevel 1
        Quit
    ${EndIf}
    SetRegView 64
FunctionEnd

Function un.onInit
    SetShellVarContext current
    SetRegView 64
FunctionEnd

!macro CheckAppClosed Prefix
Function ${Prefix}CheckAppClosed
    Push $0
    Push $1
    StrCpy $1 "$INSTDIR\FDU-Connect-GUI.exe"
    Call ${Prefix}CheckWritableProgram
    StrCpy $1 "$INSTDIR\fdu-connect.exe"
    Call ${Prefix}CheckWritableProgram
    StrCpy $1 "$INSTDIR\Uninstall.exe"
    Call ${Prefix}CheckWritableProgram
    Pop $1
    Pop $0
FunctionEnd

Function ${Prefix}CheckWritableProgram
    IfFileExists "$1" 0 done
    ClearErrors
    FileOpen $0 "$1" a
    IfErrors busy
    FileClose $0
    Return
busy:
    MessageBox MB_OK|MB_ICONEXCLAMATION "$(AppRunning)" /SD IDOK
    SetErrorLevel 2
    Abort
done:
FunctionEnd
!macroend
!insertmacro CheckAppClosed ""
!insertmacro CheckAppClosed "un."

Function ProbeRuntime
    nsExec::ExecToStack /TIMEOUT=$6 '"$PLUGINSDIR\runtime-check.exe" --check-webview2'
    Pop $0
    Pop $1
FunctionEnd

Section "${PRODUCT_NAME}"
    Call CheckAppClosed
    InitPluginsDir
    SetOutPath "$PLUGINSDIR"
    File /oname=runtime-check.exe "${PAYLOAD_DIR}\FDU-Connect-GUI.exe"
    StrCpy $6 15000
    Call ProbeRuntime
    StrCmp $0 "0" runtime_ready
    DetailPrint "$(PreparingRuntime)"
    File /oname=WebView2Setup.exe "${WEBVIEW2_INSTALLER}"
    nsExec::ExecToStack /TIMEOUT=${DEPENDENCY_TIMEOUT} '"$PLUGINSDIR\WebView2Setup.exe" /silent /install'
    Pop $2
    Pop $1
    System::Call 'kernel32::GetTickCount() i.r4'
runtime_check:
    System::Call 'kernel32::GetTickCount() i.r5'
    IntOp $5 $5 - $4
    IntCmpU $5 ${RUNTIME_WAIT} runtime_failed runtime_probe runtime_failed
runtime_probe:
    IntOp $6 ${RUNTIME_WAIT} - $5
    Call ProbeRuntime
    StrCmp $0 "0" runtime_ready
    Sleep 250
    Goto runtime_check
runtime_failed:
    DetailPrint "WebView2: $2; runtime check: $0"
    MessageBox MB_OK|MB_ICONSTOP "$(RuntimeFailed)" /SD IDOK
    SetErrorLevel 3
    Abort
runtime_ready:
    Call CheckAppClosed
    SetOutPath "$INSTDIR"
    SetOverwrite on
    File "${PAYLOAD_DIR}\FDU-Connect-GUI.exe"
    File "${PAYLOAD_DIR}\fdu-connect.exe"
    File "${PAYLOAD_DIR}\config.toml.example"
    File "${LICENSE_FILE}"
    WriteUninstaller "$INSTDIR\Uninstall.exe"
    CreateDirectory "$SMPROGRAMS\${PRODUCT_NAME}"
    CreateShortcut "$SMPROGRAMS\${PRODUCT_NAME}\${PRODUCT_NAME}.lnk" "$INSTDIR\FDU-Connect-GUI.exe"
    CreateShortcut "$SMPROGRAMS\${PRODUCT_NAME}\Uninstall.lnk" "$INSTDIR\Uninstall.exe"
    WriteRegStr HKCU "${UNINSTALL_KEY}" "DisplayName" "${PRODUCT_NAME}"
    WriteRegStr HKCU "${UNINSTALL_KEY}" "DisplayVersion" "${APP_VERSION}"
    WriteRegStr HKCU "${UNINSTALL_KEY}" "Publisher" "FDU Connect"
    WriteRegStr HKCU "${UNINSTALL_KEY}" "InstallLocation" "$INSTDIR"
    WriteRegStr HKCU "${UNINSTALL_KEY}" "DisplayIcon" "$INSTDIR\FDU-Connect-GUI.exe"
    WriteRegStr HKCU "${UNINSTALL_KEY}" "UninstallString" '$\"$INSTDIR\Uninstall.exe$\"'
    WriteRegStr HKCU "${UNINSTALL_KEY}" "QuietUninstallString" '$\"$INSTDIR\Uninstall.exe$\" /S'
    WriteRegDWORD HKCU "${UNINSTALL_KEY}" "NoModify" 1
    WriteRegDWORD HKCU "${UNINSTALL_KEY}" "NoRepair" 1
    SetErrorLevel 0
SectionEnd

Section "Uninstall"
    Call un.CheckAppClosed
    Delete "$INSTDIR\FDU-Connect-GUI.exe"
    Delete "$INSTDIR\fdu-connect.exe"
    Delete "$INSTDIR\config.toml.example"
    Delete "$INSTDIR\LICENSE"
    Delete "$INSTDIR\Uninstall.exe"
    Delete "$SMPROGRAMS\${PRODUCT_NAME}\${PRODUCT_NAME}.lnk"
    Delete "$SMPROGRAMS\${PRODUCT_NAME}\Uninstall.lnk"
    RMDir "$SMPROGRAMS\${PRODUCT_NAME}"
    ReadRegStr $0 HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "${PRODUCT_NAME}"
    StrCmp $0 '$\"$INSTDIR\FDU-Connect-GUI.exe$\" --minimized' 0 autostart_done
    DeleteRegValue HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "${PRODUCT_NAME}"
autostart_done:
    DeleteRegKey HKCU "${UNINSTALL_KEY}"
    ; User settings and VPN/login data are intentionally preserved.
    RMDir "$INSTDIR"
    SetErrorLevel 0
SectionEnd
