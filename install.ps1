# Installs nook so you can type `nook` in any terminal. Nothing else is
# needed first: no Go, no admin rights.
#
# From the internet (works in Command Prompt and PowerShell, Windows 7 and up):
#   powershell -ExecutionPolicy Bypass -c "[Net.ServicePointManager]::SecurityProtocol=3072; iex ((New-Object Net.WebClient).DownloadString('https://github.com/SarcasticSphinx/nook/releases/latest/download/install.ps1'))"
#
# With no internet at all:
#   from a nook folder (USB stick):    double-click install.cmd
#   from a nook running in Wi-Fi mode: the line shown on http://HOST:4747/get
#
# It puts the app in %LOCALAPPDATA%\nook and adds that folder to your PATH,
# for your account only. To remove it: install.cmd uninstall
#
# Written for Windows PowerShell 2.0 and later, so it also works on Windows 7.

$NookBase = $env:NOOK_BASE # the host fills this in when it serves this file
$NookArgs = @($args)
$NookScript = $MyInvocation.MyCommand.Path

& {
    $ErrorActionPreference = 'Stop'
    $releases = 'https://github.com/SarcasticSphinx/nook/releases/latest/download'
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

    New-Item -ItemType Directory -Force -Path $dir | Out-Null
    $exe = Join-Path $dir 'nook.exe'
    $tmp = Join-Path $dir 'nook.download'

    # A nook folder with builds in dist\ next to this script (USB copy)?
    $src = $null
    if ($NookScript) {
        $d = Join-Path (Split-Path -Parent $NookScript) 'dist'
        if (Test-Path $d) { $src = $d }
    }

    if ($NookBase) {
        Write-Host "downloading nook from $NookBase ..."
        try {
            (New-Object Net.WebClient).DownloadFile("$NookBase/dl/auto?os=windows&arch=$arch&osver=$osver", $tmp)
        } catch {
            Remove-Item -Force $tmp -ErrorAction SilentlyContinue
            Write-Host "Download failed. Is the host still running? You can also join in a browser: $NookBase"
            return
        }
    } elseif ($src) {
        $pick = $null
        foreach ($f in $list) {
            if (Test-Path (Join-Path $src $f)) { $pick = $f; break }
        }
        if (-not $pick) {
            Write-Host "The dist folder has no build for this computer ($arch)."
            return
        }
        Write-Host 'copying the app, plus the builds for everyone else ...'
        Get-ChildItem -Path $src -Filter 'nook-*' | ForEach-Object { Copy-Item -Force $_.FullName $dir }
        Copy-Item -Force (Join-Path $src $pick) $tmp
    } else {
        # GitHub needs TLS 1.2, which older Windows doesn't switch on by itself.
        try { [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor 3072 } catch { }
        Write-Host 'downloading nook from GitHub ...'
        try {
            (New-Object Net.WebClient).DownloadFile("$releases/$($list[0])", $tmp)
        } catch {
            Remove-Item -Force $tmp -ErrorAction SilentlyContinue
            Write-Host 'Download failed. Check the internet connection, or install from a USB copy of nook.'
            return
        }
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
