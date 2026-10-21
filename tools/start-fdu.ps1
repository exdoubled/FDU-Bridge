param([switch]$CheckOnly)
$ErrorActionPreference = 'Stop'
$targets = @(
    @{ File = 'fdu-connect-rules.exe'; Arguments = '-config "' + (Join-Path $PSScriptRoot 'fdu-rules.toml') + '"' },
    @{ File = 'FDU-Node-Picker.exe'; Arguments = '' }
)
foreach ($target in $targets) {
    $path = Join-Path $PSScriptRoot $target.File
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw "Missing program: $path" }
    $running = Get-CimInstance Win32_Process -Filter ("Name='" + $target.File + "'") |
        Where-Object { $_.ExecutablePath -ieq $path }
    if ($CheckOnly) {
        [PSCustomObject]@{ Program = $target.File; Running = [bool]$running; Path = $path }
    } elseif (-not $running) {
        $options = @{ FilePath = $path; WorkingDirectory = $PSScriptRoot; WindowStyle = 'Normal' }
        if ($target.Arguments) { $options.ArgumentList = $target.Arguments }
        Start-Process @options
    }
}
