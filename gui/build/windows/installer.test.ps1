$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'installer.ps1')

$directory = Join-Path ([IO.Path]::GetTempPath()) ('fdu-installer-test-' + [guid]::NewGuid().ToString('N'))
$identity = 'FDUConnect.Test.' + [guid]::NewGuid().ToString('N')
$product = 'FDU Connect Installer Test ' + [guid]::NewGuid().ToString('N')
$uninstallKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\' + $identity
$runKey = 'Software\Microsoft\Windows\CurrentVersion\Run'
$programsDirectory = [Environment]::GetFolderPath('Programs')
$shortcutDirectory = Join-Path $programsDirectory $product
$savedFixtureDirectory = $env:FDU_INSTALLER_TEST_DIR
$savedFixtureMode = $env:FDU_INSTALLER_TEST_MODE
New-Item -ItemType Directory -Path $directory | Out-Null

function Run-Setup([string]$Path, [string]$InstallDirectory) {
    $process = Start-Process -FilePath $Path -ArgumentList @('/S', "/D=$InstallDirectory") -WindowStyle Hidden -PassThru
    try {
        if (-not $process.WaitForExit(30000)) { $process.Kill(); throw 'Fixture installer did not finish.' }
        return $process.ExitCode
    } finally { $process.Dispose() }
}

function Remove-FixtureAutoStart {
    $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey($runKey, $true)
    if ($key) {
        try { $key.DeleteValue($product, $false) } finally { $key.Dispose() }
    }
}

function Remove-FixtureInstallation([string]$InstallDirectory, [string]$ExpectedAutoStart, [string]$CaseName) {
    $uninstaller = Join-Path $InstallDirectory 'Uninstall.exe'
    $startInfo = [Diagnostics.ProcessStartInfo]::new($uninstaller, '/S')
    $startInfo.UseShellExecute = $false
    $startInfo.CreateNoWindow = $true
    # NSIS uses fixed temporary uninstaller names; isolate concurrently running suites.
    $startInfo.EnvironmentVariables['TEMP'] = $directory
    $startInfo.EnvironmentVariables['TMP'] = $directory
    $process = [Diagnostics.Process]::Start($startInfo)
    try {
        if (-not $process.WaitForExit(10000)) { $process.Kill(); throw "[$CaseName] Fixture uninstaller did not finish." }
        $exitCode = $process.ExitCode
    } finally { $process.Dispose() }
    $deadline = [DateTime]::UtcNow.AddSeconds(10)
    while ((Test-Path -LiteralPath $uninstallKey) -and [DateTime]::UtcNow -lt $deadline) { Start-Sleep -Milliseconds 100 }
    if (Test-Path -LiteralPath $uninstallKey) { throw "[$CaseName] Uninstaller registration survived uninstall (launcher exit $exitCode)." }
    foreach ($name in @('FDU-Connect-GUI.exe', 'fdu-connect.exe', 'config.toml.example', 'LICENSE')) {
        if (Test-Path -LiteralPath (Join-Path $InstallDirectory $name)) { throw "Uninstall retained program file $name." }
    }
    if (Test-Path -LiteralPath $shortcutDirectory) { throw 'Uninstall retained start-menu shortcuts.' }
    $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey($runKey)
    try { $autoStart = if ($key) { $key.GetValue($product, $null) } else { $null } } finally { if ($key) { $key.Dispose() } }
    if ($ExpectedAutoStart) {
        if ($autoStart -cne $ExpectedAutoStart) { throw 'Uninstall removed an autostart entry belonging to another folder.' }
    } elseif ($null -ne $autoStart) { throw 'Uninstall retained its own autostart entry.' }
    if ([IO.File]::ReadAllText((Join-Path $InstallDirectory 'gui-settings.json')) -cne 'preserve user settings') {
        throw 'Uninstall changed user settings.'
    }
}

try {
    $source = @'
package main
import (
    "os"
    "path/filepath"
    "strconv"
    "time"
)
func main() {
    dir := os.Getenv("FDU_INSTALLER_TEST_DIR")
    if dir == "" { os.Exit(91) }
    if len(os.Args) == 2 && os.Args[1] == "--hold" { time.Sleep(30*time.Second); return }
    if len(os.Args) == 2 && os.Args[1] == "--check-webview2" {
        data, err := os.ReadFile(filepath.Join(dir,"runtime-ready"))
        if err != nil { os.Exit(1) }
        ready, err := strconv.ParseInt(string(data),10,64)
        if err != nil || time.Now().UnixNano() < ready { os.Exit(1) }
        return
    }
    if len(os.Args) == 3 && os.Args[1] == "/silent" && os.Args[2] == "/install" {
        f, err := os.OpenFile(filepath.Join(dir,"installer-invocations"),os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600)
        if err != nil { os.Exit(92) }; f.WriteString("run\n"); f.Close()
        switch os.Getenv("FDU_INSTALLER_TEST_MODE") {
        case "timeout": time.Sleep(10*time.Second)
        case "failure": os.Exit(17)
        default: os.WriteFile(filepath.Join(dir,"runtime-ready"),[]byte(strconv.FormatInt(time.Now().Add(250*time.Millisecond).UnixNano(),10)),0600); os.Exit(17)
        }
        return
    }
    os.WriteFile(filepath.Join(dir,"gui-launched"),[]byte("unexpected launch"),0600)
}
'@
    $sourcePath = Join-Path $directory 'fixture.go'
    [IO.File]::WriteAllText($sourcePath, $source, [Text.UTF8Encoding]::new($false))
    $gui = Join-Path $directory 'FDU-Connect-GUI.exe'
    & go build -trimpath -ldflags '-H windowsgui -s -w' -o $gui $sourcePath
    if ($LASTEXITCODE -ne 0) { throw 'Could not build fixture program.' }
    Copy-Item -LiteralPath $gui -Destination (Join-Path $directory 'fdu-connect.exe')
    Copy-Item -LiteralPath $gui -Destination (Join-Path $directory 'fake-webview2.exe')
    foreach ($name in @('LICENSE', 'config.toml.example')) { [IO.File]::WriteAllText((Join-Path $directory $name), "fixture $name") }
    $setup = Join-Path $directory 'fixture-setup.exe'
    Invoke-MakeNSIS @{
        PAYLOAD_DIR = $directory; LICENSE_FILE = (Join-Path $directory 'LICENSE')
        WEBVIEW2_INSTALLER = (Join-Path $directory 'fake-webview2.exe'); OUTPUT_FILE = $setup
        APP_VERSION = 'v1.2.3-test'; PRODUCT_VERSION = '1.2.3.0'; APP_ID = $identity; PRODUCT_NAME = $product
        DEPENDENCY_TIMEOUT = '250'; RUNTIME_WAIT = '1000'
    }
    $env:FDU_INSTALLER_TEST_DIR = $directory
    foreach ($mode in @('already', 'install', 'failure', 'timeout')) {
        $env:FDU_INSTALLER_TEST_MODE = $mode
        foreach ($name in @('runtime-ready', 'installer-invocations', 'gui-launched')) {
            $path = Join-Path $directory $name
            if (Test-Path -LiteralPath $path) { Remove-Item -LiteralPath $path }
        }
        if ($mode -eq 'already') { [IO.File]::WriteAllText((Join-Path $directory 'runtime-ready'), '1') }
        $installDirectory = Join-Path $directory "installed $mode"
        New-Item -ItemType Directory -Path $installDirectory | Out-Null
        [IO.File]::WriteAllText((Join-Path $installDirectory 'gui-settings.json'), 'preserve user settings')
        $exitCode = Run-Setup $setup $installDirectory
        if ($mode -eq 'failure' -or $mode -eq 'timeout') {
            if ($exitCode -eq 0 -or (Test-Path -LiteralPath (Join-Path $installDirectory 'FDU-Connect-GUI.exe')) -or
                (Test-Path -LiteralPath $uninstallKey) -or (Test-Path -LiteralPath $shortcutDirectory)) {
                throw "Failed dependency installation was reported as complete: $mode/$exitCode"
            }
            continue
        }
        if ($exitCode -ne 0) { throw "Fixture installation failed: $mode/$exitCode" }
        foreach ($name in @('FDU-Connect-GUI.exe', 'fdu-connect.exe', 'config.toml.example', 'LICENSE', 'Uninstall.exe')) {
            if (-not (Test-Path -LiteralPath (Join-Path $installDirectory $name))) { throw "Installer omitted $name." }
        }
        if (-not (Test-Path -LiteralPath $uninstallKey) -or -not (Test-Path -LiteralPath (Join-Path $shortcutDirectory "$product.lnk"))) {
            throw 'Installed application has no registration or shortcut.'
        }
        if ($null -ne (Get-ItemProperty -LiteralPath $uninstallKey).PSObject.Properties['DisplayVersion']) {
            throw 'Setup registered a version that becomes stale after in-app updates.'
        }
        if ((Test-Path -LiteralPath (Join-Path $directory 'gui-launched')) -or
            (Test-Path -LiteralPath (Join-Path $installDirectory 'MicrosoftEdgeWebView2RuntimeInstallerX64.exe'))) {
            throw 'Silent installation launched the GUI or retained the dependency installer.'
        }
        $runs = Join-Path $directory 'installer-invocations'
        if (($mode -eq 'already' -and (Test-Path -LiteralPath $runs)) -or
            ($mode -eq 'install' -and ([IO.File]::ReadAllLines($runs).Count -ne 1))) { throw 'Unexpected dependency installer invocation count.' }
        if ($mode -eq 'already') {
            $blocker = Start-Process -FilePath (Join-Path $installDirectory 'FDU-Connect-GUI.exe') -ArgumentList '--hold' -WindowStyle Hidden -PassThru
            try {
                Start-Sleep -Milliseconds 200
                if ((Run-Setup $setup $installDirectory) -eq 0) { throw 'Repair overwrote a running application.' }
            } finally {
                if (-not $blocker.HasExited) { $blocker.Kill(); $blocker.WaitForExit() }
                $blocker.Dispose()
            }
            New-ItemProperty -LiteralPath $uninstallKey -Name 'DisplayVersion' -Value 'v0.0.1-old-setup' -PropertyType String -Force | Out-Null
            if ((Run-Setup $setup $installDirectory) -ne 0) { throw 'Closed application could not be repaired.' }
            if ($null -ne (Get-ItemProperty -LiteralPath $uninstallKey).PSObject.Properties['DisplayVersion']) {
                throw 'Repair retained the stale version from an older setup.'
            }
            if ([IO.File]::ReadAllText((Join-Path $installDirectory 'gui-settings.json')) -cne 'preserve user settings') {
                throw 'Repair changed user settings.'
            }
        }
        $autoStart = if ($mode -eq 'already') { '"' + (Join-Path $installDirectory 'FDU-Connect-GUI.exe') + '" --minimized' } else { '"C:\another-fixture\FDU-Connect-GUI.exe" --minimized' }
        $runRegistry = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey($runKey)
        try { $runRegistry.SetValue($product, $autoStart, [Microsoft.Win32.RegistryValueKind]::String) } finally { $runRegistry.Dispose() }
        $expectedAutoStart = if ($mode -eq 'already') { '' } else { $autoStart }
        $uninstallerReadLock = $null
        if ($mode -eq 'already') {
            # The original uninstaller may still have a reader while its temp copy runs.
            $uninstallerReadLock = [IO.File]::Open((Join-Path $installDirectory 'Uninstall.exe'),
                [IO.FileMode]::Open, [IO.FileAccess]::Read, ([IO.FileShare]::Read -bor [IO.FileShare]::Delete))
        }
        try {
            if ($uninstallerReadLock -and (Run-Setup $setup $installDirectory) -eq 0) {
                throw 'Repair overwrote a locked uninstaller.'
            }
            Remove-FixtureInstallation $installDirectory $expectedAutoStart $mode
        }
        finally { if ($uninstallerReadLock) { $uninstallerReadLock.Dispose() } }
        Remove-FixtureAutoStart
    }
    Write-Output 'Windows installer integration checks passed.'
} finally {
    $env:FDU_INSTALLER_TEST_DIR = $savedFixtureDirectory
    $env:FDU_INSTALLER_TEST_MODE = $savedFixtureMode
    Remove-FixtureAutoStart
    if (Test-Path -LiteralPath $uninstallKey) { Remove-Item -LiteralPath $uninstallKey -Recurse -Force }
    if (Test-Path -LiteralPath $shortcutDirectory) {
        $resolvedShortcut = [IO.Path]::GetFullPath($shortcutDirectory)
        $programsPrefix = [IO.Path]::GetFullPath($programsDirectory).TrimEnd('\') + '\'
        if (-not $resolvedShortcut.StartsWith($programsPrefix, [StringComparison]::OrdinalIgnoreCase) -or
            [IO.Path]::GetFileName($resolvedShortcut) -cne $product) { throw 'Refusing cleanup outside the fixture shortcut directory.' }
        Remove-Item -LiteralPath $resolvedShortcut -Recurse -Force
    }
    $resolvedDirectory = [IO.Path]::GetFullPath($directory)
    if (-not $resolvedDirectory.StartsWith([IO.Path]::GetTempPath(), [StringComparison]::OrdinalIgnoreCase) -or
        [IO.Path]::GetFileName($resolvedDirectory) -notlike 'fdu-installer-test-*') { throw 'Refusing cleanup outside the fixture directory.' }
    Remove-Item -LiteralPath $resolvedDirectory -Recurse -Force
}
