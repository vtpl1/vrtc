param(
    [string]$Package = "./internal/edge",
    [string]$Run = "",
    [string]$GoCacheDir = ".gocache",
    [string]$MingwBin = "C:\msys64\mingw64\bin",
    [string]$AVGrabberDir = "internal\avgrabber"
)

$ErrorActionPreference = "Stop"

$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$repoRoot = (Resolve-Path (Join-Path $scriptDir "..")).Path
$goCachePath = Join-Path $repoRoot $GoCacheDir
$avGrabberPath = Join-Path $repoRoot $AVGrabberDir
$avGrabberDll = Join-Path $avGrabberPath "AudioVideoGrabber2.dll"
$gcc = Join-Path $MingwBin "gcc.exe"
$gxx = Join-Path $MingwBin "g++.exe"

if (-not (Test-Path $gcc)) {
    throw "gcc not found at '$gcc'. Pass -MingwBin to the MSYS2 mingw64 bin directory."
}

if (-not (Test-Path $gxx)) {
    throw "g++ not found at '$gxx'. Pass -MingwBin to the MSYS2 mingw64 bin directory."
}

if (-not (Test-Path $avGrabberDll)) {
    throw "AudioVideoGrabber2.dll not found at '$avGrabberDll'. Pass -AVGrabberDir if the DLL lives elsewhere."
}

New-Item -ItemType Directory -Force $goCachePath | Out-Null

$env:GOCACHE = (Resolve-Path $goCachePath).Path
$env:CGO_ENABLED = "1"
$env:CC = $gcc
$env:CXX = $gxx
$env:PATH = "$MingwBin;$avGrabberPath;$env:PATH"

$goArgs = @("test", "-v")
if ($Run -ne "") {
    $goArgs += @("-run", $Run)
}
$goArgs += $Package

Write-Host "Running: go $($goArgs -join ' ')"
Write-Host "GOCACHE=$env:GOCACHE"
Write-Host "CC=$env:CC"
Write-Host "CXX=$env:CXX"

Push-Location $repoRoot
try {
    & go @goArgs
    exit $LASTEXITCODE
} finally {
    Pop-Location
}
