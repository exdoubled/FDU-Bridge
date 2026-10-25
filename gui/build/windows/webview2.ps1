function Get-WebView2InstallerHash([string]$Path) {
    $signature = Get-AuthenticodeSignature -LiteralPath $Path -ErrorAction Stop
    if ($signature.Status -ne 'Valid') {
        throw "WebView2 installer signature is not valid: $($signature.Status)."
    }
    if (-not $signature.SignerCertificate -or
        $signature.SignerCertificate.GetNameInfo([Security.Cryptography.X509Certificates.X509NameType]::SimpleName, $false) -cne 'Microsoft Corporation') {
        throw 'WebView2 installer must be signed by Microsoft Corporation.'
    }
    if ((Get-Item -LiteralPath $Path -ErrorAction Stop).Length -lt 10MB) {
        throw 'WebView2 installer is too small to be the full offline installer.'
    }
    return (Get-FileHash -LiteralPath $Path -Algorithm SHA256 -ErrorAction Stop).Hash.ToLowerInvariant()
}

function Get-WebView2Installer([string]$OutputDirectory) {
    $installer = Join-Path $OutputDirectory 'MicrosoftEdgeWebView2RuntimeInstallerX64.exe'
    if (Test-Path -LiteralPath $installer) {
        $hash = Get-WebView2InstallerHash $installer
    } else {
        # Microsoft's x64 Evergreen Standalone Installer, not the online bootstrapper.
        # https://developer.microsoft.com/en-us/microsoft-edge/webview2/
        $source = [uri]'https://go.microsoft.com/fwlink/?linkid=2124701'
        $temporary = Join-Path $OutputDirectory ('.webview2-' + [guid]::NewGuid().ToString('N') + '.exe')
        $savedProgress = $ProgressPreference
        try {
            $ProgressPreference = 'SilentlyContinue'
            Write-Host 'Downloading the WebView2 x64 offline installer from Microsoft...'
            $download = Invoke-WebRequest -Uri $source -OutFile $temporary -UseBasicParsing -PassThru -ErrorAction Stop
            $downloadUri = if ($download.BaseResponse.ResponseUri) {
                $download.BaseResponse.ResponseUri # Windows PowerShell 5.1
            } else {
                $download.BaseResponse.RequestMessage.RequestUri # PowerShell 7
            }
            if ($downloadUri.Scheme -ne 'https') { throw 'WebView2 installer download must use HTTPS.' }
            $hash = Get-WebView2InstallerHash $temporary
            Move-Item -LiteralPath $temporary -Destination $installer -Force -ErrorAction Stop
        } finally {
            $ProgressPreference = $savedProgress
            if (Test-Path -LiteralPath $temporary) { Remove-Item -LiteralPath $temporary -Force }
        }
    }
    Write-Host "WebView2 offline installer SHA256: $hash"
    return [pscustomobject]@{ Path = $installer; SHA256 = $hash }
}
