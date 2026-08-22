# Starts local dev dependencies: Redis + Mailpit (PostgreSQL runs as a Windows service).
$ErrorActionPreference = "Stop"

function Start-Bg([string]$name, [string]$pattern, [scriptblock]$start) {
    if (Get-Process | Where-Object { $_.ProcessName -like $pattern }) {
        Write-Host "[ok] $name already running"
        return
    }
    & $start
    Write-Host "[ok] $name started"
}

Start-Bg "Redis (127.0.0.1:6379)" "redis-server" {
    Start-Process -FilePath (Get-Command redis-server).Source -ArgumentList "--port 6379 --save '' --appendonly no" -WindowStyle Hidden
}

Start-Bg "Mailpit (smtp 1025, ui 8025)" "mailpit" {
    Start-Process -FilePath (Get-Command mailpit).Source -ArgumentList "--smtp 127.0.0.1:1025 --listen 127.0.0.1:8025" -WindowStyle Hidden
}

Write-Host ""
Write-Host "PostgreSQL service: postgresql-x64-17 (port 5432)"
Write-Host "Mailpit UI:        http://localhost:8025"
