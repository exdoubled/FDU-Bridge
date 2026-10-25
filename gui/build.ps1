param(
    [switch]$Test,
    [switch]$Package,
    [string]$OutputDirectory,
    [ValidatePattern('^(dev|v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?)$')]
    [string]$Version = 'dev'
)
$ErrorActionPreference = 'Stop'
$wailsVersion = 'v3.0.0-beta.34'
$root = Split-Path -Parent $PSScriptRoot
$prereleaseIdentifier = '(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)'
$semverPattern = '\Av(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-' + $prereleaseIdentifier + '(\.' + $prereleaseIdentifier + ')*)?\z'
if ($Version -cne 'dev' -and $Version -cnotmatch $semverPattern) { throw 'Version must be dev or vX.Y.Z[-suffix].' }
$baseVersion = $Version
if ($Version -eq 'dev') {
    $baseVersion = ''
    try {
        $tag = (& git -C $root describe --tags --match 'v*' --abbrev=0 2>$null | Out-String).Trim()
        if ($LASTEXITCODE -eq 0 -and $tag -cmatch $semverPattern) { $baseVersion = $tag }
    } catch { } # A development checkout may have no reachable version tag or Git installation.
}
$guiFlags = "-H windowsgui -s -w -X github.com/mythologyli/zju-connect/gui/internal/updates.Version=$Version -X github.com/mythologyli/zju-connect/gui/internal/updates.BaseVersion=$baseVersion"
$output = if ($OutputDirectory) { $OutputDirectory } else { Join-Path $root 'dist' }
New-Item -ItemType Directory -Path $output -Force | Out-Null
$output = (Resolve-Path -LiteralPath $output).Path
$programs = @((Join-Path $output 'FDU-Connect-GUI.exe'), (Join-Path $output 'fdu-connect.exe'))
$running = Get-CimInstance Win32_Process -Filter "Name='FDU-Connect-GUI.exe' OR Name='fdu-connect.exe'" |
    Where-Object { $_.ExecutablePath -and $programs -contains $_.ExecutablePath }
if ($running) { throw '输出目录中的程序仍在运行，请先退出，或用 -OutputDirectory 指定其他目录。' }

if ($Package) {
    if ($Test) {
        & (Join-Path $PSScriptRoot 'build\windows\webview2.test.ps1')
        & (Join-Path $PSScriptRoot 'build\windows\packages.test.ps1')
    }
    . (Join-Path $PSScriptRoot 'build\windows\webview2.ps1')
    $webView2Installer = Get-WebView2Installer $output
} else {
    Write-Host 'Development build: requires an existing WebView2 Runtime. Use the setup executable from a -Package build to install dependencies.'
}

function Invoke-Checked([string]$Description, [scriptblock]$Command) {
    & $Command
    if ($LASTEXITCODE -ne 0) { throw "$Description failed." }
}

$wails = Join-Path (go env GOPATH) 'bin\wails3.exe'
if ($installed = Get-Command wails3 -ErrorAction SilentlyContinue) {
    # wails3 prints its version on stderr; Windows PowerShell 5.1 would treat that as a terminating error.
    $ErrorActionPreference = 'Continue'
    $installedVersion = (& $installed.Source version 2>&1 | ForEach-Object { "$_" } | Out-String).Trim()
    $ErrorActionPreference = 'Stop'
    if ($LASTEXITCODE -eq 0 -and $installedVersion -eq $wailsVersion) { $wails = $installed.Source }
}
if (-not (Test-Path -LiteralPath $wails)) {
    Invoke-Checked 'Installing the Wails CLI' { go install "github.com/wailsapp/wails/v3/cmd/wails3@$wailsVersion" }
}

Push-Location $PSScriptRoot
try {
    # Go embeds this directory while generating bindings, before the first Vite build.
    $assetsDirectory = Join-Path $PSScriptRoot 'frontend\dist'
    New-Item -ItemType Directory -Path $assetsDirectory -Force | Out-Null
    if (-not (Get-ChildItem -LiteralPath $assetsDirectory -File)) {
        New-Item -ItemType File -Path (Join-Path $assetsDirectory '.gitkeep') -Force | Out-Null
    }
    # Bindings first: the frontend type-checks against them.
    Invoke-Checked 'Generating bindings' { & $wails generate bindings -silent -ts -d frontend/bindings ./... }
    Push-Location frontend
    try {
        Invoke-Checked 'Installing frontend packages' { pnpm install --frozen-lockfile }
        if ($Test) { Invoke-Checked 'Frontend tests' { pnpm test } }
        Invoke-Checked 'Frontend build' { pnpm build }
    } finally { Pop-Location }
    if ($Test) { Invoke-Checked 'GUI tests' { go test ./... } }
    $resourceInfo = Join-Path $PSScriptRoot 'build\windows\info.json'
    if ($Version -ne 'dev') {
        $versionInfo = Get-Content -LiteralPath $resourceInfo -Raw | ConvertFrom-Json
        $versionInfo.fixed.file_version = (($Version.Substring(1) -split '-', 2)[0] + '.0')
        $versionInfo.info.'0409'.ProductVersion = $Version.Substring(1)
        $resourceInfo = Join-Path $output 'windows-version.json'
        [IO.File]::WriteAllText($resourceInfo, ($versionInfo | ConvertTo-Json -Depth 5), [Text.UTF8Encoding]::new($false))
    }
    Invoke-Checked 'Windows resources' { & $wails generate syso -arch amd64 -icon build/windows/icon.ico -manifest build/windows/app.exe.manifest -info $resourceInfo -out rsrc_windows_amd64.syso }
    Invoke-Checked 'GUI build' { go build -tags production -trimpath -ldflags $guiFlags -o (Join-Path $output 'FDU-Connect-GUI.exe') . }
} finally { Pop-Location }

Push-Location $root
try {
    Invoke-Checked 'FDU core build' { go build -trimpath -ldflags "-X main.zjuConnectVersion=$Version" -o (Join-Path $output 'fdu-connect.exe') . }
    Copy-Item -LiteralPath 'config.toml.example' -Destination (Join-Path $output 'config.toml.example') -Force
} finally { Pop-Location }
Write-Output (Join-Path $output 'FDU-Connect-GUI.exe')
if ($Package) {
    if ($Test) { & (Join-Path $PSScriptRoot 'build\windows\installer.test.ps1') }
    . (Join-Path $PSScriptRoot 'build\windows\packages.ps1')
    New-WindowsPackages $output (Join-Path $root 'LICENSE') $webView2Installer.Path $Version
}
