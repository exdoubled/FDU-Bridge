. (Join-Path $PSScriptRoot 'installer.ps1')

function New-WindowsPackages([string]$OutputDirectory, [string]$LicensePath, [string]$InstallerPath, [string]$Version) {
    $prefix = if ($Version -eq 'dev') { 'FDU-Connect' } else { "FDU-Connect-$Version" }
    # Package public files explicitly; never archive the user's output directory.
    $files = @(
        (Join-Path $OutputDirectory 'FDU-Connect-GUI.exe'),
        (Join-Path $OutputDirectory 'fdu-connect.exe'),
        (Join-Path $OutputDirectory 'config.toml.example'),
        $LicensePath
    )
    $archive = Join-Path $OutputDirectory "$prefix-Windows-x64.zip"
    Compress-Archive -LiteralPath $files -DestinationPath $archive -Force
    # This filename is also used by older clients, whose download limit is 256 MiB.
    if ((Get-Item -LiteralPath $archive).Length -gt 256MB) {
        throw 'Windows update package exceeds the 256 MiB downloader limit.'
    }
    $setup = New-WindowsInstaller $OutputDirectory $LicensePath $InstallerPath $Version
    Write-Output $archive
    Write-Output $setup
}
