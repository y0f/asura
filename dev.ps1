# dev.ps1 — single-command local development for Windows
# Watches .go and .templ files. Rebuilds and restarts on change.
# Usage: .\dev.ps1
# Stop:  Ctrl+C

$ErrorActionPreference = "Stop"
$binary = "asura-dev.exe"
$config = "config.yaml"
$proc = $null
$twProc = $null

function Cleanup {
    if ($proc -and !$proc.HasExited) { Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue }
    if ($twProc -and !$twProc.HasExited) { Stop-Process -Id $twProc.Id -Force -ErrorAction SilentlyContinue }
    Remove-Item -Force $binary -ErrorAction SilentlyContinue
}

trap { Cleanup; exit }

function Build-And-Run {
    if ($script:proc -and !$script:proc.HasExited) {
        Stop-Process -Id $script:proc.Id -Force -ErrorAction SilentlyContinue
        Start-Sleep -Milliseconds 500
    }
    Write-Host "[dev] building..." -ForegroundColor Cyan
    & templ generate 2>&1 | Select-Object -Last 1
    $env:CGO_ENABLED = "0"
    & go build -o $binary ./cmd/asura 2>&1
    if ($LASTEXITCODE -eq 0) {
        $listen = (Select-String -Path $config -Pattern "listen:" | Select-Object -First 1).ToString().Split('"')[1]
        Write-Host "[dev] starting server on $listen" -ForegroundColor Green
        $script:proc = Start-Process -FilePath ".\$binary" -ArgumentList "-config", $config -PassThru -NoNewWindow
    } else {
        Write-Host "[dev] build failed" -ForegroundColor Red
        $script:proc = $null
    }
}

# Start tailwind watcher
if (Test-Path ".\tailwindcss.exe") {
    $twProc = Start-Process -FilePath ".\tailwindcss.exe" -ArgumentList "-i", "web\tailwind.input.css", "-o", "web\static\tailwind.css", "--watch" -PassThru -NoNewWindow -RedirectStandardOutput "NUL"
    Write-Host "[dev] tailwind watcher started" -ForegroundColor Cyan
}

# Initial build
Build-And-Run

# Watch loop
Write-Host "[dev] watching for changes... (Ctrl+C to stop)" -ForegroundColor Yellow
$lastHash = ""
while ($true) {
    $files = Get-ChildItem -Recurse -Include "*.go","*.templ" -Path cmd,internal -ErrorAction SilentlyContinue
    $hash = ($files | ForEach-Object { "$($_.LastWriteTime)$($_.FullName)" }) -join "`n"
    if ($hash -ne $lastHash -and $lastHash -ne "") {
        Write-Host "`n[dev] change detected, rebuilding..." -ForegroundColor Yellow
        Build-And-Run
    }
    $lastHash = $hash
    Start-Sleep -Seconds 2
}
