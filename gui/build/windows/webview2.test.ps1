$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'webview2.ps1')

function Assert-Throws([scriptblock]$Action, [string]$Message) {
    try { & $Action } catch {
        if ($_.Exception.Message -notlike "*$Message*") { throw }
        return
    }
    throw "Expected failure containing: $Message"
}

# Keep the checks offline: exercise trust failures without executing any installer.
$script:status = 'Valid'
$script:publisher = 'Microsoft Corporation'
$script:signatureFailure = $false
$script:downloadFailure = $false
$script:downloadScheme = 'https'
$script:downloadSize = 10MB
$script:downloads = 0
function Get-AuthenticodeSignature {
    param($LiteralPath, $ErrorAction)
    if ($script:signatureFailure) { throw 'Signature inspection failed.' }
    $certificate = [pscustomobject]@{}
    $certificate | Add-Member ScriptMethod GetNameInfo { param($Type, $Issuer) return $script:publisher }
    return [pscustomobject]@{ Status = $script:status; SignerCertificate = $certificate }
}
function Invoke-WebRequest {
    param($Uri, $OutFile, [switch]$UseBasicParsing, [switch]$PassThru, $ErrorAction)
    $script:downloads++
    $stream = [IO.File]::Create($OutFile)
    try { $stream.SetLength($script:downloadSize) } finally { $stream.Dispose() }
    if ($script:downloadFailure) { throw 'Download failed.' }
    return [pscustomobject]@{ BaseResponse = [pscustomobject]@{ ResponseUri = [uri]"$($script:downloadScheme)://example.com/installer.exe" } }
}

$directory = Join-Path ([IO.Path]::GetTempPath()) ('fdu-webview2-test-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $directory | Out-Null
try {
    $bundle = Get-WebView2Installer $directory
    if ((Split-Path -Leaf $bundle.Path) -cne 'MicrosoftEdgeWebView2RuntimeInstallerX64.exe') { throw 'Wrong installer filename.' }
    if ($bundle.SHA256 -cne (Get-FileHash -LiteralPath $bundle.Path -Algorithm SHA256).Hash.ToLowerInvariant()) { throw 'Wrong installer hash.' }
    $null = Get-WebView2Installer $directory
    if ($script:downloads -ne 1) { throw 'A valid cached installer was downloaded again.' }

    $script:status = 'HashMismatch'
    Assert-Throws { Get-WebView2Installer $directory } 'signature is not valid'
    $script:status = 'Valid'
    $script:publisher = 'Other Publisher'
    Assert-Throws { Get-WebView2Installer $directory } 'must be signed by Microsoft Corporation'
    $script:publisher = 'Microsoft Corporation'
    $script:signatureFailure = $true
    Assert-Throws { Get-WebView2Installer $directory } 'Signature inspection failed'
    $script:signatureFailure = $false

    Remove-Item -LiteralPath $bundle.Path
    $script:downloadSize = 2MB
    Assert-Throws { Get-WebView2Installer $directory } 'too small to be the full offline installer'
    if (Get-ChildItem -LiteralPath $directory) { throw 'Tiny bootstrapper was retained.' }
    $script:downloadSize = 10MB
    $script:status = 'NotSigned'
    Assert-Throws { Get-WebView2Installer $directory } 'signature is not valid'
    if (Get-ChildItem -LiteralPath $directory) { throw 'Untrusted download was retained.' }
    $script:status = 'Valid'
    $script:downloadScheme = 'http'
    Assert-Throws { Get-WebView2Installer $directory } 'must use HTTPS'
    if (Get-ChildItem -LiteralPath $directory) { throw 'Non-HTTPS download was retained.' }
    $script:downloadScheme = 'https'
    $script:downloadFailure = $true
    Assert-Throws { Get-WebView2Installer $directory } 'Download failed'
    if (Get-ChildItem -LiteralPath $directory) { throw 'Failed download was retained.' }
    Write-Output 'WebView2 packaging checks passed.'
} finally {
    Remove-Item -LiteralPath $directory
}
