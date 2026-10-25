function New-SetupLauncher([string]$PayloadPath, [string]$OutputPath, [string]$Version, [string]$NumericVersion) {
    Add-Type -AssemblyName System.IO.Compression
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $outputDirectory = [IO.Path]::GetFullPath((Split-Path -Parent $OutputPath))
    $work = Join-Path $outputDirectory ('.fdu-launcher-' + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $work | Out-Null
    try {
        $source = Join-Path $PSScriptRoot 'setuplauncher'
        Get-ChildItem -LiteralPath $source -Filter '*.go' -File | Where-Object Name -NotLike '*_test.go' |
            Copy-Item -Destination $work
        [IO.File]::WriteAllText((Join-Path $work 'go.mod'), "module fdu-connect-setup`n`ngo 1.26.0`n", [Text.UTF8Encoding]::new($false))
        $info = Get-Content -LiteralPath (Join-Path $PSScriptRoot 'info.json') -Raw -Encoding UTF8 | ConvertFrom-Json
        $info.fixed.file_version = $NumericVersion
        $info.info.'0409'.ProductVersion = $Version
        $info.info.'0409'.FileDescription = 'FDU Connect Setup'
        $infoPath = Join-Path $work 'info.json'
        [IO.File]::WriteAllText($infoPath, ($info | ConvertTo-Json -Depth 5), [Text.UTF8Encoding]::new($false))
        $wailsCommand = Get-Command wails3.exe -CommandType Application -ErrorAction SilentlyContinue
        $wails = if ($wailsCommand) { $wailsCommand.Source } else { Join-Path (go env GOPATH) 'bin\wails3.exe' }
        if (-not (Test-Path -LiteralPath $wails -PathType Leaf)) { throw 'The Wails CLI used to build the GUI is required to generate setup resources.' }
        & $wails generate syso -arch amd64 -icon (Join-Path $PSScriptRoot 'icon.ico') `
            -manifest (Join-Path $source 'setup.manifest') -info $infoPath -out (Join-Path $work 'rsrc_windows_amd64.syso')
        if ($LASTEXITCODE -ne 0) { throw 'Setup launcher resource generation failed.' }
        Push-Location $work
        try {
            & go build -trimpath -buildvcs=false -ldflags '-H windowsgui -s -w' -o $OutputPath .
            if ($LASTEXITCODE -ne 0) { throw 'Setup launcher compilation failed.' }
        } finally { Pop-Location }
        $bundle = Join-Path $work 'payload.zip'
        $zip = [IO.Compression.ZipFile]::Open($bundle, [IO.Compression.ZipArchiveMode]::Create)
        try {
            $null = [IO.Compression.ZipFileExtensions]::CreateEntryFromFile($zip, $PayloadPath, 'setup.exe', [IO.Compression.CompressionLevel]::NoCompression)
        } finally { $zip.Dispose() }
        $payloadStream = [IO.File]::OpenRead($bundle)
        try {
            $launcherStream = [IO.File]::Open($OutputPath, [IO.FileMode]::Append, [IO.FileAccess]::Write, [IO.FileShare]::None)
            try { $payloadStream.CopyTo($launcherStream) } finally { $launcherStream.Dispose() }
        } finally { $payloadStream.Dispose() }
    } finally {
        $resolved = [IO.Path]::GetFullPath($work)
        if ([IO.Path]::GetDirectoryName($resolved) -cne $outputDirectory -or
            [IO.Path]::GetFileName($resolved) -notlike '.fdu-launcher-*') { throw 'Refusing cleanup outside the setup build directory.' }
        Remove-Item -LiteralPath $resolved -Recurse -Force
    }
}
