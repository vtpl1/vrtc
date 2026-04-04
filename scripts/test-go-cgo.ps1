[CmdletBinding(PositionalBinding = $false)]
param(
    [string]$Package = "./internal/edge",
    [string]$Run = "",
    [string]$GoCacheDir = ".gocache",
    [string]$MingwBin = "C:\msys64\mingw64\bin",
    [string]$AVGrabberDir = "internal\avgrabber"
)

$goArgs = @("test", "-v")
if ($Run -ne "") {
    $goArgs += @("-run", $Run)
}
$goArgs += $Package

$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$wrapper = Join-Path $scriptDir "go-cgo.ps1"

& powershell -ExecutionPolicy Bypass -File $wrapper `
    -GoCacheDir $GoCacheDir `
    -MingwBin $MingwBin `
    -AVGrabberDir $AVGrabberDir `
    @goArgs

exit $LASTEXITCODE
