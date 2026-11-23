# Installs nook as a command you can run from any terminal:
#
#   nook          chat over Bluetooth with the laptops nearby
#   nook wifi     chat over a shared Wi-Fi network instead
#
# Two ways to run this installer:
#   from the nook folder:  double-click install.cmd
#   from a running host:   irm http://HOST:4747/install.ps1 | iex
#
# It puts the app in %LOCALAPPDATA%\nook and adds that folder to your PATH,
# for your account only (no admin needed). Nothing else is changed.
# To remove it: install.cmd uninstall
#
# Written for Windows PowerShell 2.0 and later, so it also works on Windows 7.

$NookBase = $env:NOOK_BASE # the host fills this in when it serves this file
$NookArgs = @($args)
$NookScript = $MyInvocation.MyCommand.Path

& {
    $ErrorActionPreference = 'Stop'
    $dir = Join-Path $env:LOCALAPPDATA 'nook'
    $onWindows = [Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT

    # Add or remove a folder in the user's PATH, keeping %VARIABLES% intact.
    function Edit-UserPath([string]$folder, [bool]$add) {
        $key = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Environment')
        try {
            $raw = [string]$key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
            $kind = [Microsoft.Win32.RegistryValueKind]::ExpandString
            if ($raw -ne '') { $kind = $key.GetValueKind('Path') }
            $want = $folder.TrimEnd('\')
            $parts = @($raw -split ';' | Where-Object { $_ -ne '' -and [Environment]::ExpandEnvironmentVariables($_).TrimEnd('\') -ne $want })
            if ($add) { $parts += $folder }
            $key.SetValue('Path', ($parts -join ';'), $kind)
        } finally {
            $key.Close()
        }
        # Tell Windows the environment changed so new terminals pick it up.
        [Environment]::SetEnvironmentVariable('NOOK_REFRESH', '1', 'User')
        [Environment]::SetEnvironmentVariable('NOOK_REFRESH', $null, 'User')
    }

    if ($NookArgs -contains 'uninstall' -or $NookArgs -contains '-uninstall') {
        if ($onWindows) { Edit-UserPath $dir $false }
        if (Test-Path $dir) { Remove-Item -Recurse -Force $dir }
        Write-Host 'nook removed.'
        return
    }

    $arch = $env:PROCESSOR_ARCHITECTURE
    if ($env:PROCESSOR_ARCHITEW6432) { $arch = $env:PROCESSOR_ARCHITEW6432 }
    $osver = [Environment]::OSVersion.Version.Major

    New-Item -ItemType Directory -Force -Path $dir | Out-Null
    $exe = Join-Path $dir 'nook.exe'
    $tmp = Join-Path $dir 'nook.download'

    if ($NookBase) {
        # Ask the host for the right build for this computer.
        Write-Host "downloading nook from $NookBase ..."
        try {
            (New-Object Net.WebClient).DownloadFile("$NookBase/dl/auto?os=windows&arch=$arch&osver=$osver", $tmp)
        } catch {
            Remove-Item -Force $tmp -ErrorAction SilentlyContinue
            Write-Host "Download failed. Is the host still running? You can also join in a browser: $NookBase"
            return
        }
    } else {
        # Copy every build from dist\ so this computer can hand the app out too.
        if (-not $NookScript) {
            Write-Host 'Run install.cmd from the nook folder, or use the one-line install shown by a running host.'
            return
        }
        $src = Join-Path (Split-Path -Parent $NookScript) 'dist'
        $old = $osver -lt 10
        if ($arch -eq 'ARM64') {
            $list = 'nook-win-arm64.exe', 'nook-win-x64.exe', 'nook-win-x86.exe'
        } elseif ($arch -eq 'AMD64') {
            if ($old) { $list = 'nook-win7-x64.exe', 'nook-win7-x86.exe' }
            else { $list = 'nook-win-x64.exe', 'nook-win7-x64.exe', 'nook-win-x86.exe', 'nook-win7-x86.exe' }
        } else {
            if ($old) { $list = @('nook-win7-x86.exe') }
            else { $list = 'nook-win-x86.exe', 'nook-win7-x86.exe' }
        }
        $pick = $null
        foreach ($f in $list) {
            if (Test-Path (Join-Path $src $f)) { $pick = $f; break }
        }
        if (-not $pick) {
            Write-Host "The dist folder has no build for this computer ($arch). Run build.sh on a computer with Go first."
            return
        }
        Write-Host 'copying the app, plus the builds for everyone else ...'
        Get-ChildItem -Path $src -Filter 'nook-*' | ForEach-Object { Copy-Item -Force $_.FullName $dir }
        Copy-Item -Force (Join-Path $src $pick) $tmp
    }

    try {
        if (Test-Path $exe) { Remove-Item -Force $exe }
    } catch {
        Write-Host 'nook is running right now. Close it first (Ctrl+C), then run this again.'
        Remove-Item -Force $tmp -ErrorAction SilentlyContinue
        return
    }
    Move-Item -Force $tmp $exe

    if ($onWindows) {
        $v = $null
        try { $v = & $exe version } catch { }
        if (-not $v) {
            Write-Host 'Windows would not run the app (Smart App Control or antivirus may be blocking it).'
            if ($NookBase) { Write-Host "Join in a browser instead: $NookBase" }
            return
        }
        Edit-UserPath $dir $true
        if (($env:Path -split ';') -notcontains $dir) { $env:Path = "$env:Path;$dir" }
    }

    Write-Host ''
    Write-Host "installed $exe"
    Write-Host 'start it any time by typing:'
    Write-Host '  nook         chat over Bluetooth with the laptops nearby'
    Write-Host '  nook wifi    chat over a shared Wi-Fi network instead'
    Write-Host '  nook help    everything else'
    Write-Host '(terminal windows that were already open, and VS Code, need a restart first)'
}
