param([switch]$Test, [string]$OutputDirectory)
$ErrorActionPreference = 'Stop'
$compiler = Join-Path $env:WINDIR 'Microsoft.NET\Framework64\v4.0.30319\csc.exe'
if (-not (Test-Path -LiteralPath $compiler)) {
    $compiler = Join-Path $env:WINDIR 'Microsoft.NET\Framework\v4.0.30319\csc.exe'
}
if (-not (Test-Path -LiteralPath $compiler)) { throw 'Windows .NET Framework compiler not found.' }
$output = if ($OutputDirectory) { $OutputDirectory } else { Join-Path $PSScriptRoot 'bin' }
New-Item -ItemType Directory -Path $output -Force | Out-Null
$references = @('/r:System.dll', '/r:System.Core.dll', '/r:System.Web.Extensions.dll', '/r:System.Windows.Forms.dll', '/r:System.Drawing.dll')
$sources = @(Join-Path $PSScriptRoot 'PickerClient.cs')
$window = Join-Path $PSScriptRoot 'PickerWindow.cs'
if (Test-Path -LiteralPath $window) { $sources += $window }
foreach ($extra in @('RulesStore.cs', 'RulesPanel.cs')) {
    $extraPath = Join-Path $PSScriptRoot $extra
    if (Test-Path -LiteralPath $extraPath) { $sources += $extraPath }
}
if ($Test) {
    $sources += Join-Path $PSScriptRoot 'PickerTests.cs'
    $rulesTests = Join-Path $PSScriptRoot 'RulesTests.cs'
    if (Test-Path -LiteralPath $rulesTests) { $sources += $rulesTests }
    $target = Join-Path $output 'PickerTests.exe'
    & $compiler /nologo /utf8output /target:exe /main:FduNodePicker.PickerTests "/out:$target" @references @sources
    if ($LASTEXITCODE -ne 0) { throw 'Test compilation failed.' }
    & $target
    if ($LASTEXITCODE -ne 0) { throw 'Node picker tests failed.' }
} else {
    $target = Join-Path $output 'FDU-Node-Picker.exe'
    & $compiler /nologo /utf8output /target:winexe /main:FduNodePicker.Program "/out:$target" @references @sources
    if ($LASTEXITCODE -ne 0) { throw 'Build failed.' }
    Write-Output $target
}
