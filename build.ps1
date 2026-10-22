param(
    [switch]$Test,
    [switch]$Package,
    [string]$OutputDirectory,
    [ValidatePattern('^(dev|v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?)$')]
    [string]$Version = 'dev'
)
$ErrorActionPreference = 'Stop'
& (Join-Path $PSScriptRoot 'gui\build.ps1') @PSBoundParameters
