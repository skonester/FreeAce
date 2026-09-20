; Keep aligned with $FreeAceMinRealShellArtifactBytes in
; scripts/verify-windows-authenticode.ps1. Generated CI stubs are empty.
!define FREEACE_MIN_REAL_SHELL_ARTIFACT_BYTES 1024

!macro FREEACE_REGISTER_PROGID_OPEN EXT
  ; Enhance Tauri's default ProgId open verb. Do not write a parallel
  ; SystemFileAssociations\FreeAceOpen  -  that doubles "Open with FreeAce" under
  ; Show more options when the ProgId is already the default association.
  WriteRegStr HKCU "Software\Classes\run.rosie.freeace${EXT}\shell\open" "MUIVerb" "Open with FreeAce"
  WriteRegStr HKCU "Software\Classes\run.rosie.freeace${EXT}\shell\open" "Icon" "$INSTDIR\freeace.exe"
  WriteRegStr HKCU "Software\Classes\run.rosie.freeace${EXT}\shell\open" "MultiSelectModel" "Player"
!macroend

!macro FREEACE_REGISTER_CLASSIC_EXTRACT EXT
  ; Fallback only when Win11 sparse packages are unavailable. Those packages
  ; also surface in the legacy menu, so classic Extract would duplicate them.
  WriteRegStr HKCU "Software\Classes\SystemFileAssociations\${EXT}\shell\FreeAceExtract" "" "Extract with FreeAce"
  WriteRegStr HKCU "Software\Classes\SystemFileAssociations\${EXT}\shell\FreeAceExtract" "Icon" "$INSTDIR\freeace.exe"
  WriteRegStr HKCU "Software\Classes\SystemFileAssociations\${EXT}\shell\FreeAceExtract" "MultiSelectModel" "Player"
  WriteRegStr HKCU "Software\Classes\SystemFileAssociations\${EXT}\shell\FreeAceExtract\command" "" '"$INSTDIR\freeace.exe" --extract "%1"'
!macroend

!macro FREEACE_CLEAN_LEGACY_ARCHIVE_VERBS EXT
  ; Always purge classic archive verbs on install/upgrade. Earlier betas left
  ; FreeAceOpen beside the ProgId open verb; beta.20 could leave Extract after a
  ; failed cleanup path. Win11 packages re-provide Extract when registered.
  DeleteRegKey HKCU "Software\Classes\SystemFileAssociations\${EXT}\shell\FreeAceOpen"
  DeleteRegKey HKCU "Software\Classes\SystemFileAssociations\${EXT}\shell\FreeAceExtract"
  DeleteRegKey HKCU "Software\Classes\SystemFileAssociations\${EXT}\shell\FreeAce.Extract"
!macroend

!macro FREEACE_UNREGISTER_ARCHIVE_VERBS EXT
  !insertmacro FREEACE_CLEAN_LEGACY_ARCHIVE_VERBS "${EXT}"
  DeleteRegKey HKCU "Software\Classes\${EXT}\OpenWithProgids\FreeAce.Archive"
!macroend

!macro FREEACE_REGISTER_COMPRESS_VERBS
  DeleteRegKey HKCU "Software\Classes\*\shell\FreeAce"
  DeleteRegKey HKCU "Software\Classes\Directory\shell\FreeAce"
  DeleteRegKey HKCU "Software\Classes\Directory\Background\shell\FreeAce"

  WriteRegStr HKCU "Software\Classes\*\shell\FreeAceCompress" "MUIVerb" "Compress with FreeAce"
  WriteRegStr HKCU "Software\Classes\*\shell\FreeAceCompress" "Icon" "$INSTDIR\freeace.exe"
  WriteRegStr HKCU "Software\Classes\*\shell\FreeAceCompress" "MultiSelectModel" "Player"
  WriteRegStr HKCU "Software\Classes\*\shell\FreeAceCompress\command" "" '"$INSTDIR\freeace.exe" --compress "%1"'
  WriteRegStr HKCU "Software\Classes\Directory\shell\FreeAceCompress" "MUIVerb" "Compress folder with FreeAce"
  WriteRegStr HKCU "Software\Classes\Directory\shell\FreeAceCompress" "Icon" "$INSTDIR\freeace.exe"
  WriteRegStr HKCU "Software\Classes\Directory\shell\FreeAceCompress" "MultiSelectModel" "Player"
  WriteRegStr HKCU "Software\Classes\Directory\shell\FreeAceCompress\command" "" '"$INSTDIR\freeace.exe" --compress "%1"'
  ; Empty folder background: %V is the folder path.
  WriteRegStr HKCU "Software\Classes\Directory\Background\shell\FreeAceCompress" "MUIVerb" "Compress with FreeAce"
  WriteRegStr HKCU "Software\Classes\Directory\Background\shell\FreeAceCompress" "Icon" "$INSTDIR\freeace.exe"
  WriteRegStr HKCU "Software\Classes\Directory\Background\shell\FreeAceCompress\command" "" '"$INSTDIR\freeace.exe" --compress "%V"'
!macroend

!macro FREEACE_UNREGISTER_COMPRESS_VERBS
  DeleteRegKey HKCU "Software\Classes\*\shell\FreeAceCompress"
  DeleteRegKey HKCU "Software\Classes\Directory\shell\FreeAceCompress"
  DeleteRegKey HKCU "Software\Classes\Directory\Background\shell\FreeAceCompress"
  DeleteRegKey HKCU "Software\Classes\*\shell\FreeAce"
  DeleteRegKey HKCU "Software\Classes\Directory\shell\FreeAce"
  DeleteRegKey HKCU "Software\Classes\Directory\Background\shell\FreeAce"
!macroend

!macro FREEACE_CLEAN_LEGACY_SHELL_PAYLOAD
  ; Beta 11/13 placed these directly in $INSTDIR. Keep cleanup available on
  ; both update and uninstall in case a mapped DLL survived the first attempt.
  Delete /REBOOTOK "$INSTDIR\freeace_shell.dll"
  Delete /REBOOTOK "$INSTDIR\freeace_extract_shell.dll"
  Delete /REBOOTOK "$INSTDIR\FreeAceContextMenu.msix"
  Delete /REBOOTOK "$INSTDIR\FreeAceExtractContextMenu.msix"
  Delete /REBOOTOK "$INSTDIR\register-windows-context-menu.ps1"
!macroend

!macro FREEACE_REGISTER_WIN11_CONTEXT_MENU
  ; Sparse MSIX + IExplorerCommand DLL.
  ; Shell hosts can keep a COM DLL mapped long after the menu closes. Every
  ; release therefore gets a new directory so an update never overwrites a DLL
  ; that Explorer/dllhost still has open. ${VERSION} is defined by Tauri's NSIS
  ; template before this macro is expanded.
  ; $R6 = 1 when current packages registered, 2 when registration was
  ; deferred because Explorer still has a DLL open, 3 when a prior payload was
  ; restored. Deferred/restored states retain every payload directory; only 1
  ; permits stale-payload cleanup.
  StrCpy $R6 "0"
  IfFileExists "$INSTDIR\shell-${VERSION}\FreeAceContextMenu.msix" 0 freeace_skip_win11_menu
  StrCpy $R9 "$INSTDIR\shell-${VERSION}"
  IfFileExists "$R9\freeace_shell.dll" 0 freeace_skip_win11_menu
  IfFileExists "$R9\FreeAceExtractContextMenu.msix" 0 freeace_skip_win11_menu
  IfFileExists "$R9\freeace_extract_shell.dll" 0 freeace_skip_win11_menu
  ; Skip empty CI stubs (real packages are much larger than 1 KiB).
  FileOpen $R8 "$R9\FreeAceContextMenu.msix" r
  FileSeek $R8 0 END $R7
  FileClose $R8
  IntCmp $R7 ${FREEACE_MIN_REAL_SHELL_ARTIFACT_BYTES} freeace_skip_win11_menu freeace_skip_win11_menu 0
  FileOpen $R8 "$R9\FreeAceExtractContextMenu.msix" r
  FileSeek $R8 0 END $R7
  FileClose $R8
  IntCmp $R7 ${FREEACE_MIN_REAL_SHELL_ARTIFACT_BYTES} freeace_skip_win11_menu freeace_skip_win11_menu 0

  ; The script and sparse packages ship beside the DLLs. ExternalLocation stays
  ; at $INSTDIR because AppxManifest references both root freeace.exe and the
  ; versioned shell-${VERSION} COM server paths from that common external root.
  StrCpy $R8 "$R9\register-windows-context-menu.ps1"
  IfFileExists "$R8" 0 freeace_menu_script_instdir
  Goto freeace_menu_run_script
  freeace_menu_script_instdir:
  StrCpy $R8 "$INSTDIR\register-windows-context-menu.ps1"
  IfFileExists "$R8" 0 freeace_menu_no_script
  freeace_menu_run_script:
  DetailPrint "Registering Win11 context menu package... (this may take a moment)"
  nsExec::ExecToLog '"$SYSDIR\WindowsPowerShell\v1.0\powershell.exe" -NoProfile -ExecutionPolicy Bypass -File "$R8" -MsixPath "$R9\FreeAceContextMenu.msix" -ExtractMsixPath "$R9\FreeAceExtractContextMenu.msix" -ExternalLocation "$INSTDIR" -ShellPayloadLocation "$R9" -LogPath "$INSTDIR\freeace-context-menu-register.log"'
  Pop $0
  StrCmp $0 "error" freeace_menu_exec_failed 0
  IntCmp $0 0 freeace_menu_registered 0 0
  IntCmp $0 2 freeace_menu_registration_deferred 0 0
  IntCmp $0 3 freeace_menu_previous_restored 0 0
  freeace_menu_exec_failed:
  DetailPrint "WARNING: Win11 context menu registration failed (exit $0). Classic verbs still work. See $INSTDIR\freeace-context-menu-register.log"
  Goto freeace_skip_win11_menu
  freeace_menu_registered:
  DetailPrint "Win11 context menu package registered."
  StrCpy $R6 "1"
  Goto freeace_skip_win11_menu
  freeace_menu_registration_deferred:
  DetailPrint "Win11 context menu registration deferred until Explorer releases the previous shell payload."
  StrCpy $R6 "2"
  Goto freeace_skip_win11_menu
  freeace_menu_previous_restored:
  DetailPrint "Previous Win11 context menu payload restored; retaining rollback payloads."
  StrCpy $R6 "3"
  Goto freeace_skip_win11_menu
  freeace_menu_no_script:
  DetailPrint "WARNING: register-windows-context-menu.ps1 missing; skipping Win11 modern menu. Classic verbs still work."
  FileOpen $R8 "$INSTDIR\freeace-context-menu-register.log" w
  FileWrite $R8 "ERROR: register-windows-context-menu.ps1 not found next to shell package or in $INSTDIR$\r$\n"
  FileClose $R8
  freeace_skip_win11_menu:
!macroend

!macro FREEACE_UNREGISTER_WIN11_CONTEXT_MENU
  ; Retry once, then fail the PowerShell step if either sparse package remains.
  ; PREUNINSTALL uses this before Tauri deletes files. Abort uninstall when
  ; packages remain so Explorer never keeps a registration against a missing
  ; ExternalLocation.
  ; $R5 = 1 when unregister failed so PREUNINSTALL can Abort.
  StrCpy $R5 "0"
  DetailPrint "Unregistering Win11 sparse context-menu packages…"
  StrCpy $R8 "$INSTDIR\shell-${VERSION}\register-windows-context-menu.ps1"
  IfFileExists "$R8" 0 freeace_unreg_script_instdir
  Goto freeace_unreg_run
  freeace_unreg_script_instdir:
  StrCpy $R8 "$INSTDIR\register-windows-context-menu.ps1"
  IfFileExists "$R8" 0 freeace_unreg_no_script
  freeace_unreg_run:
  nsExec::ExecToLog '"$SYSDIR\WindowsPowerShell\v1.0\powershell.exe" -NoProfile -ExecutionPolicy Bypass -File "$R8" -Unregister -LogPath "$INSTDIR\freeace-context-menu-register.log"'
  Pop $0
  StrCmp $0 "error" freeace_win11_unregister_fail 0
  IntCmp $0 0 freeace_win11_unregister_ok 0 0
  freeace_win11_unregister_fail:
  StrCpy $R5 "1"
  DetailPrint "WARNING: Could not fully unregister Win11 sparse context-menu packages (exit $0)."
  FileOpen $R8 "$INSTDIR\freeace-context-menu-register.log" a
  FileSeek $R8 0 END
  FileWrite $R8 "WARNING: Win11 sparse package unregister incomplete during uninstall (exit $0)$\r$\n"
  FileClose $R8
  Goto freeace_win11_unregister_ok
  freeace_unreg_no_script:
  DetailPrint "WARNING: register-windows-context-menu.ps1 missing; probing AppX packages."
  nsExec::ExecToLog '"$SYSDIR\WindowsPowerShell\v1.0\powershell.exe" -NoProfile -ExecutionPolicy Bypass -Command "if (Get-AppxPackage -Name run.rosie.freeace.contextmenu) { exit 1 }; if (Get-AppxPackage -Name run.rosie.freeace.extractmenu) { exit 1 }; exit 0"'
  Pop $0
  StrCmp $0 "error" freeace_unreg_packages_remain 0
  IntCmp $0 0 freeace_win11_unregister_ok 0 0
  freeace_unreg_packages_remain:
  StrCpy $R5 "1"
  DetailPrint "WARNING: Win11 sparse packages still present; cannot unregister without the helper script."
  freeace_win11_unregister_ok:
!macroend

!macro FREEACE_CLEAN_SHELL_PAYLOADS KEEPDIR LABELPREFIX
  ; Remove only installer-owned files from each payload except KEEPDIR. Avoid
  ; recursive deletion so an unexpected junction or user file is never followed.
  ; Locked DLLs and the directory are scheduled for deletion after reboot.
  FindFirst $R8 $R9 "$INSTDIR\shell-*"
  ${LABELPREFIX}_loop:
  StrCmp $R9 "" ${LABELPREFIX}_done
  StrCmp $R9 "${KEEPDIR}" ${LABELPREFIX}_next
  ; Never follow a junction/symlink that happens to match shell-*.
  System::Call 'kernel32::GetFileAttributesW(w "$INSTDIR\$R9") i .R7'
  IntOp $R7 $R7 & 0x400
  IntCmp $R7 0 ${LABELPREFIX}_clean ${LABELPREFIX}_next ${LABELPREFIX}_next
  ${LABELPREFIX}_clean:
  Delete /REBOOTOK "$INSTDIR\$R9\freeace_shell.dll"
  Delete /REBOOTOK "$INSTDIR\$R9\freeace_extract_shell.dll"
  Delete /REBOOTOK "$INSTDIR\$R9\FreeAceContextMenu.msix"
  Delete /REBOOTOK "$INSTDIR\$R9\FreeAceExtractContextMenu.msix"
  Delete /REBOOTOK "$INSTDIR\$R9\register-windows-context-menu.ps1"
  RMDir /REBOOTOK "$INSTDIR\$R9"
  ${LABELPREFIX}_next:
  FindNext $R8 $R9
  Goto ${LABELPREFIX}_loop
  ${LABELPREFIX}_done:
  FindClose $R8
!macroend

!macro FREEACE_POSTINSTALL_CLASSIC_EXTRACT_FALLBACK
  !insertmacro FREEACE_REGISTER_CLASSIC_EXTRACT ".7z"
  !insertmacro FREEACE_REGISTER_CLASSIC_EXTRACT ".freeace"
  !insertmacro FREEACE_REGISTER_CLASSIC_EXTRACT ".zip"
  !insertmacro FREEACE_REGISTER_CLASSIC_EXTRACT ".tar"
  !insertmacro FREEACE_REGISTER_CLASSIC_EXTRACT ".gz"
  !insertmacro FREEACE_REGISTER_CLASSIC_EXTRACT ".bz2"
  !insertmacro FREEACE_REGISTER_CLASSIC_EXTRACT ".xz"
  !insertmacro FREEACE_REGISTER_CLASSIC_EXTRACT ".rar"
  !insertmacro FREEACE_REGISTER_CLASSIC_EXTRACT ".tgz"
  !insertmacro FREEACE_REGISTER_CLASSIC_EXTRACT ".tbz2"
  !insertmacro FREEACE_REGISTER_CLASSIC_EXTRACT ".txz"
  !insertmacro FREEACE_REGISTER_CLASSIC_EXTRACT ".001"
!macroend

!macro NSIS_HOOK_PREINSTALL
  ; Refuse a pre-created junction/symlink before Tauri copies any resources
  ; through it. A missing destination returns INVALID_FILE_ATTRIBUTES (-1).
  System::Call 'kernel32::GetFileAttributesW(w "$INSTDIR\shell-${VERSION}") i .R7'
  IntCmp $R7 -1 freeace_preinstall_destination_safe freeace_preinstall_check_reparse freeace_preinstall_check_reparse
  freeace_preinstall_check_reparse:
  IntOp $R8 $R7 & 0x400
  IntCmp $R8 0 freeace_preinstall_destination_safe 0 0
  MessageBox MB_ICONSTOP|MB_OK "FreeAce cannot install into a shell directory that is a junction or symbolic link:$\r$\n$INSTDIR\shell-${VERSION}"
  Abort
  freeace_preinstall_destination_safe:
  ; Re-running the exact same installer must not try to rewrite its own loaded
  ; shell DLL. For an existing same-version payload, NSIS skips equal-timestamp
  ; files while still restoring missing or genuinely different files. A normal
  ; future-version update has no destination directory and keeps overwrite=on.
  IfFileExists "$INSTDIR\shell-${VERSION}\freeace_shell.dll" 0 freeace_preinstall_done
  SetOverwrite ifdiff
  freeace_preinstall_done:
!macroend

!macro NSIS_HOOK_POSTINSTALL
  SetOverwrite on
  ; Drop leftover classic archive verbs from earlier betas before writing new
  ; state. Win11 sparse packages also appear under Show more options, so classic
  ; Extract/Compress must not stack on top of them when registration succeeds.
  !insertmacro FREEACE_CLEAN_LEGACY_ARCHIVE_VERBS ".7z"
  !insertmacro FREEACE_CLEAN_LEGACY_ARCHIVE_VERBS ".freeace"
  !insertmacro FREEACE_CLEAN_LEGACY_ARCHIVE_VERBS ".zip"
  !insertmacro FREEACE_CLEAN_LEGACY_ARCHIVE_VERBS ".tar"
  !insertmacro FREEACE_CLEAN_LEGACY_ARCHIVE_VERBS ".gz"
  !insertmacro FREEACE_CLEAN_LEGACY_ARCHIVE_VERBS ".bz2"
  !insertmacro FREEACE_CLEAN_LEGACY_ARCHIVE_VERBS ".xz"
  !insertmacro FREEACE_CLEAN_LEGACY_ARCHIVE_VERBS ".rar"
  !insertmacro FREEACE_CLEAN_LEGACY_ARCHIVE_VERBS ".tgz"
  !insertmacro FREEACE_CLEAN_LEGACY_ARCHIVE_VERBS ".tbz2"
  !insertmacro FREEACE_CLEAN_LEGACY_ARCHIVE_VERBS ".txz"
  !insertmacro FREEACE_CLEAN_LEGACY_ARCHIVE_VERBS ".001"
  !insertmacro FREEACE_REGISTER_PROGID_OPEN ".7z"
  !insertmacro FREEACE_REGISTER_PROGID_OPEN ".freeace"
  !insertmacro FREEACE_REGISTER_PROGID_OPEN ".zip"
  !insertmacro FREEACE_REGISTER_PROGID_OPEN ".tar"
  !insertmacro FREEACE_REGISTER_PROGID_OPEN ".gz"
  !insertmacro FREEACE_REGISTER_PROGID_OPEN ".bz2"
  !insertmacro FREEACE_REGISTER_PROGID_OPEN ".xz"
  !insertmacro FREEACE_REGISTER_PROGID_OPEN ".rar"
  !insertmacro FREEACE_REGISTER_PROGID_OPEN ".tgz"
  !insertmacro FREEACE_REGISTER_PROGID_OPEN ".tbz2"
  !insertmacro FREEACE_REGISTER_PROGID_OPEN ".txz"
  !insertmacro FREEACE_REGISTER_COMPRESS_VERBS
  !insertmacro FREEACE_REGISTER_WIN11_CONTEXT_MENU
  IntCmp $R6 1 freeace_postinstall_win11_ok 0 0
  IntCmp $R6 3 freeace_postinstall_win11_restored 0 0
  DetailPrint "Keeping classic Extract/Compress verbs (Win11 menu unavailable)."
  !insertmacro FREEACE_POSTINSTALL_CLASSIC_EXTRACT_FALLBACK
  Goto freeace_postinstall_verbs_done
  freeace_postinstall_win11_ok:
  DetailPrint "Removing classic Extract/Compress verbs; Win11 packages cover legacy menu too."
  !insertmacro FREEACE_UNREGISTER_COMPRESS_VERBS
  !insertmacro FREEACE_CLEAN_LEGACY_SHELL_PAYLOAD
  !insertmacro FREEACE_CLEAN_SHELL_PAYLOADS "shell-${VERSION}" freeace_update_shell_cleanup
  freeace_postinstall_verbs_done:
  Goto freeace_postinstall_done
  freeace_postinstall_win11_restored:
  DetailPrint "Removing classic Extract/Compress verbs; restored Win11 packages cover legacy menu too."
  !insertmacro FREEACE_UNREGISTER_COMPRESS_VERBS
  ; The restored package may still be loaded from an older shell-* directory.
  ; Leave all payloads intact until a later successful registration.
  freeace_postinstall_done:
!macroend

!macro NSIS_HOOK_PREUNINSTALL
  ; Unregister sparse packages before Tauri deletes files. Explorer still
  ; resolves ExternalLocation against $INSTDIR at this point. Abort if either
  ; package remains so we never delete a live COM server. In-place updates pass
  ; /UPDATE; never Abort there or a leftover AppX identity blocks the upgrade.
  StrCpy $R4 $CMDLINE
  StrCpy $R3 0
  StrCpy $R1 "0"
  freeace_preuninstall_scan_update:
  StrCpy $R2 $R4 7 $R3
  StrCmp $R2 "" freeace_preuninstall_unregister
  StrCmp $R2 "/UPDATE" freeace_preuninstall_update
  IntOp $R3 $R3 + 1
  Goto freeace_preuninstall_scan_update
  freeace_preuninstall_update:
  StrCpy $R1 "1"
  freeace_preuninstall_unregister:
  !insertmacro FREEACE_UNREGISTER_WIN11_CONTEXT_MENU
  StrCmp $R1 "1" freeace_preuninstall_done
  IntCmp $R5 1 freeace_preuninstall_abort 0 0
  Goto freeace_preuninstall_done
  freeace_preuninstall_abort:
  MessageBox MB_ICONSTOP|MB_OK "FreeAce could not unregister the Win11 context-menu packages. Uninstall was cancelled so Explorer can still find the shell files. Remove run.rosie.freeace.contextmenu and run.rosie.freeace.extractmenu from Apps, then uninstall again."
  Abort
  freeace_preuninstall_done:
!macroend

!macro NSIS_HOOK_POSTUNINSTALL
  ; PREUNINSTALL already unregistered packages (or aborted). Clean payloads and
  ; classic verbs now that no AppX identity still points at $INSTDIR.
  !insertmacro FREEACE_CLEAN_SHELL_PAYLOADS "" freeace_uninstall_shell_cleanup
  !insertmacro FREEACE_CLEAN_LEGACY_SHELL_PAYLOAD
  Delete /REBOOTOK "$INSTDIR\freeace-context-menu-register.log"
  !insertmacro FREEACE_UNREGISTER_COMPRESS_VERBS
  !insertmacro FREEACE_UNREGISTER_ARCHIVE_VERBS ".7z"
  !insertmacro FREEACE_UNREGISTER_ARCHIVE_VERBS ".freeace"
  !insertmacro FREEACE_UNREGISTER_ARCHIVE_VERBS ".zip"
  !insertmacro FREEACE_UNREGISTER_ARCHIVE_VERBS ".tar"
  !insertmacro FREEACE_UNREGISTER_ARCHIVE_VERBS ".gz"
  !insertmacro FREEACE_UNREGISTER_ARCHIVE_VERBS ".bz2"
  !insertmacro FREEACE_UNREGISTER_ARCHIVE_VERBS ".xz"
  !insertmacro FREEACE_UNREGISTER_ARCHIVE_VERBS ".rar"
  !insertmacro FREEACE_UNREGISTER_ARCHIVE_VERBS ".tgz"
  !insertmacro FREEACE_UNREGISTER_ARCHIVE_VERBS ".tbz2"
  !insertmacro FREEACE_UNREGISTER_ARCHIVE_VERBS ".txz"
  !insertmacro FREEACE_UNREGISTER_ARCHIVE_VERBS ".001"
  DeleteRegKey HKCU "Software\Classes\FreeAce.Archive"
  RMDir /REBOOTOK "$INSTDIR"
!macroend
