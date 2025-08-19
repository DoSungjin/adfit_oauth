# AdFit 개발 환경 전체 실행 스크립트
# API 서버와 크론잡을 동시에 실행 (PowerShell)

param(
    [string]$Environment = "development"
)

Write-Host "🚀 AdFit 전체 시스템 시작 (PowerShell)" -ForegroundColor Cyan
Write-Host "환경: $Environment" -ForegroundColor Yellow

# 관리자 권한 확인
if (-NOT ([Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole] "Administrator")) {
    Write-Host "⚠️ 권장: 관리자 권한으로 실행하면 더 안정적입니다" -ForegroundColor Yellow
}

# 필수 도구 확인
$tools = @("go", "git")
foreach ($tool in $tools) {
    try {
        $version = & $tool version 2>$null
        if ($LASTEXITCODE -eq 0) {
            Write-Host "✅ $tool 설치됨" -ForegroundColor Green
        }
    } catch {
        Write-Host "❌ $tool 가 설치되지 않았습니다" -ForegroundColor Red
        exit 1
    }
}

# 프로젝트 구조 확인
$requiredFiles = @(
    "config\app_config.yaml",
    "main_with_config.go",
    "cron\main_with_config.go"
)

foreach ($file in $requiredFiles) {
    if (-not (Test-Path $file)) {
        Write-Host "❌ 필수 파일이 없습니다: $file" -ForegroundColor Red
        exit 1
    }
}
Write-Host "✅ 프로젝트 구조 확인 완료" -ForegroundColor Green

# .env 파일 확인
if (-not (Test-Path ".env")) {
    Write-Host "⚠️ .env 파일이 없습니다. .env.config_example을 참고하세요" -ForegroundColor Yellow
    if (Test-Path ".env.config_example") {
        Write-Host "💡 다음 명령으로 .env 파일을 생성할 수 있습니다:" -ForegroundColor Cyan
        Write-Host "   Copy-Item .env.config_example .env" -ForegroundColor White
    }
}

Write-Host "`n🔧 시스템 시작 옵션:" -ForegroundColor Cyan
Write-Host "1. API 서버만 실행" -ForegroundColor White
Write-Host "2. 크론잡만 실행" -ForegroundColor White  
Write-Host "3. 둘 다 실행 (권장)" -ForegroundColor White
Write-Host "4. 종료" -ForegroundColor White

$choice = Read-Host "`n선택하세요 (1-4)"

switch ($choice) {
    "1" {
        Write-Host "▶️ API 서버 실행 중..." -ForegroundColor Cyan
        .\Run-Server.ps1 -Environment $Environment
    }
    "2" {
        Write-Host "▶️ 크론잡 실행 중..." -ForegroundColor Cyan
        Set-Location "cron"
        .\Run-CronJobs.ps1 -Environment $Environment
        Set-Location ".."
    }
    "3" {
        Write-Host "▶️ API 서버와 크론잡 동시 실행..." -ForegroundColor Cyan
        Write-Host "두 개의 PowerShell 창이 열립니다." -ForegroundColor Yellow
        
        # API 서버 실행 (새 창)
        Start-Process powershell -ArgumentList "-NoExit", "-Command", "cd '$PWD'; .\Run-Server.ps1 -Environment $Environment"
        
        # 잠시 대기
        Start-Sleep -Seconds 2
        
        # 크론잡 실행 (새 창)
        Start-Process powershell -ArgumentList "-NoExit", "-Command", "cd '$PWD\cron'; .\Run-CronJobs.ps1 -Environment $Environment"
        
        Write-Host "✅ 두 시스템이 별도 창에서 실행 중입니다" -ForegroundColor Green
        Write-Host "각 창을 닫으면 해당 서비스가 종료됩니다" -ForegroundColor Yellow
    }
    "4" {
        Write-Host "👋 종료합니다." -ForegroundColor Yellow
        exit 0
    }
    default {
        Write-Host "❌ 잘못된 선택입니다. 1-4 중 선택하세요." -ForegroundColor Red
        exit 1
    }
}

Write-Host "`n✅ 실행 완료" -ForegroundColor Green
Read-Host "계속하려면 Enter를 누르세요"
