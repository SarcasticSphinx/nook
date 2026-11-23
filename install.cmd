@echo off
rem Double-click to install nook, then type "nook" in any terminal.
rem Works on its own: next to a nook folder it installs from there,
rem otherwise it downloads nook from GitHub. To remove: install.cmd uninstall
setlocal
if exist "%~dp0install.ps1" (
  powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0install.ps1" %*
) else (
  powershell -NoProfile -ExecutionPolicy Bypass -Command "[Net.ServicePointManager]::SecurityProtocol=[Net.ServicePointManager]::SecurityProtocol -bor 3072; & ([scriptblock]::Create((New-Object Net.WebClient).DownloadString('https://github.com/SarcasticSphinx/nook/releases/latest/download/install.ps1'))) %*"
)
echo %cmdcmdline% | find /i "/c" >nul && pause
