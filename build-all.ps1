#!/usr/bin/env pwsh
# Cross-compile build script for Windows (PowerShell)
# Usage: .\build-all.ps1

$ErrorActionPreference = "Stop"

$module = "spacemolt-lt"
$outdir = "dist"
$version = "1.0.0"

Write-Host "=== SpaceMolt Bot Runner - Cross-Compile Build ===" -ForegroundColor Cyan

# Clean output dir
if (Test-Path $outdir) { Remove-Item $outdir -Recurse -Force }
New-Item -ItemType Directory -Path $outdir | Out-Null

$targets = @(
    @{ GOOS = "windows"; GOARCH = "amd64"; suffix = ".exe" },
    @{ GOOS = "linux";   GOARCH = "amd64"; suffix = ""     },
    @{ GOOS = "linux";   GOARCH = "arm64";  suffix = ""     },
)

foreach ($t in $targets) {
    $name = "${module}-$($t.GOOS)-$($t.GOARCH)$($t.suffix)"
    $out = Join-Path $outdir $name
    $env:GOOS = $t.GOOS
    $env:GOARCH = $t.GOARCH
    Write-Host "Building $name..." -ForegroundColor Yellow
    & go build -o $out ./cmd/spacemolt-lt
    if ($LASTEXITCODE -ne 0) {
        Write-Error "Build failed for $name"
    }
    Write-Host "  -> $out" -ForegroundColor Green
}

Write-Host "`n=== Build complete. Output in ./dist/ ===" -ForegroundColor Cyan
