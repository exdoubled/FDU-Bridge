. (Join-Path $PSScriptRoot 'launcher.ps1')

function Get-MakeNSISPath {
    if ($env:NSIS_MAKENSIS) {
        if (-not (Test-Path -LiteralPath $env:NSIS_MAKENSIS -PathType Leaf)) { throw 'NSIS_MAKENSIS does not point to makensis.exe.' }
        return (Resolve-Path -LiteralPath $env:NSIS_MAKENSIS).Path
    }
    $command = Get-Command makensis.exe -CommandType Application -ErrorAction SilentlyContinue
    if ($command) { return $command.Source }
    foreach ($base in @(${env:ProgramFiles(x86)}, $env:ProgramFiles)) {
        if ($base) {
            $candidate = Join-Path $base 'NSIS\makensis.exe'
            if (Test-Path -LiteralPath $candidate -PathType Leaf) { return $candidate }
        }
    }
    throw 'NSIS 3.13 is required for -Package. Install NSIS 3.13 or set NSIS_MAKENSIS to its makensis.exe.'
}

function Invoke-MakeNSIS([hashtable]$Definitions) {
    $compiler = Get-MakeNSISPath
    $compilerVersion = (& $compiler /VERSION | Out-String).Trim()
    if ($LASTEXITCODE -ne 0 -or $compilerVersion -cnotmatch '^v3\.13(?:\.0)?$') {
        throw "NSIS 3.13 is required to match CI; found '$compilerVersion'."
    }
    $arguments = @('/NOCONFIG', '/V2', '/INPUTCHARSET', 'UTF8')
    foreach ($key in $Definitions.Keys) {
        $value = [string]$Definitions[$key]
        if ($key -notmatch '^[A-Z_][A-Z0-9_]*$' -or $value -match '["\r\n]') { throw 'Invalid NSIS build definition.' }
        $arguments += "/D${key}=$value"
    }
    $arguments += (Join-Path $PSScriptRoot 'installer.nsi')
    & $compiler @arguments | ForEach-Object { Write-Host $_ }
    if ($LASTEXITCODE -ne 0) { throw "Windows installer compilation failed ($LASTEXITCODE)." }
}

function New-WindowsInstaller([string]$OutputDirectory, [string]$LicensePath, [string]$InstallerPath, [string]$Version) {
    $numericVersion = '0.0.0.0'
    if ($Version -cne 'dev') {
        if ($Version -cnotmatch '^v([0-9]+\.[0-9]+\.[0-9]+)(?:-[0-9A-Za-z.-]+)?$') { throw 'Invalid installer version.' }
        $numericVersion = $Matches[1] + '.0'
    }
    foreach ($path in @((Join-Path $OutputDirectory 'FDU-Connect-GUI.exe'), (Join-Path $OutputDirectory 'fdu-connect.exe'),
        (Join-Path $OutputDirectory 'config.toml.example'), $LicensePath, $InstallerPath)) {
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw "Installer input is missing: $path" }
    }
    $prefix = if ($Version -ceq 'dev') { 'FDU-Connect' } else { "FDU-Connect-$Version" }
    $setup = Join-Path $OutputDirectory "$prefix-Windows-x64-setup.exe"
    $inner = Join-Path $OutputDirectory ('.fdu-nsis-' + [guid]::NewGuid().ToString('N') + '.exe')
    try {
        Invoke-MakeNSIS @{
            PAYLOAD_DIR = (Resolve-Path -LiteralPath $OutputDirectory).Path
            LICENSE_FILE = (Resolve-Path -LiteralPath $LicensePath).Path
            WEBVIEW2_INSTALLER = (Resolve-Path -LiteralPath $InstallerPath).Path
            OUTPUT_FILE = $inner; APP_VERSION = $Version; PRODUCT_VERSION = $numericVersion
        }
        New-SetupLauncher $inner $setup $Version $numericVersion
    } finally {
        if (Test-Path -LiteralPath $inner) { Remove-Item -LiteralPath $inner -Force }
    }
    if (-not (Test-Path -LiteralPath $setup -PathType Leaf) -or (Get-Item -LiteralPath $setup).Length -eq 0) { throw 'NSIS did not create the Windows installer.' }
    return $setup
}
