$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'packages.ps1')
Add-Type -AssemblyName System.IO.Compression.FileSystem

function Assert-Entries([string]$Path, [string[]]$Expected) {
    $zip = [IO.Compression.ZipFile]::OpenRead($Path)
    try {
        $actual = @($zip.Entries | ForEach-Object { $_.FullName } | Sort-Object)
        if (($actual -join ',') -cne (($Expected | Sort-Object) -join ',')) {
            throw "Unexpected package contents in ${Path}: $($actual -join ', ')"
        }
        foreach ($entry in $zip.Entries) {
            $reader = [IO.StreamReader]::new($entry.Open())
            try {
                if ($reader.ReadToEnd() -cne $entry.Name) { throw "Wrong contents for $($entry.Name)." }
            } finally { $reader.Dispose() }
        }
    } finally { $zip.Dispose() }
}

$directory = Join-Path ([IO.Path]::GetTempPath()) ('fdu-packages-test-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $directory | Out-Null
try {
    $standard = @('FDU-Connect-GUI.exe', 'fdu-connect.exe', 'config.toml.example', 'LICENSE')
    $installer = 'MicrosoftEdgeWebView2RuntimeInstallerX64.exe'
    foreach ($name in ($standard + $installer + 'gui-settings.json' + 'login-cache.json')) {
        [IO.File]::WriteAllText((Join-Path $directory $name), $name)
    }
    foreach ($version in @('dev', 'v1.2.3-beta.1')) {
        $packages = @(New-WindowsPackages $directory (Join-Path $directory 'LICENSE') (Join-Path $directory $installer) $version)
        if ($packages.Count -ne 2) { throw 'Expected a lightweight update package and a full offline package.' }
        $prefix = if ($version -eq 'dev') { 'FDU-Connect' } else { "FDU-Connect-$version" }
        if ((Split-Path -Leaf $packages[0]) -cne "$prefix-Windows-x64.zip") { throw 'Update package name changed.' }
        if ((Split-Path -Leaf $packages[1]) -cne "$prefix-Windows-x64-offline.zip") { throw 'Wrong offline package name.' }
        Assert-Entries $packages[0] $standard
        Assert-Entries $packages[1] ($standard + $installer)
    }
    # Avoid compressing a huge fixture; only the resulting file length matters here.
    function Compress-Archive {
        param($LiteralPath, $DestinationPath, [switch]$Force)
        $stream = [IO.File]::Create($DestinationPath)
        try { $stream.SetLength(256MB + 1) } finally { $stream.Dispose() }
    }
    $rejected = $false
    try { $null = New-WindowsPackages $directory (Join-Path $directory 'LICENSE') (Join-Path $directory $installer) 'dev' } catch {
        if ($_.Exception.Message -notlike '*256 MiB*') { throw }
        $rejected = $true
    }
    if (-not $rejected) { throw 'An update package beyond the downloader limit was accepted.' }
    Write-Output 'Windows dual-package checks passed.'
} finally {
    Get-ChildItem -LiteralPath $directory -File | Remove-Item -Force
    Remove-Item -LiteralPath $directory
}
