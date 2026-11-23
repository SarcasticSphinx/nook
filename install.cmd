@echo off
rem Double-click to install nook, then type "nook" in any terminal.
rem To remove it: install.cmd uninstall
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0install.ps1" %*
echo %cmdcmdline% | find /i "/c" >nul && pause
