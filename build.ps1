# Build the Vue admin UI and the Go server binary.
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $MyInvocation.MyCommand.Path

$web = Join-Path $root 'web'
Push-Location $web
try {
    if (-not (Test-Path 'node_modules')) {
        npm install
    }
    npm run build
}
finally {
    Pop-Location
}

$server = Join-Path $root 'server'
Push-Location $server
try {
    go build -trimpath -ldflags "-s -w" -o (Join-Path $root 'cdn-proxy.exe') .
}
finally {
    Pop-Location
}

Write-Host ''
Write-Host 'Build complete: cdn-proxy.exe' -ForegroundColor Green
Write-Host 'Run from the project root: .\cdn-proxy.exe'
