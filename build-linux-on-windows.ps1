# 在 Windows 上构建 llama-proxy 的 PowerShell 脚本
# 使用方法: .\build-linux-on-windows.ps1

Write-Host "========================================" -ForegroundColor Cyan
Write-Host "  llama-proxy Build Script for Windows" -ForegroundColor Cyan
Write-Host "========================================" -ForegroundColor Cyan
Write-Host ""

# 保存原始环境变量（如果存在）
$originalGOOS = $env:GOOS
$originalGOARCH = $env:GOARCH
$originalCGO = $env:CGO_ENABLED

# 清理任何残留的交叉编译环境变量
Remove-Item Env:\GOOS -ErrorAction SilentlyContinue
Remove-Item Env:\GOARCH -ErrorAction SilentlyContinue
Remove-Item Env:\CGO_ENABLED -ErrorAction SilentlyContinue

# 创建 bin 目录
New-Item -ItemType Directory -Force -Path bin | Out-Null

# ========== 1. 构建 Windows 版本 (.exe) ==========
Write-Host "[1/2] Building Windows version (llama-proxy.exe)..." -ForegroundColor Green
go build -ldflags "-s -w" -o bin/llama-proxy.exe ./cmd/llama-proxy

if ($LASTEXITCODE -eq 0 -and (Test-Path "bin/llama-proxy.exe")) {
    $bytes = [System.IO.File]::ReadAllBytes("bin/llama-proxy.exe")[0..1]
    $hex = ($bytes | ForEach-Object { "0x{0:X2}" -f $_ }) -join " "

    if ($hex -eq "0x4D 0x5A") {
        $fileInfo = Get-Item "bin/llama-proxy.exe"
        Write-Host "  ✓ Windows exe built successfully" -ForegroundColor Green
        Write-Host "  File size: $([math]::Round($fileInfo.Length / 1MB, 2)) MB" -ForegroundColor Gray
    } else {
        Write-Host "  ✗ Warning: exe file may be corrupted (header: $hex)" -ForegroundColor Red
    }
} else {
    Write-Host "  ✗ Windows build failed!" -ForegroundColor Red
}

Write-Host ""

# ========== 2. 构建 Linux 版本 ==========
Write-Host "[2/2] Building Linux amd64 version..." -ForegroundColor Green

# 设置交叉编译环境变量
$env:GOOS = "linux"
$env:GOARCH = "amd64"
$env:CGO_ENABLED = "0"

go build -ldflags "-s -w" -o bin/llama-proxy-linux-amd64 ./cmd/llama-proxy

if ($LASTEXITCODE -eq 0 -and (Test-Path "bin/llama-proxy-linux-amd64")) {
    $bytes = [System.IO.File]::ReadAllBytes("bin/llama-proxy-linux-amd64")[0..3]
    $hex = ($bytes | ForEach-Object { "0x{0:X2}" -f $_ }) -join " "

    if ($hex -eq "0x7F 0x45 0x4C 0x46") {
        $fileInfo = Get-Item "bin/llama-proxy-linux-amd64"
        Write-Host "  ✓ Linux binary built successfully" -ForegroundColor Green
        Write-Host "  File size: $([math]::Round($fileInfo.Length / 1MB, 2)) MB" -ForegroundColor Gray
    } else {
        Write-Host "  ✗ Warning: Linux file may be corrupted (header: $hex)" -ForegroundColor Red
    }
} else {
    Write-Host "  ✗ Linux build failed!" -ForegroundColor Red
}

Write-Host ""
Write-Host "========================================" -ForegroundColor Cyan
Write-Host "Build Summary:" -ForegroundColor White
Write-Host "========================================" -ForegroundColor Cyan
Get-ChildItem bin/ | ForEach-Object {
    $size = "{0:N2}" -f ($_.Length / 1MB)
    Write-Host "  $($_.Name) ($size MB)" -ForegroundColor Gray
}

Write-Host ""
Write-Host "Deployment Instructions:" -ForegroundColor Yellow
Write-Host "------------------------" -ForegroundColor Yellow
Write-Host "Windows (本地):" -ForegroundColor White
Write-Host "  .\bin\llama-proxy.exe client --config config.yaml" -ForegroundColor Gray
Write-Host ""
Write-Host "Linux (服务器):" -ForegroundColor White
Write-Host "  scp bin/llama-proxy-linux-amd64 user@server:~/" -ForegroundColor Gray
Write-Host "  ssh user@server" -ForegroundColor Gray
Write-Host "  chmod +x llama-proxy-linux-amd64" -ForegroundColor Gray
Write-Host "  ./llama-proxy-linux-amd64 server --config server.yaml" -ForegroundColor Gray

# 恢复原始环境变量
if ($originalGOOS) { $env:GOOS = $originalGOOS } else { Remove-Item Env:\GOOS -ErrorAction SilentlyContinue }
if ($originalGOARCH) { $env:GOARCH = $originalGOARCH } else { Remove-Item Env:\GOARCH -ErrorAction SilentlyContinue }
if ($originalCGO) { $env:CGO_ENABLED = $originalCGO } else { Remove-Item Env:\CGO_ENABLED -ErrorAction SilentlyContinue }
