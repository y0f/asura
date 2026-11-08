# Rebuild templates, CSS and the embedded server when application sources change.
$ErrorActionPreference = 'Stop'
Set-Location -LiteralPath $PSScriptRoot
$binary = Join-Path $PSScriptRoot 'asura-dev.exe'
$next = Join-Path $PSScriptRoot 'asura-dev.next.exe'
$tailwind = Join-Path $PSScriptRoot 'tailwindcss.exe'
$generatedCSS = [IO.Path]::Combine($PSScriptRoot, 'web', 'static', 'tailwind.css')
$proc = $null
if (!(Test-Path -LiteralPath $tailwind)) { throw 'Download the standalone Tailwind CLI first (see CONTRIBUTING.md).' }

function Stop-Server {
    if ($script:proc) {
        if (!$script:proc.HasExited) {
            Stop-Process -Id $script:proc.Id -ErrorAction SilentlyContinue
            $script:proc.WaitForExit()
        }
        $script:proc.Dispose()
    }
    $script:proc = $null
}
function Build-And-Run {
    $templVersion = & go list -m -f '{{.Version}}' github.com/a-h/templ
    if ($LASTEXITCODE -ne 0) { return $false }
    Write-Host '[dev] generating templates and CSS...'
    & go run "github.com/a-h/templ/cmd/templ@$templVersion" generate | Out-Host
    if ($LASTEXITCODE -ne 0) { return $false }
    & $tailwind -i web/tailwind.input.css -o web/static/tailwind.css | Out-Host
    if ($LASTEXITCODE -ne 0) { return $false }
    Write-Host '[dev] building...'
    $previousCGO = $env:CGO_ENABLED
    try {
        $env:CGO_ENABLED = '0'
        & go build -o $next ./cmd/asura | Out-Host
        if ($LASTEXITCODE -ne 0) { return $false }
    } finally { $env:CGO_ENABLED = $previousCGO }
    Stop-Server
    Move-Item -LiteralPath $next -Destination $binary -Force
    Write-Host '[dev] starting server...'
    $script:proc = Start-Process -FilePath $binary -ArgumentList '-config', 'config.yaml' -PassThru -WindowStyle Hidden
    return $true
}
function Get-Snapshot {
    $files = @(Get-ChildItem -Path cmd,internal,web -Recurse -File | Where-Object { $_.Name -notlike '*_templ.go' -and $_.FullName -ne $generatedCSS })
    $files += Get-Item -LiteralPath go.mod,go.sum
    return ($files | Sort-Object FullName | ForEach-Object { "$($_.FullName):$($_.LastWriteTimeUtc.Ticks):$($_.Length)" }) -join "`n"
}

try {
    $lastHash = Get-Snapshot
    if (!(Build-And-Run)) { Write-Host '[dev] build failed; waiting for a source change.' }
    Write-Host '[dev] watching application sources and embedded assets (Ctrl+C to stop)...'
    while ($true) {
        Start-Sleep -Seconds 2
        $hash = Get-Snapshot
        if ($hash -ne $lastHash) {
            $lastHash = $hash
            if (!(Build-And-Run)) { Write-Host '[dev] build failed; keeping the previous server.' }
        }
    }
} finally {
    Stop-Server
    Remove-Item -LiteralPath $binary,$next -Force -ErrorAction SilentlyContinue
}
