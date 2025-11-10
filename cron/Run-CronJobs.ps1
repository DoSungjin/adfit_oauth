# AdFit Cron Jobs with Configuration (PowerShell)  
# PowerShell 전용 크론잡 스케줄러 실행 스크립트

param(
    [string]$Environment = "development",
    [string]$ConfigPath = "..\config\app_config.yaml"
)

Write-Host "🤖 AdFit 크론잡 스케줄러 (PowerShell) 시작..." -ForegroundColor Cyan
Write-Host "환경: $Environment" -ForegroundColor Yellow

# 설정 파일 존재 확인
if (-not (Test-Path $ConfigPath)) {
    Write-Host "❌ 오류: $ConfigPath 파일이 없습니다" -ForegroundColor Red
    Read-Host "계속하려면 Enter를 누르세요"
    exit 1
}
Write-Host "✅ 설정 파일 확인 완료: $ConfigPath" -ForegroundColor Green

# .env 파일 로드 (상위 디렉토리에서)
$envPath = "..\.env"
if (Test-Path $envPath) {
    Write-Host "📁 .env 파일 로드 중..." -ForegroundColor Yellow
    Get-Content $envPath | ForEach-Object {
        if ($_ -match "^([^#].*)=(.*)$") {
            $name = $matches[1].Trim()
            $value = $matches[2].Trim()
            [System.Environment]::SetEnvironmentVariable($name, $value, "Process")
            Write-Host "  $name = $($value.Substring(0, [Math]::Min(10, $value.Length)))..." -ForegroundColor Gray
        }
    }
    Write-Host "✅ 환경변수 로드 완료" -ForegroundColor Green
} else {
    Write-Host "⚠️ $envPath 파일이 없습니다. 기본 환경변수를 사용합니다." -ForegroundColor Yellow
}

# 필수 환경변수 확인
$youtubePath = [System.Environment]::GetEnvironmentVariable("YOUTUBE_API_KEY")
if ([string]::IsNullOrEmpty($youtubePath)) {
    Write-Host "⚠️ 경고: YOUTUBE_API_KEY 환경변수가 설정되지 않음" -ForegroundColor Yellow
    Write-Host "   통계 업데이트가 제한될 수 있습니다" -ForegroundColor Yellow
} else {
    Write-Host "✅ YouTube API 키 설정됨" -ForegroundColor Green
}

# 환경변수 설정
$env:ENVIRONMENT = $Environment

try {
    Write-Host "🔧 크론잡 스케줄러 시작 중..." -ForegroundColor Cyan
    
    # Go 모듈 확인
    if (-not (Test-Path "..\go.mod")) {
        Write-Host "❌ go.mod 파일이 없습니다. 프로젝트 루트를 확인하세요." -ForegroundColor Red
        exit 1
    }
    
    # 크론잡 실행
    Write-Host "▶️ go run main_with_config.go" -ForegroundColor White
    Write-Host "⏰ 예정된 스케줄:" -ForegroundColor Cyan
    Write-Host "  • 매시간 0분: 활성 대회 통계 업데이트" -ForegroundColor White
    Write-Host "  • 매일 오전 2시: 전체 시스템 통계 업데이트" -ForegroundColor White
    Write-Host "🛑 중지하려면 Ctrl+C를 누르세요`n" -ForegroundColor Yellow
    
    & go run main_with_config.go
    
} catch {
    Write-Host "❌ 크론잡 실행 오류: $($_.Exception.Message)" -ForegroundColor Red
    Write-Host "스택 트레이스: $($_.ScriptStackTrace)" -ForegroundColor Gray
} finally {
    Write-Host "`n🛑 크론잡 스케줄러가 종료되었습니다." -ForegroundColor Yellow
    Read-Host "계속하려면 Enter를 누르세요"
}
